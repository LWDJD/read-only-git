package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/LWDJD/read-only-git/internal/arweave"
	"github.com/LWDJD/read-only-git/internal/publish"
	"github.com/LWDJD/read-only-git/internal/repopack"
	"github.com/LWDJD/read-only-git/internal/sitekit"
)

// ---------- 工具 ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr 把错误原样透给前端。
//
// 尤其是节点或上传服务返回的原话，包装一层就丢了排错线索。
func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, fmt.Errorf("这个接口只接受 POST"))
		return false
	}
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("请求体不是合法 JSON: %w", err))
		return false
	}
	return true
}

// safeJoin 把相对路径拼到站点根下，并挡住越界写法。
//
// 界面能改文件，就必须挡住 ../ 这类路径，否则一个手滑的请求就能写到站点外面。
func safeJoin(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	// 先看原始写法：以分隔符开头的，在哪个平台都当作绝对路径拒绝。
	// 不能只靠 filepath.IsAbs，它在 Windows 上不认 "/etc/passwd" 这种写法，
	// 会让同一份界面在两个平台上一个放行、一个拦住。
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, `\`) {
		return "", fmt.Errorf("路径越界: %q", rel)
	}

	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("路径越界: %q", rel)
	}

	full := filepath.Join(root, clean)
	back, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(back, "..") {
		return "", fmt.Errorf("路径越界: %q", rel)
	}
	return full, nil
}

func siteOf(s *Server, given string) string {
	if strings.TrimSpace(given) != "" {
		return given
	}
	return s.siteDir
}

// ---------- 状态 ----------

type fileState struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type repoState struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type recordState struct {
	File    string `json:"file"`
	Target  string `json:"target"`
	Root    string `json:"root"`
	Count   int    `json:"count"`
	Updated string `json:"updated"`
}

type templateState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Files       int    `json:"files"`
}

type scaffoldState struct {
	Templates []templateState `json:"templates"`
	// Missing 是站点里还缺多少个骨架文件。
	// 大于零说明这个目录还不算一个能打开的站点，界面会提示布一下。
	Missing int `json:"missing"`
	Total   int `json:"total"`
}

type stateResponse struct {
	Site      string          `json:"site"`
	Exists    bool            `json:"exists"`
	Files     []fileState     `json:"files"`
	TotalSize int64           `json:"totalSize"`
	Repos     []repoState     `json:"repos"`
	Records   []recordState   `json:"records"`
	Scaffold  scaffoldState   `json:"scaffold"`
	Error     string          `json:"error,omitempty"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	site := siteOf(s, r.URL.Query().Get("site"))
	writeJSON(w, s.buildState(site))
}

// buildState 每次都重新扫描并逐文件重算摘要。
//
// 这里绝不能换成缓存：管理员可能绕过界面直接改目录，
// 一旦缓存，界面就会显示与磁盘不符的内容。
func (s *Server) buildState(site string) stateResponse {
	out := stateResponse{Site: site}

	// 站点目录不存在不算错误：新建站点时它就是空的。
	// 把骨架缺多少一并算出来，界面才知道该不该提示布一下。
	info, err := os.Stat(site)
	if err != nil || !info.IsDir() {
		out.Scaffold = buildScaffold(site)
		out.Error = fmt.Sprintf("站点目录还不存在: %s", site)
		return out
	}
	out.Exists = true

	scanned, err := publish.Scan(site)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	for _, f := range scanned.Files {
		out.Files = append(out.Files, fileState{Path: f.Path, Size: f.Size, Digest: f.Digest})
	}
	out.TotalSize = scanned.TotalSize()

	out.Repos = readRegistry(site)
	out.Records = readRecords(site)
	out.Scaffold = buildScaffold(site)
	return out
}

// buildScaffold 汇总模板信息与目标目录里还缺多少骨架文件。
//
// 目录不存在时，所有文件都算缺：这正是「还没有站点」的样子。
func buildScaffold(site string) scaffoldState {
	var out scaffoldState
	for _, tpl := range sitekit.Templates() {
		out.Templates = append(out.Templates, templateState{
			ID:          tpl.ID,
			Name:        tpl.Name,
			Description: tpl.Description,
			Files:       tpl.Files,
		})
	}
	out.Total = len(templateFiles("default"))

	missing, err := sitekit.Missing("default", site)
	if err != nil {
		// 取不到清单就当作「不知道」，不拿它去吓用户
		out.Missing = 0
		return out
	}
	out.Missing = len(missing)
	return out
}

