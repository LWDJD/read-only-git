package webui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LWDJD/read-only-git/internal/arweave"
	"github.com/LWDJD/read-only-git/internal/publish"
)

// testToken 是当前测试服务的访问 token。
//
// 测试串行执行，每个用例起自己的服务，这个包级量够用；
// 请求辅助函数自己去拿它，不必让每个调用点都传一遍。
var testToken string

func newTestServer(t *testing.T, site string) *Server {
	t.Helper()
	// 端口传 0：测试之间互不干扰，由系统挑空闲的
	srv := New(site, 0)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	testToken = srv.Token()
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

// 指定端口时应当固定监听它，方便反复访问同一个地址。
func TestServerHonorsFixedPort(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 先占一个端口拿到号再放掉：这样既知道一个可用端口，
	// 又不至于与别的进程撞车。
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	srv := New(site, port)
	if err := srv.Start(); err != nil {
		t.Fatalf("固定端口启动失败: %v", err)
	}
	defer srv.Close()

	if !strings.HasSuffix(srv.baseURL(), ":"+strconv.Itoa(port)+"/") {
		t.Fatalf("baseURL 应当用指定端口，实际 %s", srv.baseURL())
	}
	// 给用户打开的地址要带上 token
	if !strings.Contains(srv.URL(), "?token="+srv.Token()) {
		t.Fatalf("URL 应当带上 token，实际 %s", srv.URL())
	}
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(into); err != nil {
		t.Fatal(err)
	}
}

func postJSON(t *testing.T, url string, body any) (int, map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// 界面能改文件，就必须挡住越界路径，否则一个手滑的请求就能写到站点外面。
func TestSafeJoinBlocksEscapes(t *testing.T) {
	root := t.TempDir()

	bad := []string{"../x", `..\x`, "/etc/passwd", "a/../../b", "", ".", ".."}
	for _, rel := range bad {
		if _, err := safeJoin(root, rel); err == nil {
			t.Fatalf("%q 应当被拦住", rel)
		}
	}

	good := []string{"index.html", "src/app.js", "a/b/c.txt"}
	for _, rel := range good {
		if _, err := safeJoin(root, rel); err != nil {
			t.Fatalf("%q 应当放行: %v", rel, err)
		}
	}
}

// 状态接口要如实反映磁盘，含每个文件的摘要。
func TestStateListsFilesWithDigest(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(site, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site, "src", "app.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, site)

	var st stateResponse
	getJSON(t, srv.baseURL()+"api/state", &st)

	if !st.Exists {
		t.Fatal("站点应当被认作存在")
	}
	if len(st.Files) != 2 {
		t.Fatalf("应当列出 2 个文件，实际 %d", len(st.Files))
	}
	for _, f := range st.Files {
		if len(f.Digest) != 64 {
			t.Fatalf("摘要应当是 64 位十六进制: %+v", f)
		}
	}
}

// .rog 是工具状态，不该混进站点文件清单；记录摘要另外列出。
func TestStateSkipsStateDir(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(site, ".rog"), 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"target":"arweave","root":"entry","files":{"a.txt":"d"},"refs":{"a.txt":"id"}}`
	if err := os.WriteFile(filepath.Join(site, ".rog", "publish-arweave-1.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, site)

	var st stateResponse
	getJSON(t, srv.baseURL()+"api/state", &st)

	for _, f := range st.Files {
		if strings.HasPrefix(f.Path, ".rog") {
			t.Fatalf(".rog 不该进文件清单: %+v", st.Files)
		}
	}
	if len(st.Records) != 1 {
		t.Fatalf("记录摘要应当单独列出，实际 %+v", st.Records)
	}
	if st.Records[0].Count != 1 || st.Records[0].Root != "entry" {
		t.Fatalf("记录摘要内容不对: %+v", st.Records[0])
	}
}

// 替换之后状态立刻反映新摘要，证明没有缓存。
func TestReplaceThenStateShowsNewDigest(t *testing.T) {
	site := t.TempDir()
	target := filepath.Join(site, "a.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, site)

	var before stateResponse
	getJSON(t, srv.baseURL()+"api/state", &before)

	code, _ := postJSON(t, srv.baseURL()+"api/files/replace", map[string]any{
		"site": site,
		"files": []map[string]any{
			{"path": "a.txt", "bytes": b64("brand new content")},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("替换应当成功，实际 %d", code)
	}

	var after stateResponse
	getJSON(t, srv.baseURL()+"api/state", &after)

	if before.Files[0].Digest == after.Files[0].Digest {
		t.Fatal("替换后摘要应当变化，说明状态确实重新扫过")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "brand new content" {
		t.Fatalf("文件内容不对: %q", got)
	}
}

// 一次请求写多个文件：界面拖入一批文件或整个目录时走这条。
//
// 顺带确认目录会被按需建出来：拖进来的目录结构不必先在磁盘上存在。
func TestReplaceWritesManyFilesAndMakesDirs(t *testing.T) {
	site := t.TempDir()
	srv := newTestServer(t, site)

	code, out := postJSON(t, srv.baseURL()+"api/files/replace", map[string]any{
		"site": site,
		"files": []map[string]any{
			{"path": "one.txt", "bytes": b64("1")},
			{"path": "deep/two.txt", "bytes": b64("22")},
			{"path": "deep/deeper/three.txt", "bytes": b64("333")},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("批量写入应当成功，实际 %d %+v", code, out)
	}

	for rel, want := range map[string]string{
		"one.txt":               "1",
		"deep/two.txt":          "22",
		"deep/deeper/three.txt": "333",
	} {
		got, err := os.ReadFile(filepath.Join(site, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s 没被写出来: %v", rel, err)
		}
		if string(got) != want {
			t.Fatalf("%s 内容不对: %q", rel, got)
		}
	}
}

// 越界的路径不写，但同一批里其余文件照写。
//
// 批量接口逐个报告结果，不能因为一条坏请求把整批丢掉：
// 拖进来二十个文件，其中一个名字越界，其余十九个不该跟着白干。
func TestReplaceRejectsEscapeButWritesRest(t *testing.T) {
	site := t.TempDir()
	srv := newTestServer(t, site)

	code, out := postJSON(t, srv.baseURL()+"api/files/replace", map[string]any{
		"site": site,
		"files": []map[string]any{
			{"path": "../evil.txt", "bytes": b64("x")},
			{"path": "good.txt", "bytes": b64("ok")},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("批量接口应当整体返回 200，实际 %d %+v", code, out)
	}

	results, _ := out["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("应当逐个报告结果，实际 %+v", out)
	}
	first, _ := results[0].(map[string]any)
	if msg, _ := first["error"].(string); msg == "" {
		t.Fatalf("越界那条应当带上错误，实际 %+v", first)
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(site), "evil.txt")); err == nil {
		t.Fatal("越界路径不该被写出去")
	}
	if got, err := os.ReadFile(filepath.Join(site, "good.txt")); err != nil || string(got) != "ok" {
		t.Fatalf("同一批里合法的文件应当照写，实际 %q %v", got, err)
	}
}

// 失败后要能原样重试：页面得记住上一次的请求，并给一个按钮。
//
// 重试放在前端而不在后端：发布失败时后端已经写下了部分完成的记录，
// 重新发一次同一个请求，记录机制会跳过已上链的文件，
// 既不会重复付费，也不需要在后端另做一套「从断点继续」。
func TestPageOffersRetry(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	page, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page, []byte("function offerRetry(")) {
		t.Fatal("失败后应当给出重试按钮")
	}
	if !bytes.Contains(page, []byte("var lastTask = null")) {
		t.Fatal("应当记住上一次的请求参数")
	}
}

// 签名端点挂在 webui 的 /sign/ 下，用 webui 的 token 就能访问。
//
// 这是「不再另起端口、不再另开页面」的核心：一个 token 走通全程，
// 用户在同一个标签页里确认钱包。
func TestSignEndpointsMountedUnderWebUI(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL()+"sign/api/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("/sign/api/next 应当可达，实际 %d", res.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "" {
		t.Fatalf("还没有发布任务，不该有待签内容，实际 %q", out.ID)
	}
}

// arweave-js 也要能从 webui 下取到：页面靠它构造交易。
//
// 它不套 token：是公开的第三方库，而且 <script src> 带不了请求头。
func TestSignVendorServedUnderWebUI(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	res, err := http.Get(srv.baseURL() + "sign/vendor/arweave.js")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("arweave-js 应当可达，实际 %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) < 10000 {
		t.Fatalf("arweave-js 体积不对：%d 字节", len(body))
	}
}

// 签名端点共用 webui 的 token：拿别的 token 去问应当被拒。
func TestSignEndpointsRejectWrongToken(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL()+"sign/api/next", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", "not-the-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("错误 token 应当被拒，实际 %d", res.StatusCode)
	}
}

// 发布任务不再吐一个「签名页 <地址>」让用户自己去开。
//
// 连同那句被打印两遍的老问题一起盯住：签名就在当前页面里。
func TestPublishNoLongerPrintsSignURL(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, site)

	// 用 manual 代理但不填地址：任务会在这里失败，日志已经成型
	_, out := postJSON(t, srv.baseURL()+"api/publish", map[string]any{
		"site": site, "target": "turbo", "proxyMode": "manual",
	})
	taskID, _ := out["taskId"].(string)

	var snap map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
		if snap["status"] != "running" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	logs, _ := snap["logs"].([]any)
	for _, l := range logs {
		if s, _ := l.(string); strings.Contains(s, "签名页") {
			t.Fatalf("不该再打印签名页地址，实际有：%q", s)
		}
	}

	// 也不该再往 data 里塞 signUrl
	data, _ := snap["data"].(map[string]any)
	if _, ok := data["signUrl"]; ok {
		t.Fatal("不该再往任务 data 里放 signUrl")
	}
}

// 重试也要能签名：签名循环必须挂在 runTask 上，不能只绑在发布按钮上。
//
// 之前就是绑在按钮上，于是点重试时后端一直在等签名、前端却没人去弹钱包，
// 用户只看到一句「等待钱包确认」然后就卡住了。
func TestSignLoopRunsOnRetryToo(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	page, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"async function runTask(url, body, label, opts)",
		"if (o.sign)",
		"lastTask = { url: url, body: body, label: label, opts: o }",
	} {
		if !bytes.Contains(page, []byte(want)) {
			t.Fatalf("runTask 应当把「要不要签名」当参数带着走，缺少：%s", want)
		}
	}
}

// 没连过钱包时自动连一次。
//
// 不连就用钱包签名，会被钱包直接拒；而用户看到的是「一直没有弹窗」，
// 很难猜到是没授权。
func TestSignLoopConnectsWalletFirst(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	page, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page, []byte("var walletConnected = false")) {
		t.Fatal("应当记住钱包连接状态")
	}
	if !bytes.Contains(page, []byte("if (!walletConnected) {")) {
		t.Fatal("签名前应当先确认钱包已连接")
	}
}

// 任务跑着的时候按钮要变灰。
//
// 这不是装饰：连点两下打包，就是对着同一个目录各干一遍。
func TestPageDisablesButtonsWhileBusy(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	page, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"function setBusy(",
		"setBusy(true)",
		"setBusy(false)",
		"b.disabled = busy",
	} {
		if !bytes.Contains(page, []byte(want)) {
			t.Fatalf("任务进行中应当禁用按钮，缺少：%s", want)
		}
	}
}

// 打包不再问「增量还是全量」，界面上只留一个「完整重打包」。
func TestPageHasRebuildInsteadOfIncremental(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	page, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(page, []byte("packIncremental")) {
		t.Fatal("不该再有「增量更新」勾选框")
	}
	if !bytes.Contains(page, []byte("packRebuild")) {
		t.Fatal("应当有「完整重打包」开关")
	}
	if !bytes.Contains(page, []byte("默认自动")) {
		t.Fatal("应当告诉用户默认是自动判断")
	}
}

// 打包接口接受 rebuild 字段，不传就是自动。
func TestPackAcceptsRebuildFlag(t *testing.T) {
	site := t.TempDir()
	srv := newTestServer(t, site)

	src := t.TempDir()
	code, out := postJSON(t, srv.baseURL()+"api/pack", map[string]any{
		"source": src, "outDir": site, "name": "x", "rebuild": true,
	})
	if code != http.StatusOK {
		t.Fatalf("建任务应当返回 200，实际 %d", code)
	}
	if id, _ := out["taskId"].(string); id == "" {
		t.Fatal("应当返回 taskId")
	}
}

// b64 把一小段文本编成接口要的 base64。
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// 删目录要递归，不能只能删空目录。
func TestDeleteDirectoryRecursively(t *testing.T) {
	site := t.TempDir()
	deep := filepath.Join(site, "dir", "sub")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, site)
	code, out := postJSON(t, srv.baseURL()+"api/files/delete", map[string]any{
		"site": site, "path": "dir",
	})
	if code != http.StatusOK {
		t.Fatalf("删目录应当成功，实际 %d %+v", code, out)
	}
	if _, err := os.Stat(filepath.Join(site, "dir")); !os.IsNotExist(err) {
		t.Fatal("目录应当连同里面的东西一起消失")
	}
}

// 站点根不能删：那一下会把整个站点连同 .rog 一起清掉。
func TestDeleteRejectsSiteRoot(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, site)

	for _, p := range []string{".", "", "sub/.."} {
		code, _ := postJSON(t, srv.baseURL()+"api/files/delete", map[string]any{
			"site": site, "path": p,
		})
		if code == http.StatusOK {
			t.Fatalf("路径 %q 不该被允许删除", p)
		}
	}
	if _, err := os.Stat(filepath.Join(site, "a.txt")); err != nil {
		t.Fatal("站点内容不该被动过")
	}
}

// 复制文件与目录。
func TestCopyFileAndDirectory(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(site, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, site)

	// 先建个目标目录，把东西复制进去
	if code, _ := postJSON(t, srv.baseURL()+"api/files/mkdir", map[string]any{
		"site": site, "path": "dest",
	}); code != http.StatusOK {
		t.Fatalf("建目录失败，实际 %d", code)
	}

	code, out := postJSON(t, srv.baseURL()+"api/files/copy", map[string]any{
		"site": site, "from": []string{"a.txt", "pkg"}, "to": "dest",
	})
	if code != http.StatusOK {
		t.Fatalf("复制应当成功，实际 %d %+v", code, out)
	}

	results, _ := out["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("应当逐个报告结果，实际 %+v", out)
	}
	for _, r := range results {
		if m, _ := r.(map[string]any); m["error"] != nil && m["error"] != "" {
			t.Fatalf("复制出错: %+v", m)
		}
	}

	for _, rel := range []string{"dest/a.txt", "dest/pkg/b.txt"} {
		got, err := os.ReadFile(filepath.Join(site, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s 没被复制出来: %v", rel, err)
		}
		if len(got) == 0 {
			t.Fatalf("%s 是空的", rel)
		}
	}

	// 源还在（复制、不是移动）
	if _, err := os.Stat(filepath.Join(site, "a.txt")); err != nil {
		t.Fatal("复制之后源应当还在")
	}
}

// 不能把目录复制进它自己的子目录：那会无限递归。
func TestCopyRejectsIntoItself(t *testing.T) {
	site := t.TempDir()
	if err := os.MkdirAll(filepath.Join(site, "pkg", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site, "pkg", "b.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, site)
	code, out := postJSON(t, srv.baseURL()+"api/files/copy", map[string]any{
		"site": site, "from": []string{"pkg"}, "to": "pkg/inner",
	})
	if code != http.StatusOK {
		t.Fatalf("批量接口应当整体返回 200，实际 %d", code)
	}
	results, _ := out["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("应当报告一条结果，实际 %+v", out)
	}
	first, _ := results[0].(map[string]any)
	if msg, _ := first["error"].(string); msg == "" {
		t.Fatal("把目录复制进自己里面应当被拒")
	}

	// 别真的递归出一堆东西来
	if _, err := os.Stat(filepath.Join(site, "pkg", "inner", "pkg")); err == nil {
		t.Fatal("不该真的复制进去")
	}
}

// 新建目录支持嵌套。
func TestMkdirCreatesNested(t *testing.T) {
	site := t.TempDir()
	srv := newTestServer(t, site)

	code, out := postJSON(t, srv.baseURL()+"api/files/mkdir", map[string]any{
		"site": site, "path": "a/b/c",
	})
	if code != http.StatusOK {
		t.Fatalf("建目录应当成功，实际 %d %+v", code, out)
	}
	if info, err := os.Stat(filepath.Join(site, "a", "b", "c")); err != nil || !info.IsDir() {
		t.Fatalf("嵌套目录没建出来: %v", err)
	}
}

// 一轮完整的内嵌签名：发布任务排队 → 「页面」取走内容 → 回传签名 → 任务继续。
//
// 这是 W4 的端到端。以前签名是个独立服务，测试里得再起一个端口；
// 现在它就在 webui 自己的 mux 下，用同一个 token 就能走完全程。
//
// 这里扮演“页面”的是一段 Go 代码，它按要求依次调 /sign/api/next、
// /sign/api/blob/<id>、/sign/api/sign/<id>，与浏览器里那段脚本做同一件事。
// 最终提交由一个假上传服务接住，不碰真网络。
func TestPublishSignsThroughMountedEndpoints(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site, "a.txt"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	var uploaded int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&uploaded, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"fake-id-%d"}`, n)
	}))
	defer upstream.Close()

	srv := newTestServer(t, site)

	code, out := postJSON(t, srv.baseURL()+"api/publish", map[string]any{
		"site": site, "target": "turbo", "endpoint": upstream.URL, "proxyMode": "off",
	})
	if code != http.StatusOK {
		t.Fatalf("建任务应当返回 200，实际 %d", code)
	}
	taskID, _ := out["taskId"].(string)

	// 扮演页面，直到任务收尾
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}

			req, _ := http.NewRequest(http.MethodGet, srv.baseURL()+"sign/api/next", nil)
			req.Header.Set("X-Rog-Token", testToken)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			var task struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			}
			_ = json.NewDecoder(res.Body).Decode(&task)
			res.Body.Close()

			if task.ID == "" {
				// 暂时没有待签内容，而不是结束了
				time.Sleep(20 * time.Millisecond)
				continue
			}

			// 取内容（这里的内容就是钱包看到的那份字节）
			breq, _ := http.NewRequest(http.MethodGet, srv.baseURL()+"sign/api/blob/"+task.ID, nil)
			breq.Header.Set("X-Rog-Token", testToken)
			bres, err := http.DefaultClient.Do(breq)
			if err != nil {
				return
			}
			payload, _ := io.ReadAll(bres.Body)
			bres.Body.Close()
			if len(payload) == 0 {
				t.Error("待签内容不该是空的")
				return
			}

			// 假签名：真的钱包会在这里闷一个 ANS-104 字节串。
			// 内容本身对 Uploader 无意义，它只负责把字节递出去。
			sreq, _ := http.NewRequest(http.MethodPost,
				srv.baseURL()+"sign/api/sign/"+task.ID, bytes.NewReader([]byte("signed-"+task.ID)))
			sreq.Header.Set("X-Rog-Token", testToken)
			sres, err := http.DefaultClient.Do(sreq)
			if err != nil {
				return
			}
			sres.Body.Close()
		}
	}()

	var snap map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
		if snap["status"] != "running" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if snap["status"] != "done" {
		t.Fatalf("发布应当成功，实际 %v：%v", snap["status"], snap["error"])
	}

	// 4 个内容文件 + 1 份发布记录 + 1 份 manifest = 6 次提交
	if n := atomic.LoadInt32(&uploaded); n < 3 {
		t.Fatalf("上传服务收到的提交太少：%d", n)
	}
}

