package webui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T, site string) *Server {
	t.Helper()
	// 端口传 0：测试之间互不干扰，由系统挑空闲的
	srv := New(site, 0)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
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

	if !strings.HasSuffix(srv.URL(), ":"+strconv.Itoa(port)+"/") {
		t.Fatalf("URL 应当用指定端口，实际 %s", srv.URL())
	}
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	res, err := http.Get(url)
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
	res, err := http.Post(url, "application/json", bytes.NewReader(data))
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
	getJSON(t, srv.URL()+"api/state", &st)

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
	getJSON(t, srv.URL()+"api/state", &st)

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
	getJSON(t, srv.URL()+"api/state", &before)

	code, _ := postJSON(t, srv.URL()+"api/files/replace", map[string]any{
		"site":  site,
		"path":  "a.txt",
		"bytes": base64.StdEncoding.EncodeToString([]byte("brand new content")),
	})
	if code != http.StatusOK {
		t.Fatalf("替换应当成功，实际 %d", code)
	}

	var after stateResponse
	getJSON(t, srv.URL()+"api/state", &after)

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

func TestReplaceRejectsEscape(t *testing.T) {
	site := t.TempDir()
	srv := newTestServer(t, site)

	code, out := postJSON(t, srv.URL()+"api/files/replace", map[string]any{
		"site":  site,
		"path":  "../evil.txt",
		"bytes": base64.StdEncoding.EncodeToString([]byte("x")),
	})
	if code != http.StatusBadRequest {
		t.Fatalf("越界路径应当被拒，实际 %d %+v", code, out)
	}
}

func TestDeleteFile(t *testing.T) {
	site := t.TempDir()
	target := filepath.Join(site, "a.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, site)

	code, _ := postJSON(t, srv.URL()+"api/files/delete", map[string]any{"site": site, "path": "a.txt"})
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

	code, out := postJSON(t, srv.URL()+"api/pack", map[string]any{
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
		getJSON(t, srv.URL()+"api/task/"+taskID, &snap)

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

	code, _ := postJSON(t, srv.URL()+"api/pack", map[string]any{"source": "   "})
	if code != http.StatusBadRequest {
		t.Fatalf("空源仓库应当在建任务前被拒，实际 %d", code)
	}
}

// 页面内嵌且不引用任何外部资源。
func TestPageIsSelfContained(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	res, err := http.Get(srv.URL())
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
