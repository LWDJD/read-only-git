package arweave

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// long 造一段足够长的内容。
//
// 与 stubSigner 里那道兼底是两回事：那条保证「再短也能解析」，
// 这条让站点文件足够大，贴近真实场景。
func long(s string) string { return strings.Repeat(s, 300) }

// L1 提交失败时，本轮新签的文件不能留在记录里。
//
// 这是实际踩到的 bug。L1 的 id 是本地从签名字段算出来的，
// 只代表「已打进 bundle 待提交」，不代表已上链。那笔交易一旦失败，
// 这些 id 在链上并不存在。若把它们留在记录里，下次发布会把
// 「摘要一致 + id 非空」当成已上链而跳过，结果是站点里只剩一份清单、
// 没有实际内容——而且每失败一次就多跳过几个，越来越空。
func TestL1FailureLeavesNoRefs(t *testing.T) {
	var hits int32
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("Transaction verification failed."))
	}))
	defer node.Close()

	site := writeSite(t, map[string]string{
		"index.html": long("hi"),
		"a.txt":      long("aaa"),
		"b.txt":      long("bbb"),
	})

	target := &Target{
		Repo:        "demo",
		TxSigner:    &countingTxSigner{},
		Signer:      &stubSigner{},
		Node:        node.URL,
		PendingPath: filepath.Join(site.Root, ".rog", "pending.json"),
	}

	rec, err := target.Publish(context.Background(), site, nil)
	if err == nil {
		t.Fatal("节点一直拒，发布应当失败")
	}

	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("应当真的试过提交")
	}
	if len(rec.Refs) != 0 {
		t.Fatalf("失败后不该留下任何文件引用，实际 %d 个: %v", len(rec.Refs), rec.Refs)
	}
	if len(rec.Files) != 0 {
		t.Fatalf("失败后不该留下任何摘要，实际 %d 个", len(rec.Files))
	}
	if rec.Root != "" {
		t.Fatalf("失败后不该留下入口（那个 manifest 没上链），实际 %q", rec.Root)
	}
}

// 成功时引用要在——上一条修的是「失败别留」，这一条盯着「成功别丢」。
//
// 两条一起才说明「撤除」没有撤过头。
func TestL1SuccessKeepsRefs(t *testing.T) {
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer node.Close()

	site := writeSite(t, map[string]string{
		"index.html": long("hi"),
		"a.txt":      long("aaa"),
	})

	target := &Target{
		Repo:        "demo",
		TxSigner:    &countingTxSigner{},
		Signer:      &stubSigner{},
		Node:        node.URL,
		PendingPath: filepath.Join(site.Root, ".rog", "pending.json"),
	}

	rec, err := target.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatalf("提交成功时不该报错: %v", err)
	}
	if rec.Root == "" {
		t.Fatal("成功后应当有入口")
	}
	for _, name := range []string{"index.html", "a.txt"} {
		if rec.Refs[name] == "" {
			t.Fatalf("%s 应当有引用，实际 %v", name, rec.Refs)
		}
	}
}

// 失败之后再成功：上一次撤掉的东西要能重新补上。
//
// 这才是用户实际走的路径——失败、修好、重试。
func TestL1RetryAfterFailureRefillsRefs(t *testing.T) {
	var attempts int32
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("boom"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer node.Close()

	site := writeSite(t, map[string]string{
		"index.html": long("hi"),
		"a.txt":      long("aaa"),
		"b.txt":      long("bbb"),
	})
	pendingPath := filepath.Join(site.Root, ".rog", "pending.json")

	mk := func() *Target {
		return &Target{
			Repo:        "demo",
			TxSigner:    &countingTxSigner{},
			Signer:      &stubSigner{},
			Node:        node.URL,
			PendingPath: pendingPath,
		}
	}

	rec1, err := mk().Publish(context.Background(), site, nil)
	if err == nil {
		t.Fatal("第一次应当失败")
	}
	if len(rec1.Refs) != 0 {
		t.Fatalf("第一次失败后不该留引用，实际 %v", rec1.Refs)
	}

	// 第二次会复用上个失败留下的待提交交易，直接重传同一个包
	rec2, err := mk().Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatalf("重试应当成功: %v", err)
	}
	for _, name := range []string{"index.html", "a.txt", "b.txt"} {
		if rec2.Refs[name] == "" {
			t.Fatalf("重试成功后 %s 应当有引用，实际 %v", name, rec2.Refs)
		}
	}
	if rec2.Root == "" {
		t.Fatal("重试成功后应当有入口")
	}
}