// 手动代理模式却没填地址，应当在动网络之前就报错。
//
// 检查要早于起签名服务：否则会先弹出个签名页，用户白等一场
// 才发现代理没填。
func TestPublishRejectsManualProxyWithoutURL(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, site)

	code, out := postJSON(t, srv.baseURL()+"api/publish", map[string]any{
		"site": site, "target": "turbo", "proxyMode": "manual",
	})
	if code != http.StatusOK {
		t.Fatalf("建任务应当返回 200，实际 %d", code)
	}
	taskID, _ := out["taskId"].(string)

	var snap map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
		if snap["status"] != "running" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if snap["status"] != "failed" {
		t.Fatalf("应当失败，实际 %v", snap["status"])
	}
	if msg, _ := snap["error"].(string); !strings.Contains(msg, "代理") {
		t.Fatalf("失败原因应当点明代理配置，实际 %q", msg)
	}
}

// 未知的代理模式同样要提前拦住。
func TestPublishRejectsUnknownProxyMode(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, site)

	_, out := postJSON(t, srv.baseURL()+"api/publish", map[string]any{
		"site": site, "target": "turbo", "proxyMode": "bogus",
	})
	taskID, _ := out["taskId"].(string)

	var snap map[string]any
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
		if snap["status"] != "running" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if msg, _ := snap["error"].(string); !strings.Contains(msg, "代理") {
		t.Fatalf("失败原因应当点明代理模式，实际 %q", msg)
	}
}

