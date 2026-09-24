package arweave

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGateway 扮一个网关：按路径返回预设内容，没有的给 404。
//
// 会把开头的 raw/ 去掉：真实网关取原始字节要带这一层，
// 而这里关心的只是「哪个 id 对应哪段内容」。
func fakeGateway(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/")
		key = strings.TrimPrefix(key, "raw/")
		body, ok := files[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not Found."))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

// 取内容必须走 /raw/<id>。不这样写的话，网关会把 manifest 解析成它
// index 指向的那个文件直接吐出来（实测过，拿到的是一段 HTML），
// 于是「取 manifest」就失败在解 JSON 上。
func TestFetchUsesRawEndpoint(t *testing.T) {
	var sawRaw bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/raw/") {
			sawRaw = true
		}
		_, _ = w.Write([]byte(`{"manifest":"arweave/paths","version":"0.2.0","paths":{"a":{"id":"1"}}}`))
	}))
	defer srv.Close()

	if _, err := FetchManifest(context.Background(), srv.URL, "e", srv.Client()); err != nil {
		t.Fatalf("取 manifest 失败: %v", err)
	}
	if !sawRaw {
		t.Error("应当走 /raw/<id>，否则网关会把 manifest 解析成 index 指向的内容")
	}
}

func TestFetchManifestParsesPaths(t *testing.T) {
	m := &Manifest{
		Manifest: "arweave/paths",
		Version:  "0.2.0",
		Index:    &ManifestIndex{Path: "index.html"},
		Paths: map[string]ManifestEntry{
			"index.html":  {ID: "id-html"},
			"src/main.js": {ID: "id-js"},
		},
	}
	raw, _ := json.Marshal(m)
	srv := fakeGateway(t, map[string]string{"entry-1": string(raw)})
	defer srv.Close()

	plan, err := FetchManifest(context.Background(), srv.URL, "entry-1", srv.Client())
	if err != nil {
		t.Fatalf("取 manifest 失败: %v", err)
	}
	if plan.IndexPath != "index.html" {
		t.Errorf("默认入口期望 index.html，得到 %q", plan.IndexPath)
	}
	if len(plan.Paths) != 2 || plan.Paths["src/main.js"] != "id-js" {
		t.Errorf("路径映射不对: %v", plan.Paths)
	}
}

// 入口填成交易 id 时拿回来的是一段二进制，解不出 manifest 要给一句能懂的提示。
func TestFetchManifestRejectsNonManifest(t *testing.T) {
	srv := fakeGateway(t, map[string]string{"tx-1": "\x00\x01binary"})
	defer srv.Close()

	_, err := FetchManifest(context.Background(), srv.URL, "tx-1", srv.Client())
	if err == nil {
		t.Fatal("不是 manifest 时应当报错")
	}
	if !strings.Contains(err.Error(), "不是一份 manifest") {
		t.Errorf("错误信息应当说清这回事，实际: %v", err)
	}
}

// manifest 的 schema 字段不对时要直接说，不要当成 0 个路径糊过去。
func TestFetchManifestRejectsWrongSchema(t *testing.T) {
	srv := fakeGateway(t, map[string]string{"e": `{"paths":{"a":{"id":"1"}}}`})
	defer srv.Close()

	_, err := FetchManifest(context.Background(), srv.URL, "e", srv.Client())
	if err == nil || !strings.Contains(err.Error(), "arweave/paths") {
		t.Fatalf("schema 不对时应当报错，实际: %v", err)
	}
}

func TestRestoreIntoWritesFiles(t *testing.T) {
	m := &Manifest{
		Manifest: "arweave/paths",
		Version:  "0.2.0",
		Paths: map[string]ManifestEntry{
			"index.html":  {ID: "id-html"},
			"src/main.js": {ID: "id-js"},
		},
	}
	raw, _ := json.Marshal(m)
	srv := fakeGateway(t, map[string]string{
		"entry":   string(raw),
		"id-html": "<h1>hi</h1>",
		"id-js":   "console.log(1)",
	})
	defer srv.Close()

	plan, err := FetchManifest(context.Background(), srv.URL, "entry", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out")
	n, err := RestoreInto(context.Background(), srv.URL, plan, dest, srv.Client(), nil)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("期望写 2 个文件，实际 %d", n)
	}
	got, err := os.ReadFile(filepath.Join(dest, "src", "main.js"))
	if err != nil {
		t.Fatalf("嵌套路径没写出来: %v", err)
	}
	if string(got) != "console.log(1)" {
		t.Errorf("内容不对: %q", got)
	}
}

// 目标目录非空时必须直接报错，不覆盖、也不替人清空。
func TestRestoreIntoRefusesNonEmptyDir(t *testing.T) {
	dest := t.TempDir()
	keep := filepath.Join(dest, "keep.txt")
	if err := os.WriteFile(keep, []byte("别动我"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan := &RestorePlan{Entry: "e", Paths: map[string]string{"a.txt": "id-a"}}
	_, err := RestoreInto(context.Background(), "https://example.invalid", plan, dest, nil, nil)
	if err == nil {
		t.Fatal("非空目录应当报错")
	}
	if !strings.Contains(err.Error(), "不是空的") {
		t.Errorf("错误信息应当点明目录非空，实际: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("不该动目录里原有的东西: %v", err)
	}
}

// 已存在但为空的目录应当放行——这要求是用户自己选的，不是我们替他造的。
func TestRestoreIntoAcceptsEmptyDir(t *testing.T) {
	m := &Manifest{Manifest: "arweave/paths", Version: "0.2.0",
		Paths: map[string]ManifestEntry{"a.txt": {ID: "id-a"}}}
	raw, _ := json.Marshal(m)
	srv := fakeGateway(t, map[string]string{"e": string(raw), "id-a": "A"})
	defer srv.Close()

	plan, _ := FetchManifest(context.Background(), srv.URL, "e", srv.Client())
	if _, err := RestoreInto(context.Background(), srv.URL, plan, t.TempDir(), srv.Client(), nil); err != nil {
		t.Fatalf("空目录应当放行: %v", err)
	}
}

// manifest 是从链上取回来的外部输入，里面的路径不能信。
func TestRestoreRejectsEscapingPath(t *testing.T) {
	for _, bad := range []string{"../evil.txt", "a/../../evil.txt", "/abs.txt"} {
		dest := t.TempDir()
		plan := &RestorePlan{Entry: "e", Paths: map[string]string{bad: "id-x"}}
		if _, err := RestoreInto(context.Background(), "https://example.invalid", plan, dest, nil, nil); err == nil {
			t.Errorf("路径 %q 应当被挡住", bad)
		}
	}
}

// 恢复过程中取不到某个文件时，要把是哪个文件说清楚。
func TestRestoreReportsWhichFileFailed(t *testing.T) {
	m := &Manifest{Manifest: "arweave/paths", Version: "0.2.0",
		Paths: map[string]ManifestEntry{"missing.js": {ID: "id-missing"}}}
	raw, _ := json.Marshal(m)
	srv := fakeGateway(t, map[string]string{"e": string(raw)})
	defer srv.Close()

	plan, _ := FetchManifest(context.Background(), srv.URL, "e", srv.Client())
	dest := filepath.Join(t.TempDir(), "out")
	_, err := RestoreInto(context.Background(), srv.URL, plan, dest, srv.Client(), nil)
	if err == nil {
		t.Fatal("取不到文件时应当报错")
	}
	if !strings.Contains(err.Error(), "missing.js") {
		t.Errorf("错误信息里应当有出问题的路径，实际: %v", err)
	}
}