func templateFiles(id string) []string {
	files, err := sitekit.Files(id)
	if err != nil {
		return nil
	}
	return files
}

func readRegistry(site string) []repoState {
	data, err := os.ReadFile(filepath.Join(site, "repository.json"))
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
	out := make([]repoState, 0, len(parsed.Repositories))
	for _, r := range parsed.Repositories {
		out = append(out, repoState{Name: r.Name, Description: r.Description})
	}
	return out
}

// readRecords 汇总 .rog 下的发布记录，只取给人看的摘要。
func readRecords(site string) []recordState {
	dir := filepath.Join(site, publish.StateDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var out []recordState
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "publish-") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rec, err := publish.LoadRecord(filepath.Join(dir, e.Name()))
		if err != nil || rec == nil {
			out = append(out, recordState{File: e.Name(), Target: "(记录损坏)"})
			continue
		}
		updated := ""
		if !rec.At.IsZero() {
			updated = rec.At.Format("2006-01-02 15:04")
		}
		out = append(out, recordState{
			File:    e.Name(),
			Target:  rec.Target,
			Root:    rec.Root,
			Count:   len(rec.Refs),
			Updated: updated,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

// ---------- 打包 ----------

type packRequest struct {
	Source      string `json:"source"`
	OutDir      string `json:"outDir"`
	Name        string `json:"name"`
	Incremental bool   `json:"incremental"`
}

func (s *Server) handlePack(w http.ResponseWriter, r *http.Request) {
	var req packRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Source) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("源仓库不能为空"))
		return
	}

	id := s.tasks.Run("pack", func(t *Task) {
		res, err := repopack.Pack(repopack.Options{
			Source:      req.Source,
			OutDir:      req.OutDir,
			Name:        req.Name,
			Incremental: req.Incremental,
			Logf:        t.Logf,
		})
		if err != nil {
			t.fail(err)
			return
		}
		t.succeed(map[string]any{
			"name":   res.Name,
			"branch": res.Branch,
			"via":    res.Via,
			"files":  len(res.Files),
			"size":   res.TotalSize,
			"packs":  len(res.Packs),
		})
	})
	writeJSON(w, map[string]string{"taskId": id})
}

// ---------- 发布 ----------

// repoNameFor 从站点目录名推出仓库名。
//
// 界面上不再提供这一栏。一个站点对应一个仓库，站点目录名就是它的名字：
// 让它可填只会多一个能填错的地方，而填错的代价是产物里的 Repo 标签
// 与发布记录的身份一起错，这两样都不该由人在界面上临时决定。
//
// CLI 那边仍可用 --repo 显式覆盖，那是脚本场景，不是随手填。
func repoNameFor(siteRoot string) string {
	return filepath.Base(filepath.Clean(siteRoot))
}

type publishRequest struct {
	Site     string `json:"site"`
	Target   string `json:"target"` // local / turbo / l1
	Dest     string `json:"dest"`   // local 用
	Endpoint string `json:"endpoint"`
	Node     string `json:"node"`
	From     string `json:"from"`
	Gateway  string `json:"gateway"`
	// ProxyMode 是网络出口：system（默认，跟随系统设置）/ manual / off。
	ProxyMode string `json:"proxyMode"`
	// ProxyURL 只在 manual 模式下用到。
	ProxyURL string `json:"proxyUrl"`
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Site = siteOf(s, req.Site)
	if strings.TrimSpace(req.Site) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("站点目录不能为空"))
		return
	}

	id := s.tasks.Run("publish", func(t *Task) {
		if err := s.doPublish(t, req); err != nil {
			t.fail(err)
			return
		}
	})
	writeJSON(w, map[string]string{"taskId": id})
}

func (s *Server) doPublish(t *Task, req publishRequest) error {
	site, err := publish.Scan(req.Site)
	if err != nil {
		return err
	}
	if len(site.Files) == 0 {
		return fmt.Errorf("%s 里没有可发布的文件", req.Site)
	}

	// 同一站点同一目标同时只允许一条发布在跑。
	// 连点两下按钮就会撞到这里，与其两条发布互踩同一份记录，
	// 不如直接把后一条拒掉，并告诉她原因。
	release, err := publish.Acquire(site.Root, req.Target)
	if err != nil {
		return err
	}
	defer release()

	t.Logf("站点 %s：%d 个文件", site.Root, len(site.Files))

	switch req.Target {
	case "local":
		return s.publishLocal(t, site, req)
	case "turbo":
		return s.publishArweave(t, site, req, false)
	case "l1":
		return s.publishArweave(t, site, req, true)
	default:
		return fmt.Errorf("未知发布目标: %q（可选 local / turbo / l1）", req.Target)
	}
}

