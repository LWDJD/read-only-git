package arweave

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LWDJD/read-only-git/internal/publish"
)

// stubSigner 是假的签名通道：给内容加个前缀充当「签名结果」。
type stubSigner struct {
	mu    sync.Mutex
	calls int
}

func (s *stubSigner) Sign(ctx context.Context, data []byte, tags []Tag) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	out := make([]byte, 0, len(data)+7)
	out = append(out, "signed:"...)
	return append(out, data...), nil
}

func (s *stubSigner) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// fakeTurbo 起一个假的 /tx 端点，每次返回递增的 id。
//
// id 用包级计数器而不是每实例自增：测试里会同时存在多个假端点，
// 各自从 1 开始会让两次不同上传拿到同一个 id，看起来像是被复用了。
var turboSeq int64

func fakeTurbo(t *testing.T) (*Uploader, func() int) {
	t.Helper()
	var mu sync.Mutex
	n := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		n++
		mu.Unlock()
		seq := atomic.AddInt64(&turboSeq, 1)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"id":"id-%d"}`, seq)))
	}))
	t.Cleanup(srv.Close)

	return NewUploader(srv.URL), func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

func writeSite(t *testing.T, files map[string]string) *publish.Site {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	site, err := publish.Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	return site
}

func TestPublishUploadsEverythingFirstTime(t *testing.T) {
	site := writeSite(t, map[string]string{
		"index.html": "<h1>hi</h1>",
		"a.txt":      "hello",
	})
	up, count := fakeTurbo(t)
	signer := &stubSigner{}
	target := &Target{Repo: "demo", Uploader: up, Signer: signer}

	rec, err := target.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 2 个文件 + 1 份 manifest
	if got := count(); got != 3 {
		t.Fatalf("首次应上传 3 次，实际 %d", got)
	}
	if signer.count() != 3 {
		t.Fatalf("签名次数应与上传一致，实际 %d", signer.count())
	}
	if len(rec.Refs) != 2 {
		t.Fatalf("应有 2 条引用，实际 %d: %+v", len(rec.Refs), rec.Refs)
	}
	if rec.Root == "" {
		t.Fatal("入口 id 不该为空")
	}
	if rec.Target != "arweave" {
		t.Fatalf("Target = %q", rec.Target)
	}
}

func TestPublishReusesUnchangedContent(t *testing.T) {
	site := writeSite(t, map[string]string{
		"index.html": "<h1>hi</h1>",
		"a.txt":      "hello",
		"b.txt":      "world",
	})
	up, count := fakeTurbo(t)
	target := &Target{Repo: "demo", Uploader: up, Signer: &stubSigner{}}

	first, err := target.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}
	afterFirst := count()

	// 只动一个文件
	if err := os.WriteFile(filepath.Join(site.Root, "b.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	site2, err := publish.Scan(site.Root)
	if err != nil {
		t.Fatal(err)
	}

	second, err := target.Publish(context.Background(), site2, first)
	if err != nil {
		t.Fatal(err)
	}

	// 增量只该多传：b.txt + manifest
	if delta := count() - afterFirst; delta != 2 {
		t.Fatalf("增量应只上传 2 次（1 文件 + 1 manifest），实际 %d", delta)
	}
	if second.Refs["a.txt"] != first.Refs["a.txt"] {
		t.Fatal("未变文件的引用应被复用")
	}
	if second.Refs["b.txt"] == first.Refs["b.txt"] {
		t.Fatal("变化文件应拿到新引用")
	}
	if second.Refs["index.html"] != first.Refs["index.html"] {
		t.Fatal("index.html 未变，引用该复用")
	}
}

func TestPublishNoChangeUploadsOnlyManifest(t *testing.T) {
	site := writeSite(t, map[string]string{"a.txt": "same"})
	up, count := fakeTurbo(t)
	target := &Target{Repo: "r", Uploader: up, Signer: &stubSigner{}}

	first, err := target.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}
	afterFirst := count()

	second, err := target.Publish(context.Background(), site, first)
	if err != nil {
		t.Fatal(err)
	}

	if delta := count() - afterFirst; delta != 1 {
		t.Fatalf("完全未变时应只上传 manifest，实际多传 %d 次", delta)
	}
	if second.Refs["a.txt"] != first.Refs["a.txt"] {
		t.Fatal("引用应被复用")
	}
}

func TestPublishManifestCoversAllFiles(t *testing.T) {
	site := writeSite(t, map[string]string{
		"index.html": "x",
		"sub/a.txt":  "y",
	})
	up, _ := fakeTurbo(t)
	target := &Target{Repo: "r", Uploader: up, Signer: &stubSigner{}}

	rec, err := target.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"index.html", "sub/a.txt"} {
		if rec.Refs[p] == "" {
			t.Fatalf("%s 缺少引用: %+v", p, rec.Refs)
		}
	}
	if EntryPath(site) != "index.html" {
		t.Fatalf("入口应为 index.html，实际 %q", EntryPath(site))
	}
}

func TestPublishRequiresDependencies(t *testing.T) {
	site := writeSite(t, map[string]string{"a.txt": "x"})

	if _, err := (&Target{}).Publish(context.Background(), site, nil); err == nil {
		t.Fatal("缺上传器应报错")
	}

	up, _ := fakeTurbo(t)
	if _, err := (&Target{Uploader: up}).Publish(context.Background(), site, nil); err == nil {
		t.Fatal("缺签名通道应报错")
	}
}

func TestContentTypeFor(t *testing.T) {
	cases := map[string]string{
		"index.html":                          "text/html; charset=utf-8",
		"app.js":                              "text/javascript; charset=utf-8",
		"style.css":                           "text/css; charset=utf-8",
		"repository.json":                     "application/json; charset=utf-8",
		"README.md":                           "text/markdown; charset=utf-8",
		"demo.git/objects/pack/pack-abc.pack": "application/octet-stream",
		"unknown.zzz":                         "application/octet-stream",
		"UPPER.HTML":                          "text/html; charset=utf-8",
	}
	for in, want := range cases {
		if got := ContentTypeFor(in); got != want {
			t.Errorf("ContentTypeFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// 出错时必须返回部分记录，否则已上传（已付费）的内容下次会被重传。
func TestPublishReturnsPartialRecordOnFailure(t *testing.T) {
	var mu sync.Mutex
	n := 0

	// 第 2 次请求返回 400（不可重试），制造中途失败
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		cur := n
		mu.Unlock()
		if cur == 2 {
			http.Error(w, "nope", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"id":"id-` + strconv.Itoa(cur) + `"}`))
	}))
	defer srv.Close()

	site := writeSite(t, map[string]string{
		"a.txt": "1",
		"b.txt": "2",
		"c.txt": "3",
	})

	up := NewUploader(srv.URL)
	up.RetryBackoff = time.Millisecond
	target := &Target{Repo: "r", Uploader: up, Signer: &stubSigner{}}

	rec, err := target.Publish(context.Background(), site, nil)
	if err == nil {
		t.Fatal("中途失败应当报错")
	}
	if rec == nil {
		t.Fatal("出错时必须返回部分记录，否则已付费的内容下次会被重传")
	}
	if len(rec.Refs) == 0 {
		t.Fatal("部分记录里应含已成功上传的引用")
	}
}