func TestDeleteFile(t *testing.T) {
	site := t.TempDir()
	target := filepath.Join(site, "a.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, site)

	code, _ := postJSON(t, srv.baseURL()+"api/files/delete", map[string]any{"site": site, "path": "a.txt"})
	if code != http.StatusOK {
		t.Fatalf("删除应当成功，实际 %d", code)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("文件应当已被删除")
	}
}

// 打包失败要以任务失败的形式报出来，而不是让请求挂住。
func TestPackTaskReportsFailure(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	code, out := postJSON(t, srv.baseURL()+"api/pack", map[string]any{
		"source": filepath.Join(t.TempDir(), "does-not-exist"),
		"outDir": t.TempDir(),
	})
	if code != http.StatusOK {
		t.Fatalf("创建任务应当返回 200，实际 %d", code)
	}
	taskID, _ := out["taskId"].(string)
	if taskID == "" {
		t.Fatal("应当返回 taskId")
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var snap map[string]any
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)

		switch snap["status"] {
		case "failed":
			if msg, _ := snap["error"].(string); msg == "" {
				t.Fatal("失败任务应当带上原因")
			}
			return
		case "done":
			t.Fatal("源仓库不存在，不该成功")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("任务迟迟不结束")
}

// 参数不合格要在建任务之前就被拒，不用等任务跑完才知道。
func TestPackRejectsEmptySource(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	code, _ := postJSON(t, srv.baseURL()+"api/pack", map[string]any{"source": "   "})
	if code != http.StatusBadRequest {
		t.Fatalf("空源仓库应当在建任务前被拒，实际 %d", code)
	}
}

// 页面内嵌且不引用任何外部资源。
func TestPageIsSelfContained(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	res, err := http.Get(srv.baseURL() + "?token=" + srv.Token())
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(res.Body); err != nil {
		t.Fatal(err)
	}
	body := buf.String()

	if !strings.Contains(body, "维护台") {
		t.Fatal("页面内容不对")
	}
	for _, bad := range []string{"cdn.", "unpkg.com", "googleapis"} {
		if strings.Contains(body, bad) {
			t.Fatalf("界面不该引用外部资源，出现了 %q", bad)
		}
	}
}

// 不带 token 的请求一律拒掉。
//
// 服务只绑 127.0.0.1，但同机的任意网页都能向它发请求，
// 一个恶意页面就能让浏览器替它改站点文件、发起发布。
func TestRequestsWithoutTokenAreRejected(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, site)

	// 首页与各接口都不该放行
	for _, path := range []string{"", "api/state", "api/files/delete"} {
		res, err := http.Get(srv.baseURL() + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("%q 不带 token 应当被拒，实际 %d", path, res.StatusCode)
		}
	}

	// 带错 token 同样不行
	res, err := http.Get(srv.baseURL() + "api/state?token=wrong")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("错误 token 应当被拒，实际 %d", res.StatusCode)
	}
}

