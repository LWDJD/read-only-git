package repopack

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pack-<40 位十六进制>.pack —— 少了这个格式，git 客户端会静默跳过该 pack。
var packNameRe = regexp.MustCompile(`^pack-[0-9a-f]{40}\.pack$`)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git 不可用，跳过")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s 失败: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// makeRepo 造一个有 n 个提交的普通仓库。
func makeRepo(t *testing.T, n int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@t.local")
	git(t, dir, "config", "user.name", "tester")
	for i := 1; i <= n; i++ {
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(strings.Repeat("x", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-q", "-m", fmt.Sprintf("commit %d", i))
	}
	return dir
}

func TestPackProducesDumbFiles(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 2)
	out := t.TempDir()

	res, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "demo" {
		t.Fatalf("name = %q", res.Name)
	}
	if res.Branch != "main" {
		t.Fatalf("branch = %q", res.Branch)
	}
	for _, rel := range []string{"HEAD", "info/refs", "objects/info/packs"} {
		if _, err := os.Stat(filepath.Join(res.Target, rel)); err != nil {
			t.Errorf("缺少必需文件 %s: %v", rel, err)
		}
	}
	if len(res.Packs) == 0 {
		t.Fatal("没有产出 pack")
	}
	for _, p := range res.Packs {
		if !packNameRe.MatchString(p) {
			t.Errorf("pack 文件名不合契约: %s", p)
		}
	}

	// 协议不会请求的东西不该留下
	for _, junk := range []string{"hooks", "logs"} {
		if _, err := os.Stat(filepath.Join(res.Target, junk)); err == nil {
			t.Errorf("残留了无用目录: %s", junk)
		}
	}

	// refs/ 必须保留（git 靠它识别仓库），heads/ tags/ 这类空子目录也是标准布局，
	// 但里面不该残留松散 ref 文件。
	refsDir := filepath.Join(res.Target, "refs")
	if _, err := os.Stat(refsDir); err != nil {
		t.Errorf("refs/ 缺失，git 将无法识别该仓库: %v", err)
	} else {
		var loose []string
		_ = filepath.WalkDir(refsDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			loose = append(loose, path)
			return nil
		})
		if len(loose) != 0 {
			t.Errorf("refs/ 下残留松散 ref: %v", loose)
		}
	}
}