// 部分记录必须能真的被下一次复用来跳过已传文件。
func TestPartialRecordIsReusable(t *testing.T) {
	site := writeSite(t, map[string]string{"a.txt": "1", "b.txt": "2"})
	up, count := fakeTurbo(t)
	target := &Target{Repo: "r", Uploader: up, Signer: &stubSigner{}}

	// 手工造一份「只有 a.txt」的部分记录
	partial := &publish.Record{
		Target: "arweave",
		Files:  map[string]string{},
		Refs:   map[string]string{},
		Labels: map[string]string{"repo": "r"},
	}
	for _, f := range site.Files {
		if f.Path == "a.txt" {
			partial.Files[f.Path] = f.Digest
			partial.Refs[f.Path] = "existing-id"
		}
	}

	rec, err := target.Publish(context.Background(), site, partial)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Refs["a.txt"] != "existing-id" {
		t.Fatal("部分记录里的引用应被复用")
	}
	// 只剩 b.txt + manifest 要传
	if count() != 2 {
		t.Fatalf("应只上传 2 次，实际 %d", count())
	}
}

// 影响 tags 的参数变了，旧引用就不能再用：照旧复用会让未变文件沿用旧标签。
func TestPublishDoesNotReuseWhenLabelsChange(t *testing.T) {
	site := writeSite(t, map[string]string{"a.txt": "same"})

	first := &Target{Repo: "old-name", Uploader: nil, Signer: &stubSigner{}}
	up1, _ := fakeTurbo(t)
	first.Uploader = up1
	rec1, err := first.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 换仓库名后重发，内容没变
	up2, count2 := fakeTurbo(t)
	second := &Target{Repo: "new-name", Uploader: up2, Signer: &stubSigner{}}
	rec2, err := second.Publish(context.Background(), site, rec1)
	if err != nil {
		t.Fatal(err)
	}

	if rec2.Refs["a.txt"] == rec1.Refs["a.txt"] {
		t.Fatal("标签变了就不该复用旧引用")
	}
	// 文件 + manifest 都要重传
	if count2() != 2 {
		t.Fatalf("标签变化后应全量重传，实际 %d 次", count2())
	}
	if rec2.Labels["repo"] != "new-name" {
		t.Fatalf("记录应带上新的标签: %+v", rec2.Labels)
	}
}
