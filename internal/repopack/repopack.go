// Package repopack 把一个 git 仓库转换成可直接静态托管的裸仓库。
//
// 产物只包含 dumb HTTP 协议真正会被请求的文件：
//
//	HEAD
//	info/refs
//	objects/info/packs
//	objects/pack/pack-<40位hex>.idx
//	objects/pack/pack-<40位hex>.pack
//
// 其中 pack 文件名里的 hex 必须是合法的 40 位十六进制，否则 git 客户端
// 解析 objects/info/packs 时会静默跳过该项目。
package repopack

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

// stateDir 是站点里存放工具状态的目录，不属于站点内容，不参与发布。
// 与 publish 包的约定保持一致。
const stateDir = ".rog"

// Options 是一次打包的输入。
type Options struct {
	Source string                           // 源仓库：本地路径，或 https/git/ssh/file 与 scp 风格地址
	OutDir string                           // 站点根目录，即放着 index.html 的那个
	Name   string                           // 对外标识；留空则从源推导
	Logf   func(format string, args ...any) // 日志回调，可为 nil

	// Rebuild 为真时丢掉已有产物，从零重建。
	//
	// 默认是全自动的：目标里已经有一个能用的仓库就做增量（保留旧 pack，
	// 内容寻址的旧数据才能复用），没有就全量。原先把这个选择交给调用方
	// 是个错位——该不该增量取决于磁盘上有没有旧 pack，
	// 而这个事实工具自己最清楚，让用户猜只会得到两种坏结果：
	// 该增量时他选了全量（白传一遍），或反过来选了增量但根本没有旧数据。
	Rebuild bool
}

// Result 描述一次打包的产物。
type Result struct {
	Name      string   // 裸名
	Target    string   // 裸仓库绝对路径
	Branch    string   // 默认分支
	Files     []string // 产物相对路径（已排序）
	Packs     []string // pack 文件名（已排序），增量时用于观察新增
	TotalSize int64    // 产物总字节数
	Via       string   // 实际使用的链路：clone / bundle / incremental
}

var (
	gitSuffixRe = regexp.MustCompile(`(?i)(\.git)+$`)
	remoteURLRe = regexp.MustCompile(`(?i)^(https?|git|ssh|file)://`)
	remoteSCPRe = regexp.MustCompile(`^[\w.+-]+@[\w.-]+:`)
	refLineRe   = regexp.MustCompile(`^\s*([0-9a-f]{40})\s+(\S+)\s*$`)
)

// noAuxIndex 让 git 不要生成 dumb 协议用不到的辅助索引。
// 直接不生成比事后删更干净，也避开了删不掉的情况。
var noAuxIndex = []string{
	"-c", "pack.writeBitmaps=false",
	"-c", "repack.writeBitmaps=false",
	"-c", "pack.writeReverseIndex=false",
	"-c", "gc.writeCommitGraph=false",
}

// junk 是协议不会请求的路径，清掉让产物保持最小。
var junkDirNames = []string{"hooks", "logs", "index", "COMMIT_EDITMSG", "description"}

var junkFilePaths = []string{
	filepath.Join("info", "exclude"),
	filepath.Join("objects", "info", "commit-graph"),
}

var junkPackSuffixes = []string{".bitmap", ".rev"}

// requiredPaths 是产物必须存在的文件。
var requiredPaths = []string{
	"HEAD",
	filepath.Join("info", "refs"),
	filepath.Join("objects", "info", "packs"),
}