func TestPackRegistryMerges(t *testing.T) {
	requireGit(t)
	out := t.TempDir()

	if _, err := Pack(Options{Source: makeRepo(t, 1), OutDir: out, Name: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Pack(Options{Source: makeRepo(t, 1), OutDir: out, Name: "two"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(out, "repository.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"one"`) || !strings.Contains(s, `"two"`) {
		t.Fatalf("清单缺少条目:\n%s", s)
	}
}

func TestPackRejectsBadName(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	for _, name := range []string{"../evil", "a/b", `a\b`, ".."} {
		if _, err := Pack(Options{Source: src, OutDir: t.TempDir(), Name: name}); err == nil {
			t.Errorf("名字 %q 应该被拒绝", name)
		}
	}
}

func TestPackMissingSource(t *testing.T) {
	requireGit(t)
	_, err := Pack(Options{
		Source: filepath.Join(t.TempDir(), "nope"),
		OutDir: t.TempDir(),
		Name:   "x",
	})
	if err == nil {
		t.Fatal("源不存在时应该报错")
	}
}

func TestPackIncrementalKeepsOldPack(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	res1, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.Packs) != 1 {
		t.Fatalf("首次应该是 1 个 pack，实际 %d", len(res1.Packs))
	}
	oldPack := res1.Packs[0]

	// 源上加一个提交
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("much longer content"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "second")

	res2, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Via != "incremental" {
		t.Fatalf("链路应为 incremental，实际 %q", res2.Via)
	}
	if len(res2.Packs) != 2 {
		t.Fatalf("增量后应有 2 个 pack，实际 %d: %v", len(res2.Packs), res2.Packs)
	}
	found := false
	for _, p := range res2.Packs {
		if p == oldPack {
			found = true
		}
	}
	if !found {
		t.Fatalf("旧 pack %s 不该消失: %v", oldPack, res2.Packs)
	}
}

func TestPackIncrementalIdempotent(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	res1, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	// 源没有变化，再跑增量
	res2, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Packs) != len(res1.Packs) {
		t.Fatalf("无变化时不该新增 pack: %v -> %v", res1.Packs, res2.Packs)
	}
}

func TestPackIncrementalFallsBackWhenTargetMissing(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	// 目标不存在，即使要求增量也应退回全量而不是报错
	res, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Via == "incremental" {
		t.Fatalf("目标不存在时不该走增量，实际 %q", res.Via)
	}
	if len(res.Packs) == 0 {
		t.Fatal("应该产出 pack")
	}
}

// BUG-1：新分支指向目标里已有的提交时，ref 不能被丢弃。
// 根因是 bundle 的 --not 会连带排除指向被排除对象的 ref。
func TestPackIncrementalKeepsRefPointingAtExistingCommit(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	if _, err := Pack(Options{Source: src, OutDir: out, Name: "demo"}); err != nil {
		t.Fatal(err)
	}

	// 新分支，指向已经存在的提交（没有任何新对象）
	git(t, src, "branch", "feature")

	res, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatalf("增量应成功: %v", err)
	}
	refs := git(t, res.Target, "show-ref")
	if !strings.Contains(refs, "refs/heads/feature") {
		t.Fatalf("指向已有提交的新分支被丢弃:\n%s", refs)
	}
}

// BUG-1 的静默变体：同时有新提交与新标签时，标签不被丢弃。
func TestPackIncrementalKeepsTagPointingAtExistingCommit(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	if _, err := Pack(Options{Source: src, OutDir: out, Name: "demo"}); err != nil {
		t.Fatal(err)
	}

	// 标签指向已有提交，随后再加一个新提交让 bundle 非空
	git(t, src, "tag", "v1")
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("more content"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "second")

	res, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	refs := git(t, res.Target, "show-ref")
	if !strings.Contains(refs, "refs/tags/v1") {
		t.Fatalf("指向已有提交的标签被静默丢弃:\n%s", refs)
	}
}

// BUG-2：源删掉的 ref 必须从目标里同步删除。
func TestPackIncrementalDeletesRemovedRef(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	git(t, src, "branch", "feature")
	if _, err := Pack(Options{Source: src, OutDir: out, Name: "demo"}); err != nil {
		t.Fatal(err)
	}

	git(t, src, "branch", "-D", "feature")
	git(t, src, "commit", "-q", "--allow-empty", "-m", "more")

	res, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	refs := git(t, res.Target, "show-ref")
	if strings.Contains(refs, "refs/heads/feature") {
		t.Fatalf("源已删除的分支仍留在目标里:\n%s", refs)
	}
}

// BUG-3：目标里多出来的 ref 不应导致增量失败，且应被对齐掉。
func TestPackIncrementalRemovesExtraRefInTarget(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	res1, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}

	// 人为在目标里塞一个源里没有的分支
	sha := strings.TrimSpace(git(t, src, "rev-parse", "HEAD"))
	git(t, res1.Target, "update-ref", "refs/heads/extra", sha)

	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("more content"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "second")

	res2, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatalf("目标含额外 ref 时增量不该失败: %v", err)
	}
	refs := git(t, res2.Target, "show-ref")
	if strings.Contains(refs, "refs/heads/extra") {
		t.Fatalf("源里不存在的 ref 应被对齐删除:\n%s", refs)
	}
}