func (s *Server) publishLocal(t *Task, site *publish.Site, req publishRequest) error {
	if strings.TrimSpace(req.Dest) == "" {
		return fmt.Errorf("本地发布需要填目标目录")
	}

	target := &publish.Local{Dir: req.Dest, Logf: t.Logf}
	statePath := publish.StatePath(site.Root, target.Name(), req.Dest)

	prev, err := publish.LoadRecord(statePath)
	if err != nil {
		t.Logf("! %v（按首次发布处理）", err)
		prev = nil
	}

	rec, err := target.Publish(context.Background(), site, prev)
	if rec != nil {
		if saveErr := publish.SaveRecord(statePath, rec); saveErr != nil {
			t.Logf("! 记录保存失败: %v", saveErr)
		}
	}
	if err != nil {
		return err
	}

	t.Logf("入口 %s", rec.Root)
	t.succeed(map[string]any{"root": rec.Root, "statePath": statePath})
	return nil
}

func (s *Server) publishArweave(t *Task, site *publish.Site, req publishRequest, useL1 bool) error {
	repo := repoNameFor(site.Root)

	// 一个 client 贯穿整轮发布：报交易、逐块 /chunk、取记录都走它。
	// 分开造的话，代理设置很容易只对其中几步生效。
	mode, err := arweave.ParseProxyMode(req.ProxyMode)
	if err != nil {
		return err
	}
	if mode == arweave.ProxyManual && strings.TrimSpace(req.ProxyURL) == "" {
		return fmt.Errorf("手动代理模式需要填代理地址")
	}
	client, err := arweave.NewClient(arweave.ProxyConfig{Mode: mode, URL: req.ProxyURL}, 0)
	if err != nil {
		return err
	}

	// 签名通道就是挂在 webui /sign/ 下的那一个。
	//
	// 不再另起服务、不再另开页面：用户就在当前页面里确认钱包。
	// 同时也不再往日志里打一个「签名页 <地址>」——那个地址现在不存在了。
	svc := s.sign

	statePath := publish.StatePath(site.Root, "arweave", repo)
	prev, err := publish.LoadRecord(statePath)
	if err != nil {
		t.Logf("! %v（按首次发布处理）", err)
		prev = nil
	}

	if req.From != "" {
		fetched, ferr := arweave.FetchRecordWithClient(context.Background(), req.Gateway, req.From,
			publish.RecordRelPath("arweave", repo), client)
		if ferr != nil {
			return ferr
		}
		if err := publish.SaveRecord(statePath, fetched); err != nil {
			return err
		}
		t.Logf("已从链上取回发布记录，含 %d 个文件引用", len(fetched.Refs))
		prev = fetched
	}

	target := &arweave.Target{
		Repo:       repo,
		Signer:     svc,
		RecordPath: publish.RecordRelPath("arweave", repo),
		Client:     client,
		Logf:       t.Logf,
	}
	if useL1 {
		target.TxSigner = svc
		target.Node = req.Node
	} else {
		target.Uploader = arweave.NewUploaderWithClient(req.Endpoint, client)
	}

	// 给整轮等签名加个上限：用户关掉页面时不该把进程永久挂住
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	t.Logf("等待钱包确认（在页面右上角连接钱包后会自动逐个弹出）")

	rec, err := target.Publish(ctx, site, prev)
	if rec != nil {
		if saveErr := publish.SaveRecord(statePath, rec); saveErr != nil {
			t.Logf("! 记录保存失败: %v", saveErr)
		}
	}
	if err != nil {
		return err
	}

	t.Logf("入口 %s", rec.Root)
	t.Logf("网关预览 https://arweave.net/%s", rec.Root)
	t.succeed(map[string]any{
		"root":      rec.Root,
		"statePath": statePath,
		"repo":      repo,
		"preview":   "https://arweave.net/" + rec.Root,
	})
	return nil
}

// ---------- 站点骨架 ----------

type siteInitRequest struct {
	Site      string `json:"site"`
	Template  string `json:"template"`
	Overwrite bool   `json:"overwrite"`
}