// Pack 执行一次完整的打包。
func Pack(opt Options) (*Result, error) {
	logf := opt.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	if strings.TrimSpace(opt.Source) == "" {
		return nil, fmt.Errorf("源仓库不能为空")
	}

	name := NormalizeName(opt.Name)
	src := ""
	remote := IsRemote(opt.Source)

	if remote {
		if name == "" {
			name = RemoteName(opt.Source)
		}
	} else {
		abs, err := filepath.Abs(opt.Source)
		if err != nil {
			return nil, err
		}
		if !dirExists(abs) {
			return nil, fmt.Errorf("源仓库不存在: %s", abs)
		}
		if err := checkSource(abs); err != nil {
			return nil, err
		}
		src = abs
		if name == "" {
			name = NormalizeName(filepath.Base(abs))
		}
	}

	if !isValidName(name) {
		return nil, fmt.Errorf("非法仓库名: %q", name)
	}

	outRoot, err := filepath.Abs(opt.OutDir)
	if err != nil {
		return nil, err
	}
	// 目录名就是仓库名，不加 .git 后缀。
	//
	// git 对 URL 后缀没有要求：dumb 协议下客户端只会往 base URL 后面拼
	// info/refs、objects/info/packs 这些固定路径（见 git 的 http.c，它只做
	// end_url_with_slash 再添上相对路径），目录叫什么都能 clone。
	// 而 .git 后缀会被一些网关拦掉，eth.limo 就是一例。
	// 既然两种写法等价，就用不会被拦的那种。
	target := filepath.Join(outRoot, name)

	// Windows 默认路径上限是 260 字符，但实际更早就会出问题：产物内部还有
	// objects/pack/pack-<40hex>.pack（约 60 字符）以及 git 自己的临时 .lock，
	// 实测目标路径到 185 左右就开始抛原始 git 错误。留足余量按 180 卡。
	if len(target) > 180 {
		return nil, fmt.Errorf("目标路径过长（%d 字符），请换一个更短的输出目录或仓库名: %s", len(target), target)
	}

	logf("源仓库   %s", opt.Source)
	logf("目标     %s", target)

	// 默认自动：目标已经是一个能用的仓库就增量，否则全量重建。
	// 远端源（clone）不参与增量，它没有本地旧 pack 可复用。
	incremental := !opt.Rebuild && !remote && isUsableTarget(target)
	if opt.Rebuild && !remote {
		logf("> 完整重打包：忽略已有产物")
	}

	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		return nil, err
	}

	// 锁要罩住整个重建过程，包括下面的 RemoveAll：两个进程同时看到
	// 不完整的目标再各自重建，会把对方的中间态当输入。
	unlock, err := acquireLock(outRoot, name)
	if err != nil {
		return nil, err
	}
	defer unlock()

	if !incremental {
		if err := removeDirRetry(target); err != nil {
			return nil, err
		}
	}

	via := ""
	switch {
	case incremental:
		logf("> 增量更新已有仓库")
		if err := fetchInto(target, opt.Source, logf); err != nil {
			return nil, err
		}
		via = "incremental"

	case remote:
		logf("> 从远端克隆（远端源不做增量）")
		if _, err := runGit("", "clone", "--bare", "--quiet", opt.Source, target); err != nil {
			os.RemoveAll(target)
			return nil, fmt.Errorf("%w\n  检查地址是否写对、网络是否可达。\n  私有仓库需要先让 git 自己拿到凭据", err)
		}
		via = "clone"

	default:
		via, err = buildBare(src, target, logf)
		if err != nil {
			return nil, err
		}
	}

	// 全量时才收拢对象。增量刻意跳过 repack 与 gc：旧 pack 一旦被合并重写，
	// 它的内容就变了，内容寻址存储上那一份也就无法复用。
	if !incremental {
		if _, err := gitIn(target, gitArgs("repack", "-a", "-d", "-q")...); err != nil {
			return nil, err
		}
		if _, err := gitIn(target, gitArgs("gc", "--prune=now", "--quiet")...); err != nil {
			return nil, err
		}
	}
	if _, err := gitIn(target, gitArgs("update-server-info")...); err != nil {
		return nil, err
	}

	prune(target)

	for _, rel := range requiredPaths {
		if !fileExists(filepath.Join(target, rel)) {
			return nil, fmt.Errorf("缺少必需文件: %s", rel)
		}
	}

	files, total, err := walkFiles(target)
	if err != nil {
		return nil, err
	}

	if err := updateRegistry(outRoot, name); err != nil {
		return nil, err
	}

	branch := ""
	if out, err := gitIn(target, "symbolic-ref", "--short", "HEAD"); err == nil {
		branch = strings.TrimSpace(out)
	}

	return &Result{
		Name:      name,
		Target:    target,
		Branch:    branch,
		Files:     files,
		Packs:     listPacks(target),
		TotalSize: total,
		Via:       via,
	}, nil
}