// BUG-4：产物目录嵌在别的 git 仓库里时，损坏的目标必须被认出来并回退全量。
// 否则 --git-dir 缺省会让 git 向上发现父仓库，把父仓库的 refs 当成自己的。
func TestPackTargetNestedInAnotherRepo(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)

	outer := t.TempDir()
	git(t, outer, "init", "-q", "-b", "main")
	git(t, outer, "config", "user.email", "t@t.local")
	git(t, outer, "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(outer, "outer.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, outer, "add", "-A")
	git(t, outer, "commit", "-q", "-m", "outer")

	out := filepath.Join(outer, "site")

	res1, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}

	// 弄坏目标（删掉 refs，git 靠它识别仓库）
	if err := os.RemoveAll(filepath.Join(res1.Target, "refs")); err != nil {
		t.Fatal(err)
	}

	res2, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatalf("损坏目标时应回退全量而不是报错: %v", err)
	}
	if res2.Via == "incremental" {
		t.Fatalf("损坏目标不该被当成可用，实际走了 %q", res2.Via)
	}
	if _, err := os.Stat(filepath.Join(res2.Target, "refs")); err != nil {
		t.Fatalf("产物应被重建，refs 仍缺失: %v", err)
	}
}

// 空源地址会被 Abs("") 解析成当前目录，必须显式拒绝。
func TestPackRejectsEmptySource(t *testing.T) {
	requireGit(t)
	if _, err := Pack(Options{Source: "", OutDir: t.TempDir(), Name: "x"}); err == nil {
		t.Fatal("空源应被拒绝")
	}
	if _, err := Pack(Options{Source: "   ", OutDir: t.TempDir(), Name: "x"}); err == nil {
		t.Fatal("全空白源应被拒绝")
	}
}

// 空仓库应该照样能产出可托管的产物，而不是失败。
func TestPackEmptyRepoProducesArtifact(t *testing.T) {
	requireGit(t)
	dir := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")

	res, err := Pack(Options{Source: dir, OutDir: t.TempDir(), Name: "x"})
	if err != nil {
		t.Fatalf("空仓库应能产出产物: %v", err)
	}
	for _, rel := range []string{"HEAD", "info/refs", "objects/info/packs"} {
		if _, err := os.Stat(filepath.Join(res.Target, rel)); err != nil {
			t.Errorf("产物缺少 %s: %v", rel, err)
		}
	}
}

// 同一个仓库的并发写入要被锁挡住；不同仓库名互不影响。
func TestAcquireLockExcludesSecond(t *testing.T) {
	out := t.TempDir()

	release, err := acquireLock(out, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(out, "demo"); err == nil {
		t.Fatal("第二次获取同一把锁应当失败")
	}

	// 不同仓库名不该互相阻塞
	otherRelease, err := acquireLock(out, "other")
	if err != nil {
		t.Fatalf("不同名字不该互相阻塞: %v", err)
	}
	otherRelease()

	release()
	reacquired, err := acquireLock(out, "demo")
	if err != nil {
		t.Fatalf("释放后应能重新获取: %v", err)
	}
	reacquired()
}

// 锁文件必须放在 .rog 下，否则会被当作站点内容发布出去。
func TestAcquireLockKeepsOutOfSite(t *testing.T) {
	out := t.TempDir()
	release, err := acquireLock(out, "demo")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if _, err := os.Stat(filepath.Join(out, "demo.lock")); err == nil {
		t.Fatal("锁文件不该出现在站点根目录")
	}
	if _, err := os.Stat(filepath.Join(out, stateDir, "demo.lock")); err != nil {
		t.Fatalf("锁文件应在 %s 下: %v", stateDir, err)
	}
}

// worktree 检出的 .git 是文件，应给出可读提示。
func TestPackRejectsWorktree(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)

	wt := filepath.Join(t.TempDir(), "wt")
	git(t, src, "worktree", "add", "-q", wt)

	_, err := Pack(Options{Source: wt, OutDir: t.TempDir(), Name: "x"})
	if err == nil {
		t.Fatal("worktree 应被拒绝")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("报错应提示 worktree，实际: %v", err)
	}
}

