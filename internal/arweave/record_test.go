package arweave

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LWDJD/read-only-git/internal/publish"
)

// 配置了 RecordPath 之后，发布记录本身也要走一遍签名与上传，
// 并写进 manifest，这样换机器才能靠入口把「路径 -> data item id」取回来。
func TestPublishUploadsRecordWhenConfigured(t *testing.T) {
	site := writeSite(t, map[string]string{
		"index.html": "<h1>hi</h1>",
		"a.txt":      "hello",
	})

	up, count := fakeTurbo(t)
	signer := &stubSigner{}
	rel := publish.RecordRelPath("arweave", "demo")
	target := &Target{Repo: "demo", Uploader: up, Signer: signer, RecordPath: rel}

	rec, err := target.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 2 个内容文件 + 1 份发布记录 + 1 份 manifest
	if got := count(); got != 4 {
		t.Fatalf("应上传 4 次（含记录），实际 %d", got)
	}
	if signer.count() != 4 {
		t.Fatalf("签名次数应与上传一致，实际 %d", signer.count())
	}
	if rec.Root == "" {
		t.Fatal("本地记录的 Root 应指向入口")
	}
	// 记录文件是发布产物，不是站点内容，不该混进内容引用里
	if _, ok := rec.Refs[rel]; ok {
		t.Fatalf("记录文件不该出现在内容引用里: %+v", rec.Refs)
	}
}

// 没配 RecordPath 时保持原行为，不多传也不多签。
func TestPublishSkipsRecordWhenNotConfigured(t *testing.T) {
	site := writeSite(t, map[string]string{"a.txt": "hello"})

	up, count := fakeTurbo(t)
	signer := &stubSigner{}
	target := &Target{Repo: "demo", Uploader: up, Signer: signer}

	if _, err := target.Publish(context.Background(), site, nil); err != nil {
		t.Fatal(err)
	}

	// 1 个内容文件 + 1 份 manifest
	if got := count(); got != 2 {
		t.Fatalf("应上传 2 次，实际 %d", got)
	}
	if signer.count() != 2 {
		t.Fatalf("签名次数应与上传一致，实际 %d", signer.count())
	}
}

// 链上那份记录的 Root 是空的，取回时按调用方给的入口 id 补上。
func TestFetchRecordRestoresRefsAndRoot(t *testing.T) {
	rel := publish.RecordRelPath("arweave", "demo")
	stored := &publish.Record{
		Target: "arweave",
		Root:   "", // 链上那份就是空的
		Files:  map[string]string{"index.html": "digest-a"},
		Refs:   map[string]string{"index.html": "id-1"},
		Labels: map[string]string{"repo": "demo"},
	}
	body, err := publish.MarshalRecord(stored)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/entry-id/"+rel {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, err := FetchRecord(context.Background(), srv.URL, "entry-id", rel)
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != "entry-id" {
		t.Fatalf("Root 应补成入口 id，实际 %q", got.Root)
	}
	if got.Refs["index.html"] != "id-1" {
		t.Fatalf("引用没取回来: %+v", got.Refs)
	}
	if got.Files["index.html"] != "digest-a" {
		t.Fatalf("摘要没取回来: %+v", got.Files)
	}
	if got.Labels["repo"] != "demo" {
		t.Fatalf("标签没取回来: %+v", got.Labels)
	}
}

// 入口还没被网关索引时，错误信息要指出这一点，否则只看到 404 会以为是路径写错。
func TestFetchRecordReportsUnindexedEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := FetchRecord(context.Background(), srv.URL, "entry-id", "some/record.json")
	if err == nil {
		t.Fatal("404 时应当报错")
	}
	if !strings.Contains(err.Error(), "索引") {
		t.Fatalf("错误信息应提示可能还没索引，实际: %v", err)
	}
}

// 取回空记录时不该把 nil map 漏出去，后续复用判断要能安全读。
func TestFetchRecordNormalizesEmptyMaps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"target":"arweave"}`))
	}))
	defer srv.Close()

	got, err := FetchRecord(context.Background(), srv.URL, "entry-id", "p.json")
	if err != nil {
		t.Fatal(err)
	}
	if got.Files == nil || got.Refs == nil {
		t.Fatalf("空记录也要给出可读的空映射: %+v", got)
	}
	if len(got.Refs) != 0 {
		t.Fatalf("不该凭空多出引用: %+v", got.Refs)
	}
}

// 完整闭环：本地记录丢了，从链上取回来，接着做增量发布。
// 内容没变的文件应当全部复用，只重新上传记录与 manifest。
func TestFetchedRecordFeedsIncrementalPublish(t *testing.T) {
	site := writeSite(t, map[string]string{
		"index.html": "<h1>hi</h1>",
		"a.txt":      "hello",
	})
	rel := publish.RecordRelPath("arweave", "demo")

	// 第一次发布：内容文件 + 记录 + manifest
	up1, count1 := fakeTurbo(t)
	target1 := &Target{Repo: "demo", Uploader: up1, Signer: &stubSigner{}, RecordPath: rel}
	first, err := target1.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}
	if count1() != 4 {
		t.Fatalf("首次应上传 4 次，实际 %d", count1())
	}

	// 把记录放到假网关上，模拟它已经随站点上了链（链上那份 Root 是空的）
	stored, err := publish.MarshalRecord(&publish.Record{
		Target: first.Target,
		Files:  first.Files,
		Refs:   first.Refs,
		Labels: first.Labels,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/entry-1/"+rel {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(stored)
	}))
	defer srv.Close()

	// 本地那份不要了，只从链上取回来
	restored, err := FetchRecord(context.Background(), srv.URL, "entry-1", rel)
	if err != nil {
		t.Fatal(err)
	}

	// 用恢复出来的记录再发一次
	up2, count2 := fakeTurbo(t)
	target2 := &Target{Repo: "demo", Uploader: up2, Signer: &stubSigner{}, RecordPath: rel}
	second, err := target2.Publish(context.Background(), site, restored)
	if err != nil {
		t.Fatal(err)
	}

	// 2 个内容文件全部复用，加上记录与 manifest
	if got := count2(); got != 2 {
		t.Fatalf("内容未变时应只传记录与 manifest，实际传了 %d 次", got)
	}
	if second.Refs["index.html"] != first.Refs["index.html"] {
		t.Fatal("复用后引用的 id 应当保持不变")
	}
	if second.Refs["a.txt"] != first.Refs["a.txt"] {
		t.Fatal("复用后引用的 id 应当保持不变")
	}
}