// buildBare 把本地源仓库变成裸仓库，返回实际使用的链路。
func buildBare(src, target string, logf func(string, ...any)) (string, error) {
	// 首选 clone --bare
	if _, err := runGit("", "clone", "--bare", "--quiet", src, target); err == nil {
		return "clone", nil
	}

	// 回退：某些环境（沙箱、受限的 sh.exe）下本地传输建不出管道。
	logf("> clone --bare 不可用，回退到 bundle 链路")
	if err := removeDirRetry(target); err != nil {
		return "", err
	}

	// 源没有任何 ref（空仓库）：bundle 不接受空内容，直接造一个空壳。
	srcGitDir, err := gitDirOf(src)
	if err != nil {
		return "", err
	}
	srcRefs, err := refMapOf(srcGitDir)
	if err != nil {
		return "", err
	}
	if len(srcRefs) == 0 {
		logf("> 源仓库没有任何 ref，产出空仓库")
		if _, err := runGit("", "init", "--bare", "--quiet", target); err != nil {
			return "", err
		}
		if out, err := runGit(src, "symbolic-ref", "HEAD"); err == nil {
			if ref := strings.TrimSpace(out); ref != "" {
				_, _ = gitIn(target, "symbolic-ref", "HEAD", ref)
			}
		}
		return "empty", nil
	}

	// 临时 bundle 放输出目录旁边：某些环境不允许写系统 TEMP。
	bundle := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".snapshot.bundle")
	defer os.Remove(bundle)

	if _, err := runGit(src, "bundle", "create", bundle, "--branches", "--tags"); err != nil {
		return "", err
	}
	if _, err := runGit("", "init", "--bare", "--quiet", target); err != nil {
		return "", err
	}

	// bundle unbundle 只把 refs 列到 stdout，并不会创建它们。
	// 不显式 update-ref，后面 gc --prune=now 会把所有对象当不可达清掉。
	listing, err := gitIn(target, "bundle", "unbundle", bundle)
	if err != nil {
		return "", err
	}
	created := 0
	for _, line := range strings.Split(listing, "\n") {
		m := refLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, err := gitIn(target, "update-ref", m[2], m[1]); err != nil {
			return "", err
		}
		created++
	}
	if created == 0 {
		return "", fmt.Errorf("bundle 里没有任何 ref")
	}

	headRef, err := runGit(src, "symbolic-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if _, err := gitIn(target, "symbolic-ref", "HEAD", strings.TrimSpace(headRef)); err != nil {
		return "", err
	}

	return "bundle", nil
}

// isBareRepo 判断目标是不是一个可用的裸仓库。
//
// 必须用 --git-dir 明确指定：若改用 -C 切工作目录，git 会向上层目录发现父仓库，
// 输出目录一旦嵌在别的仓库里就会被误判成可用，后续还会读到父仓库的 refs。
func isBareRepo(path string) bool {
	if !dirExists(path) || !fileExists(filepath.Join(path, "HEAD")) {
		return false
	}
	out, err := gitIn(path, "rev-parse", "--is-bare-repository")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "true"
}

// isUsableTarget 判断目标能否安全地做增量复用。
//
// 除了结构完整，还要过一遍 fsck 的连通性检查：refs 与文件都在、但 pack 数据
// 已经损坏的目标如果被当成可用，增量会「成功」地留下一个拉不动的产物。
func isUsableTarget(path string) bool {
	if !isBareRepo(path) {
		return false
	}
	if _, err := gitIn(path, "fsck", "--connectivity-only", "--no-progress", "--no-dangling"); err != nil {
		return false
	}
	return true
}