// handleSiteInit 把内嵌的前端模板铺到站点目录。
//
// 有了它，一个 exe 就能从零把站点立起来：不必另行准备前端文件。
func (s *Server) handleSiteInit(w http.ResponseWriter, r *http.Request) {
	var req siteInitRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Site = siteOf(s, req.Site)
	if strings.TrimSpace(req.Site) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("站点目录不能为空"))
		return
	}
	if req.Template == "" {
		req.Template = "default"
	}

	id := s.tasks.Run("site-init", func(t *Task) {
		abs, err := filepath.Abs(req.Site)
		if err != nil {
			t.fail(err)
			return
		}

		written, err := sitekit.Materialize(req.Template, abs, req.Overwrite)
		if err != nil {
			t.fail(err)
			return
		}

		if len(written) == 0 {
			t.Logf("骨架已经齐了，没有改动")
		} else {
			t.Logf("写入 %d 个文件到 %s", len(written), abs)
			for _, rel := range written {
				t.Logf("  %s", rel)
			}
		}

		missing, err := sitekit.Missing(req.Template, abs)
		if err != nil {
			t.fail(err)
			return
		}
		if len(missing) > 0 {
			t.Logf("还缺 %d 个文件（勾选覆盖可补回来）", len(missing))
		}

		t.succeed(map[string]any{"written": len(written), "site": abs})
	})
	writeJSON(w, map[string]string{"taskId": id})
}

// ---------- 从链上恢复 ----------

type restoreRequest struct {
	Site    string `json:"site"`
	Repo    string `json:"repo"`
	Entry   string `json:"entry"`
	Gateway string `json:"gateway"`
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var req restoreRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.Site = siteOf(s, req.Site)
	if strings.TrimSpace(req.Entry) == "" {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("入口 id 不能为空"))
		return
	}

	id := s.tasks.Run("restore", func(t *Task) {
		site, err := publish.Scan(req.Site)
		if err != nil {
			t.fail(err)
			return
		}
		repo := req.Repo
		if strings.TrimSpace(repo) == "" {
			repo = filepath.Base(site.Root)
		}

		rec, err := arweave.FetchRecord(context.Background(), req.Gateway, req.Entry,
			publish.RecordRelPath("arweave", repo))
		if err != nil {
			t.fail(err)
			return
		}

		statePath := publish.StatePath(site.Root, "arweave", repo)
		if err := publish.SaveRecord(statePath, rec); err != nil {
			t.fail(err)
			return
		}

		t.Logf("取回 %d 个文件引用，写入 %s", len(rec.Refs), statePath)
		t.succeed(map[string]any{"statePath": statePath, "count": len(rec.Refs)})
	})
	writeJSON(w, map[string]string{"taskId": id})
}

// ---------- 文件 ----------

type replaceFile struct {
	Path  string `json:"path"`
	Bytes []byte `json:"bytes"` // JSON 里是 base64
}

type replaceRequest struct {
	Site  string        `json:"site"`
	Files []replaceFile `json:"files"`
}

type replaceResult struct {
	Path  string `json:"path"`
	Size  int    `json:"size,omitempty"`
	Error string `json:"error,omitempty"`
}

// handleFileReplace 批量写入文件。
//
// 界面一次可能拖进来多个文件与整个目录，逐个发请求既慢又要处理半途失败，
// 所以一次收全。「重名该覆盖还是跳过」在界面侧已经问过用户了，这里只管写。
//
// 一个文件写失败不影响其余：每个都单独报告结果，前端照实显示。
func (s *Server) handleFileReplace(w http.ResponseWriter, r *http.Request) {
	var req replaceRequest
	if !decodeBody(w, r, &req) {
		return
	}
	site := siteOf(s, req.Site)
	if len(req.Files) == 0 {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("没有要写入的文件"))
		return
	}

	results := make([]replaceResult, 0, len(req.Files))
	for _, f := range req.Files {
		if err := s.writeSiteFile(site, f.Path, f.Bytes); err != nil {
			results = append(results, replaceResult{Path: f.Path, Error: err.Error()})
			continue
		}
		results = append(results, replaceResult{Path: f.Path, Size: len(f.Bytes)})
	}

	writeJSON(w, map[string]any{"ok": true, "results": results})
}

// writeSiteFile 写一个站点内的文件，路径越界一律拒绝。
func (s *Server) writeSiteFile(site, rel string, data []byte) error {
	full, err := safeJoin(site, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

type deleteRequest struct {
	Site string `json:"site"`
	Path string `json:"path"`
}

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	var req deleteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	site := siteOf(s, req.Site)

	full, err := safeJoin(site, req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := os.Remove(full); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": req.Path})
}