// 发布接口接了进程内互斥：已有同名发布在跑时，后一条应当被拒。
//
// 界面上连点两下按钮就会撞到这里，与其让两条发布互踩同一份记录，
// 不如把后一条拦下来说清原因。
func TestPublishRejectsWhenAlreadyRunning(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, site)

	abs, err := filepath.Abs(site)
	if err != nil {
		t.Fatal(err)
	}
	// 先手动占住名额，模拟「已经有一条在跑」
	release, err := publish.Acquire(abs, "local")
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	code, out := postJSON(t, srv.baseURL()+"api/publish", map[string]any{
		"site": site, "target": "local", "dest": t.TempDir(),
	})
	if code != http.StatusOK {
		t.Fatalf("建任务应当返回 200，实际 %d", code)
	}

	taskID, _ := out["taskId"].(string)
	if taskID == "" {
		t.Fatal("应当返回 taskId")
	}

	// 任务异步跑，等它报出那句话
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var snap map[string]any
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
		if snap["status"] == "failed" {
			msg, _ := snap["error"].(string)
			if !strings.Contains(msg, "在跑") {
				t.Fatalf("失败原因应当点明已有发布在跑，实际 %q", msg)
			}
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("任务迟迟不结束")
}

// 站点目录不存在时，状态里应当报出「骨架全缺」。
//
// 这不是错误，而是「还没建站点」——界面据此提示铺一下骨架。
func TestStateReportsScaffoldWhenSiteMissing(t *testing.T) {
	srv := newTestServer(t, filepath.Join(t.TempDir(), "not-created"))

	var st stateResponse
	getJSON(t, srv.baseURL()+"api/state", &st)

	if st.Exists {
		t.Fatal("目录不存在时不该说它存在")
	}
	if st.Error == "" {
		t.Fatal("应当带上一句说明")
	}
	if st.Scaffold.Total == 0 {
		t.Fatal("应当报出骨架总共有多少个文件")
	}
	if st.Scaffold.Missing != st.Scaffold.Total {
		t.Fatalf("目录不存在时骨架应当全缺，实际 %d / %d", st.Scaffold.Missing, st.Scaffold.Total)
	}
	if len(st.Scaffold.Templates) == 0 {
		t.Fatal("应当列出内置模板")
	}
}