// gitDirOf 解析出仓库实际的 git 目录：裸仓库返回自身，普通仓库返回其 .git。
//
// 只有「已经过 checkSource 验证」的源仓库可以走这里。产物目录不能用它：
// 产物一旦损坏，git 会向上层发现父仓库，把无关的 refs 当成自己的。
func gitDirOf(repo string) (string, error) {
	out, err := runGit(repo, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// refMapOf 返回 gitDir 里 ref 的映射：完整 ref 名 -> 对象 id。
//
// 范围限定为 refs/heads 与 refs/tags。这一范围必须与「对象传输用 --branches --tags」
// 以及「全量产物由 clone --bare 生成」三者一致：若这里取全部 ref，refs/remotes、
// refs/stash 这类本地状态会被要求对齐，而它们指向的对象从未被传输，
// update-ref 会以 "nonexistent object" 永久失败。
func refMapOf(gitDir string) (map[string]string, error) {
	out, err := gitIn(gitDir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads/", "refs/tags/")
	if err != nil {
		return nil, err
	}
	refs := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		refs[parts[0]] = parts[1]
	}
	return refs, nil
}

// fetchInto 在已有裸仓库上做增量更新。
//
// 对象与 ref 必须分开同步。bundle 的 --not 是对象层面的排除，它会连带丢掉
// 「指向被排除对象」的那些 ref：源新增一个指向已有提交的分支时，那个分支进不了
// bundle；源删掉一个 ref 时，目标里的旧 ref 也不会自动消失。所以这里只让 bundle
// 负责补对象，ref 一律按源无条件对齐。
func fetchInto(target, source string, logf func(string, ...any)) error {
	srcGitDir, err := gitDirOf(source)
	if err != nil {
		return err
	}
	srcRefs, err := refMapOf(srcGitDir)
	if err != nil {
		return err
	}
	// 目标必然是裸仓库（调用前已确认），gitDir 就是它自己。
	dstRefs, err := refMapOf(target)
	if err != nil {
		return err
	}

	if refMapsEqual(srcRefs, dstRefs) {
		logf("> 目标已是最新，无需更新")
		return nil
	}

	if err := transferObjects(target, source, dstRefs); err != nil {
		return err
	}

	added, updated, deleted, err := syncRefs(target, srcRefs, dstRefs)
	if err != nil {
		return err
	}

	// HEAD 跟随源仓库，避免源改了默认分支后目标仍指向旧分支。
	if out, err := runGit(source, "symbolic-ref", "HEAD"); err == nil {
		if ref := strings.TrimSpace(out); ref != "" {
			_, _ = gitIn(target, "symbolic-ref", "HEAD", ref)
		}
	}

	// 把松散 ref 收进 packed-refs，保持产物形态与全量一致。
	if _, err := gitIn(target, "pack-refs", "--all"); err != nil {
		return err
	}

	logf("> 增量完成，新增 %d / 更新 %d / 删除 %d 个 ref，当前 pack %d 个",
		added, updated, deleted, len(listPacks(target)))
	return nil
}

// transferObjects 把源里目标还没有的对象补进目标，形成独立的新 pack。
func transferObjects(target, source string, dstRefs map[string]string) error {
	bundle := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".inc.bundle")
	defer os.Remove(bundle)

	args := []string{"bundle", "create", bundle, "--branches", "--tags"}
	if len(dstRefs) > 0 {
		excludes := make([]string, 0, len(dstRefs))
		for _, sha := range dstRefs {
			excludes = append(excludes, sha)
		}
		sort.Strings(excludes) // 稳定化，便于复现
		args = append(args, "--not")
		args = append(args, excludes...)
	}

	if _, err := runGit(source, args...); err != nil {
		// 目标已拥有全部对象时，git 会拒绝创建空 bundle。这不是错误。
		if strings.Contains(err.Error(), "empty bundle") {
			return nil
		}
		return err
	}

	if _, err := gitIn(target, "bundle", "unbundle", bundle); err != nil {
		return err
	}
	return nil
}

// syncRefs 让目标的 ref 与源完全对齐，返回新增/更新/删除的个数。
func syncRefs(target string, src, dst map[string]string) (added, updated, deleted int, err error) {
	names := make([]string, 0, len(src))
	for name := range src {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		sha := src[name]
		old, exists := dst[name]
		if exists && old == sha {
			continue
		}
		if _, e := gitIn(target, "update-ref", name, sha); e != nil {
			return added, updated, deleted, fmt.Errorf("对齐 ref %s 失败: %w", name, e)
		}
		if exists {
			updated++
		} else {
			added++
		}
	}

	// 源里已经没有的 ref，从目标删掉，保持与源一致。
	gone := make([]string, 0)
	for name := range dst {
		if _, ok := src[name]; !ok {
			gone = append(gone, name)
		}
	}
	sort.Strings(gone)
	for _, name := range gone {
		if _, e := gitIn(target, "update-ref", "-d", name); e != nil {
			return added, updated, deleted, fmt.Errorf("删除 ref %s 失败: %w", name, e)
		}
		deleted++
	}

	return added, updated, deleted, nil
}

// refMapsEqual 比较两个 ref 映射是否完全一致。
func refMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// listPacks 返回 objects/pack 下的 pack 文件名（已排序）。
func listPacks(target string) []string {
	entries, err := os.ReadDir(filepath.Join(target, "objects", "pack"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pack") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// checkSource 在打包前确认源仓库可用，报错要早于 git 的内部错误。
func checkSource(src string) error {
	if fileExists(filepath.Join(src, ".git")) {
		return fmt.Errorf("不支持 worktree 检出（.git 是文件），请改用主仓库目录: %s", src)
	}
	if !dirExists(filepath.Join(src, ".git")) && !fileExists(filepath.Join(src, "HEAD")) {
		return fmt.Errorf("不是 git 仓库: %s", src)
	}

	// 浅克隆只有部分历史，repack 遍历父提交时会报 Could not read <sha>，
	// 那句话看不出是深度造成的，所以在这里提前拦下。
	if fileExists(filepath.Join(src, ".git", "shallow")) || fileExists(filepath.Join(src, "shallow")) {
		return fmt.Errorf("源仓库是浅克隆，历史不完整，打包会失败。\n"+
			"  先补全历史: git -C %q fetch --unshallow", src)
	}

	if _, err := runGit(src, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("这个目录不是一个可用的 git 仓库: %s", src)
	}
	return nil
}

// prune 清掉 dumb 协议不会请求的文件。清理是尽力而为，失败不中断。
func prune(target string) {
	for _, name := range junkDirNames {
		_ = os.RemoveAll(filepath.Join(target, name))
	}
	for _, rel := range junkFilePaths {
		_ = os.Remove(filepath.Join(target, rel))
	}
	packDir := filepath.Join(target, "objects", "pack")
	entries, err := os.ReadDir(packDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		for _, suffix := range junkPackSuffixes {
			if strings.HasSuffix(e.Name(), suffix) {
				_ = os.Remove(filepath.Join(packDir, e.Name()))
			}
		}
	}

	// 注意：refs/ 目录即使空了也必须保留。git 靠它识别仓库，
	// 删掉之后 rev-parse 会直接报 "not a git repository"。
}

// walkFiles 返回产物的相对路径（已排序）与总字节数。
func walkFiles(root string) ([]string, int64, error) {
	var files []string
	var total int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	sort.Strings(files)
	return files, total, err
}

// ---------- git 调用 ----------

// gitArgs 在命令前拼上抑制辅助索引的 -c 配置。
func gitArgs(cmd ...string) []string {
	out := make([]string, 0, len(noAuxIndex)+len(cmd))
	out = append(out, noAuxIndex...)
	return append(out, cmd...)
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("git %s 失败: %s", strings.Join(args, " "), detail)
	}
	return stdout.String(), nil
}

// removeDirRetry 删除目录，并对 Windows 的「删除待决」竞态做几次重试。
//
// 刚被强杀的进程可能还有句柄没释放完，此时 RemoveAll 会报
// "The directory is not empty"，稍等即能成功。
func removeDirRetry(path string) error {
	var err error
	for i := 0; i < 5; i++ {
		err = os.RemoveAll(path)
		if err == nil {
			return nil
		}
		time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
	}
	return err
}

// acquireLock 锁住「站点目录 + 仓库名」这个组合，防止两个进程同时写。
//
// 用操作系统级文件锁而不是「文件是否存在」判断占用：进程退出（包含被强杀）
// 时由内核释放锁，不会留下需要人工清理的残留，也没有 PID 复用的误判。
// 锁文件放在 .rog 下，它不属于站点内容，不会被发布。
func acquireLock(outRoot, name string) (func(), error) {
	dir := filepath.Join(outRoot, stateDir)
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("%s 已存在但不是目录，请先移除它", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, name+".lock")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("已有另一个操作在写 %s（锁文件 %s）", name, path)
	}
	// 刻意不删锁文件：删除会和另一个进程打开的句柄形成竞态，留着一个空文件无害。
	return func() { _ = f.Close() }, nil
}

// gitIn 在指定的 git 目录里执行命令。
//
// 与 runGit(dir, ...) 的区别：这里用 --git-dir 明确指定，git 不会向上层目录
// 发现父仓库。凡是操作「产物目录」的地方都该用它：产物一旦嵌在别的 git 仓库里，
// 用工作目录方式调用可能读到父仓库的 refs，把无关的 sha 当成自己的。
func gitIn(gitDir string, args ...string) (string, error) {
	full := make([]string, 0, len(args)+2)
	full = append(full, "--git-dir", gitDir)
	full = append(full, args...)
	return runGit("", full...)
}

// ---------- 命名 ----------

// NormalizeName 去掉结尾的 .git（允许 xxx.git.git 一并干掉）。
//
// 循环剥离直到稳定：一次剥离可能留下新的尾随空白或新的后缀
// （"x .git .git" 一次只能剥掉最后一个），不循环就不满足幂等。
func NormalizeName(raw string) string {
	name := strings.TrimSpace(raw)
	for {
		next := strings.TrimSpace(gitSuffixRe.ReplaceAllString(name, ""))
		if next == name {
			return name
		}
		name = next
	}
}

// IsRemote 判断源是远端地址。Windows 路径如 D:\x 不匹配 scp 模式（没有 @）。
func IsRemote(source string) bool {
	return remoteURLRe.MatchString(source) || remoteSCPRe.MatchString(source)
}

// RemoteName 从远端地址取仓库名：https://host/user/repo.git -> repo
func RemoteName(url string) string {
	tail := strings.TrimRight(url, `/\`)
	tail = strings.ReplaceAll(tail, `\`, "/")
	parts := strings.Split(tail, "/")
	tail = parts[len(parts)-1]
	if i := strings.Index(tail, ":"); i >= 0 {
		tail = tail[i+1:]
	}
	return NormalizeName(tail)
}

// windowsReservedNames 是 Windows 不允许作为文件名的保留名。
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// isValidName 要求仓库名在所有目标平台上都能安全地当作目录名。
//
// 宁可在这里前置拒绝，也不要留给文件系统或 git 去报原始错误：后者会给出
// unlinkat / cannot mkdir 这类看不出所以然的信息，报告里已经被点到过。
func isValidName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	// Windows 上非法、其他平台也一并拒绝，保证跨平台行为一致
	if strings.ContainsAny(name, `<>:"|?*`) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	// 目录名就是 name。两个平台的上限都要满足：NTFS 限 255 个 UTF-16
	// 码元，ext4 限 255 字节。字节数更严格（中文一字三字节），按它卡更安全。
	full := name
	if len(utf16.Encode([]rune(full))) > 255 || len(full) > 255 {
		return false
	}
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	return !windowsReservedNames[strings.ToUpper(base)]
}

// ---------- repository.json ----------

type registryEntry struct {
	Name        string
	Description string
}

func updateRegistry(outRoot, name string) error {
	path := filepath.Join(outRoot, "repository.json")
	entries := loadRegistry(path)

	description := ""
	for _, e := range entries {
		if e.Name == name {
			description = e.Description
			break
		}
	}

	kept := make([]registryEntry, 0, len(entries)+1)
	for _, e := range entries {
		if e.Name != name {
			kept = append(kept, e)
		}
	}
	kept = append(kept, registryEntry{Name: name, Description: description})
	sort.Slice(kept, func(i, j int) bool { return kept[i].Name < kept[j].Name })

	return saveRegistry(path, kept)
}

func loadRegistry(path string) []registryEntry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var parsed struct {
		Repositories []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	out := make([]registryEntry, 0, len(parsed.Repositories))
	for _, r := range parsed.Repositories {
		if strings.TrimSpace(r.Name) == "" {
			continue
		}
		out = append(out, registryEntry{Name: r.Name, Description: r.Description})
	}
	return out
}

// saveRegistry 手写 JSON，保证 repositories 始终是数组、缩进稳定、中文不转义。
func saveRegistry(path string, entries []registryEntry) error {
	var b strings.Builder
	b.WriteString("{\n  \"repositories\": [\n")
	for i, e := range entries {
		if i > 0 {
			b.WriteString(",\n")
		}
		fmt.Fprintf(&b, "    { \"name\": %s, \"description\": %s }",
			jsonString(e.Name), jsonString(e.Description))
	}
	b.WriteString("\n  ]\n}\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// jsonString 序列化字符串，但不转义 < > &（便于人工阅读）。
func jsonString(s string) string {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(buf.String(), "\n")
}

// ---------- 小工具 ----------

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