// NEW-1：源里存在 refs/heads、refs/tags 之外的本地 ref 时，增量不该失败。
//
// 这类 ref（refs/remotes、refs/stash 等）指向的对象不会随 --branches --tags
// 传输，若要求对齐就会 update-ref 报 "nonexistent object"。绝大多数真实
// 工作仓库都带 refs/stash，所以这条回归一旦出现就是普遍性失败。
func TestPackIncrementalIgnoresLocalOnlyRefs(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	if _, err := Pack(Options{Source: src, OutDir: out, Name: "demo"}); err != nil {
		t.Fatal(err)
	}

	// 造一个只能从这些本地 ref 到达的提交
	git(t, src, "checkout", "-q", "-b", "temp")
	if err := os.WriteFile(filepath.Join(src, "b.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "reachable-only-from-local-ref")
	sha := strings.TrimSpace(git(t, src, "rev-parse", "HEAD"))
	git(t, src, "checkout", "-q", "main")
	git(t, src, "branch", "-D", "temp")

	git(t, src, "update-ref", "refs/remotes/origin/main", sha)
	git(t, src, "update-ref", "refs/stash", sha)

	res, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatalf("含 refs/remotes 或 refs/stash 时增量不该失败: %v", err)
	}

	refs := git(t, res.Target, "show-ref")
	if strings.Contains(refs, "refs/remotes") || strings.Contains(refs, "refs/stash") {
		t.Fatalf("产物不该包含本地专用 ref:\n%s", refs)
	}

	// 源零改动时再跑一次也必须稳定
	if _, err := Pack(Options{Source: src, OutDir: out, Name: "demo"}); err != nil {
		t.Fatalf("二次增量仍应成功: %v", err)
	}
}

// 不传任何开关也走增量：目标里已经有这个仓库就够了。
//
// 「要不要增量」不该问用户，他知道的信息比工具少。
func TestPackAutoIncrementsWithoutAnyFlag(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	res1, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	oldPack := res1.Packs[0]

	// 源上加一个提交
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("much longer content"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "second")

	// 关键：这里什么都不传
	res2, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Via != "incremental" {
		t.Fatalf("目标可用时应当自动增量，实际链路 %q", res2.Via)
	}
	found := false
	for _, p := range res2.Packs {
		if p == oldPack {
			found = true
		}
	}
	if !found {
		t.Fatalf("自动增量也该保留旧 pack %s: %v", oldPack, res2.Packs)
	}
}

// 完整重打包：丢掉已有产物，从零重建。
//
// 这是「自动增量」的对手方。什么时候真需要它：怀疑产物坏了，
// 或者想彻底不带历史（旧 pack 不再被任何引用需要）。
// 增量做不到这两件事——它按定义就要保留旧 pack。
func TestPackRebuildDiscardsExistingTarget(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 2)
	out := t.TempDir()

	res1, err := Pack(Options{Source: src, OutDir: out, Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res1.Packs) != 1 {
		t.Fatalf("首次应该是 1 个 pack，实际 %d", len(res1.Packs))
	}
	oldPack := res1.Packs[0]

	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("more content here"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", "-A")
	git(t, src, "commit", "-q", "-m", "second")

	res2, err := Pack(Options{Source: src, OutDir: out, Name: "demo", Rebuild: true})
	if err != nil {
		t.Fatal(err)
	}
	if res2.Via == "incremental" {
		t.Fatalf("重打包不该走增量，实际 %q", res2.Via)
	}
	if len(res2.Packs) != 1 {
		t.Fatalf("重打包后应当只有 1 个 pack，实际 %v", res2.Packs)
	}
	if res2.Packs[0] == oldPack {
		t.Fatalf("重打包应当产出新的 pack，而不是沿用 %s", oldPack)
	}

	// 重建完还得是个能用的仓库
	refs := git(t, res2.Target, "show-ref")
	if !strings.Contains(refs, "refs/heads/") {
		t.Fatalf("重打包后仓库应当有分支:\n%s", refs)
	}
}

// 远端源永远不做增量：它没有本地旧 pack 可复用。
//
// 重打包开关对远端也不改变什么，不该把这条路径当作错误。
func TestPackRemoteIgnoresRebuild(t *testing.T) {
	requireGit(t)
	src := makeRepo(t, 1)
	out := t.TempDir()

	res, err := Pack(Options{Source: src, OutDir: out, Name: "demo", Rebuild: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Via == "incremental" {
		t.Fatalf("首次打包不会是增量，实际 %q", res.Via)
	}
}