// 铺骨架接口要把内嵌的前端文件写到站点目录。
//
// 这正是「只有一个 exe 也能把站点立起来」的那一步。
func TestSiteInitWritesScaffold(t *testing.T) {
	site := t.TempDir()
	srv := newTestServer(t, site)

	code, out := postJSON(t, srv.baseURL()+"api/site/init", map[string]any{
		"site": site, "template": "default",
	})
	if code != http.StatusOK {
		t.Fatalf("建任务应当返回 200，实际 %d", code)
	}
	taskID, _ := out["taskId"].(string)
	if taskID == "" {
		t.Fatal("应当返回 taskId")
	}

	// 等任务收尾
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var snap map[string]any
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
		if snap["status"] == "done" {
			break
		}
		if snap["status"] == "failed" {
			t.Fatalf("铺骨架失败: %v", snap["error"])
		}
		time.Sleep(30 * time.Millisecond)
	}

	for _, rel := range []string{"index.html", filepath.Join("src", "main.js")} {
		if _, err := os.Stat(filepath.Join(site, rel)); err != nil {
			t.Fatalf("%s 没被写出来: %v", rel, err)
		}
	}

	var st stateResponse
	getJSON(t, srv.baseURL()+"api/state", &st)
	if st.Scaffold.Missing != 0 {
		t.Fatalf("铺完之后不该还缺骨架文件，实际缺 %d", st.Scaffold.Missing)
	}
}

// 默认不覆盖：界面里改过的文件不该被模板盖掉。
func TestSiteInitKeepsExistingByDefault(t *testing.T) {
	site := t.TempDir()
	target := filepath.Join(site, "index.html")
	if err := os.WriteFile(target, []byte("我改过的"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, site)

	_, out := postJSON(t, srv.baseURL()+"api/site/init", map[string]any{"site": site})
	taskID, _ := out["taskId"].(string)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var snap map[string]any
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
		if snap["status"] != "running" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "我改过的" {
		t.Fatal("默认不该覆盖已存在的文件")
	}
}

// 发布面板不再提供「仓库名」这一栏：一个站点一个仓库，名字由目录名决定。
//
// 这一条挡的是「有人手滑把它加回来」。那个输入框的代价不是多一个字段，
// 而是产物里的 Repo 标签与发布记录的身份可能被填成别的东西。
func TestPageHasNoRepoField(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	page, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(page, []byte("pubRepo")) {
		t.Fatal("发布面板不该再出现仓库名输入框")
	}
	if !bytes.Contains(page, []byte("仓库名取站点目录名")) {
		t.Fatal("应当有一句说明告诉用户名字从哪来")
	}
}

// 仓库名从站点目录名推导，尾随分隔符要先规整掉。
func TestRepoNameFor(t *testing.T) {
	cases := []struct{ in, want string }{
		{`D:\Project\web\rog`, "rog"},
		{`D:\Project\web\rog\`, "rog"},
		{"/home/me/site", "site"},
		{"/home/me/site/", "site"},
		{"site", "site"},
	}
	for _, c := range cases {
		if got := repoNameFor(c.in); got != c.want {
			t.Errorf("repoNameFor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 站点文件逐层浏览：一次只列一个目录，像资源管理器。
//
// 之前把整棵树的路径铺开，文件一多就要在长列表里找。
// 现在页面里要有聚合「当前目录直接子项」、面包屑与进入目录的逻辑。
func TestPageBrowsesByDirectory(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	page, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`class="tree" id="files"`,
		"function childrenOf(",
		"function renderCrumbs(",
		"function enterDir(",
	} {
		if !bytes.Contains(page, []byte(want)) {
			t.Fatalf("应当逐层浏览，缺少：%s", want)
		}
	}
	if bytes.Contains(page, []byte("function buildTree(")) {
		t.Fatal("不该再把整棵树铺开")
	}
}

// 目录层级也要能删、能复制。
//
// 这是「轻量资源管理器」与一张文件表的区别：只有文件能操作、
// 目录只能进去看看，整理站点就还是要回到命令行。
func TestPageHasDirectoryActions(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	page, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"function actionCell(",
		"function removeEntry(",
		"function copySelected(",
		"function pasteClip(",
		"function newFolder(",
		`id="fileUp"`,
		`id="fileCopy"`,
		`id="filePaste"`,
		`id="fileMkdir"`,
	} {
		if !bytes.Contains(page, []byte(want)) {
			t.Fatalf("目录与文件都该能操作，缺少：%s", want)
		}
	}
}

// 拖入的落点由当前目录决定，不再看拖到哪一行上。
//
// 真正要判断的是「重不重名」，而那件事与落点在哪一行无关。
func TestPageDropLandsInCurrentDir(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	req, err := http.NewRequest(http.MethodGet, srv.baseURL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Rog-Token", testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	page, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(page, []byte("attachPanelDrop(el('files'), function () { return cwd; })")) {
		t.Fatal("整个面板应当是一个拖放区，落点是当前目录")
	}
	if bytes.Contains(page, []byte("function attachFileDrop(")) {
		t.Fatal("不该再有「拖到某一行就替换那一行」的逻辑")
	}
}

// 发布面板的完整闭环：点一次发布，产物落到目标目录，记录也写进站点。
//
// 走 local 目标：不碰网络也不碰钱包，但会把「扫描站点 → 逐个文件写出 →
// 写发布记录」这条链路完整走一遍。这类界面改动最实在的回归网就是它。
func TestPublishLocalEndToEnd(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(site, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(site, "src", "a.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	srv := newTestServer(t, site)

	code, out := postJSON(t, srv.baseURL()+"api/publish", map[string]any{
		"site": site, "target": "local", "dest": dest,
	})
	if code != http.StatusOK {
		t.Fatalf("建任务应当返回 200，实际 %d", code)
	}
	taskID, _ := out["taskId"].(string)

	var snap map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
		if snap["status"] != "running" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if snap["status"] != "done" {
		t.Fatalf("发布应当成功，实际 %v：%v", snap["status"], snap["error"])
	}

	// 产物按路径落位
	for _, rel := range []string{"index.html", filepath.Join("src", "a.js")} {
		got, err := os.ReadFile(filepath.Join(dest, rel))
		if err != nil {
			t.Fatalf("%s 没被写出来: %v", rel, err)
		}
		if len(got) == 0 {
			t.Fatalf("%s 是空的", rel)
		}
	}

	// 发布记录要落在站点里的 .rog 下，下次才能做增量
	entries, err := os.ReadDir(filepath.Join(site, publish.StateDir))
	if err != nil {
		t.Fatalf("站点里应当有发布记录目录: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("发布记录目录是空的")
	}
}

// 页面上的节点候选必须与 arweave.KnownNodes 一致。
//
// 两边分开写就会漂移：一边加了新网关，另一边不知道。
// 这也是一道回归门：改 KnownNodes 而忘了改页面，测试当场报出来。
func TestPageNodeOptionsMatchKnownNodes(t *testing.T) {
	m := regexp.MustCompile(`(?s)<datalist id="nodeList">(.*?)</datalist>`).FindStringSubmatch(DefaultPage)
	if m == nil {
		t.Fatal("页面里找不到 nodeList 这个 datalist")
	}
	var got []string
	for _, mm := range regexp.MustCompile(`<option value="([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		got = append(got, mm[1])
	}

	want := arweave.KnownNodes
	if len(got) != len(want) {
		t.Fatalf("候选数量不一致：页面 %d 个 %v，KnownNodes %d 个 %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 项不一致：页面 %q，KnownNodes %q", i, got[i], want[i])
		}
	}
}

// 节点输入框要能提候选：少了 list 属性，datalist 就是一段死代码。
func TestPageNodeInputUsesDatalist(t *testing.T) {
	if !strings.Contains(DefaultPage, `id="pubNode" list="nodeList"`) {
		t.Error(`pubNode 应当用 list="nodeList" 关联候选`)
	}
}

// 页面不该自己改 reward：价格交给 arweave-js 去问节点的 /price。
//
// 曾经乘过 2（排查「上链了却打不开」时为了排除变量），
// 真因落在 bundle 的 ANS-104 头部上，与手续费无关。
// 这条测试是防止它被再加回来：一旦有人在页面里动 reward，这里当场报。
//
// 只禁「赋值」不禁「读取」：页面要把 reward 回传给 Go 写日志。
func TestPageDoesNotOverrideReward(t *testing.T) {
	for _, bad := range []string{"REWARD_MULTIPLIER", "bumpReward", "tx.reward =", "tx.reward="} {
		if strings.Contains(DefaultPage, bad) {
			t.Errorf("页面里不该出现 %q：reward 应当照节点报价，不额外加价", bad)
		}
	}
	if !strings.Contains(DefaultPage, "createTransaction") {
		t.Error("页面应当用 arweave-js 的 createTransaction 构造交易")
	}
}

// 多块时页面不提交，只把 proofs 回传，由 Go 提交。
//
// 这是一道回归门：arweave-js 的 upload() 不暴露响应体，
// 它说成功时你没法知道节点实际回了什么。实测栽过的正是这种情形，
// 一旦有人把多块又交回 upload，失败时就再也看不见线索。
func TestPageHandsMultiChunkToGo(t *testing.T) {
	for _, good := range []string{"uploaded: false", "proofs: proofs", "data_path"} {
		if !strings.Contains(DefaultPage, good) {
			t.Errorf("页面里应当有 %q", good)
		}
	}
	if strings.Contains(DefaultPage, "transactions.upload") {
		t.Error("页面不该再用 transactions.upload：它不暴露响应体，出了问题看不见")
	}
}

// 从链上恢复有自己的面板：它是往本地拿内容，与发布（往外写）不是一件事。
//
// 也不该再留在发布面板里——之前那个 pubFrom 输入框其实就是它，
// 混在发布里会让人以为两件事要一起做。
func TestPageHasIndependentRestorePanel(t *testing.T) {
	for _, want := range []string{"restorePanel", "restoreEntry", "restoreDest", "doRestore"} {
		if !strings.Contains(DefaultPage, want) {
			t.Errorf("页面里应当有 %q", want)
		}
	}
	if strings.Contains(DefaultPage, "pubFrom") {
		t.Error("发布面板里不该再有从链上恢复的入口：它已经独立成块")
	}
}

// 恢复的目标目录要写明「必须为空」，并讲清不会替用户清空。
func TestPageRestoreExplainsEmptyDir(t *testing.T) {
	if !strings.Contains(DefaultPage, "必须是空目录") {
		t.Error("恢复面板应当写明目标目录必须为空")
	}
	if !strings.Contains(DefaultPage, "也不会替你清空") {
		t.Error("应当讲清非空目录不会被覆盖或清空")
	}
}
