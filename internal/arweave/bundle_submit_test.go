package arweave

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// chunkingTxSigner 模拟「页面已经算好分块证明」的情形。
//
// 它按单块上限切出 offsets，证明内容本身是占位：
// 这一层测的是 Go 拿到 proofs 后怎么切、怎么发，不是 Merkle 算得对不对。
type chunkingTxSigner struct {
	mu      sync.Mutex
	bundles [][]byte
}

func (s *chunkingTxSigner) SignTx(ctx context.Context, data []byte, tags []Tag) (*TxSignature, error) {
	s.mu.Lock()
	s.bundles = append(s.bundles, append([]byte(nil), data...))
	s.mu.Unlock()

	var proofs []ChunkProof
	for end := MaxChunkSize; end < len(data); end += MaxChunkSize {
		proofs = append(proofs, ChunkProof{DataPath: "p", Offset: strconv.Itoa(end - 1)})
	}
	proofs = append(proofs, ChunkProof{DataPath: "p", Offset: strconv.Itoa(len(data) - 1)})

	sig, err := fakeSignedSig(tags, "0", strconv.Itoa(len(data)))
	if err != nil {
		return nil, err
	}
	sig.Proofs = proofs
	return sig, nil
}

func (s *chunkingTxSigner) bundleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bundles)
}

// chunkNode 假扮 Arweave 节点，把收到的请求记下来供断言。
//
// 与 l1_target_test.go 里那个同名意思的辅助不同，这里要同时看住 /tx 与 /chunk，
// 所以做成结构体而不是一次性闭包。
//
// chunkFail 控制前几次 /chunk 返回 503（可重试），
// chunkFatal 非空时 /chunk 一律返回它（模拟致命错）。
type chunkNode struct {
	mu         sync.Mutex
	txs        []map[string]any
	chunks     []map[string]any
	chunkFail  int
	chunkFatal string
}

func (f *chunkNode) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tx":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.txs = append(f.txs, body)
			f.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"tx"}`))

		case "/chunk":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)

			f.mu.Lock()
			f.chunks = append(f.chunks, body)
			fatal := f.chunkFatal
			fail := f.chunkFail > 0
			if fail {
				f.chunkFail--
			}
			f.mu.Unlock()

			if fatal != "" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"` + fatal + `"}`))
				return
			}
			if fail {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"error":"timeout"}`))
				return
			}
			w.WriteHeader(http.StatusOK)

		default:
			http.NotFound(w, r)
		}
	}
}

func (f *chunkNode) snapshot() (txs, chunks []map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.txs...), append([]map[string]any(nil), f.chunks...)
}

// fastBackoff 把退避调小，不然跑一次重试用例要等好几秒。
func fastBackoff(t *testing.T) {
	t.Helper()
	old := chunkBackoffBase
	chunkBackoffBase = time.Millisecond
	t.Cleanup(func() { chunkBackoffBase = old })
}

// 装得下的时候不该碰分块，data 直接进交易。
func TestSubmitBundleSingleChunkSkipsChunking(t *testing.T) {
	data := []byte("small payload")
	sig, err := fakeSignedSig(BundleTags("demo"), "0", "13")
	if err != nil {
		t.Fatal(err)
	}

	node := &chunkNode{}
	srv := httptest.NewServer(node.handler())
	defer srv.Close()

	id, err := SubmitBundle(context.Background(), srv.URL, data, BundleTags("demo"), sig, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id != sig.ID {
		t.Fatalf("应返回交易 id，实际 %q", id)
	}

	txs, chunks := node.snapshot()
	if len(chunks) != 0 {
		t.Fatalf("单块不该发 /chunk，实际发了 %d 次", len(chunks))
	}
	if len(txs) != 1 {
		t.Fatalf("应当只发 1 笔交易，实际 %d", len(txs))
	}
	if txs[0]["data"] != base64.RawURLEncoding.EncodeToString(data) {
		t.Fatal("单块时 data 应当装在交易里")
	}
	// 没给 data_size 时应当用实际长度兜底
	if txs[0]["data_size"] != "13" {
		t.Fatalf("data_size 应当兜底为 13，实际 %#v", txs[0]["data_size"])
	}
}

// 多块：交易先报，之后逐块补，交易本体不带 data。
//
// offsets 取自真 arweave-js，块内容按同样边界切。
func TestSubmitBundleChunked(t *testing.T) {
	const size = 300000
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i & 0xff)
	}

	sig, err := fakeSignedSig(BundleTags("demo"), "0", "300000")
	if err != nil {
		t.Fatal(err)
	}
	sig.Proofs = []ChunkProof{
		{DataPath: "p0", Offset: "262143"},
		{DataPath: "p1", Offset: "299999"},
	}
	root := sig.DataRoot

	node := &chunkNode{}
	srv := httptest.NewServer(node.handler())
	defer srv.Close()

	id, err := SubmitBundle(context.Background(), srv.URL, data, BundleTags("demo"), sig, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id != sig.ID {
		t.Fatalf("应返回交易 id，实际 %q", id)
	}

	txs, chunks := node.snapshot()
	if len(txs) != 1 {
		t.Fatalf("应当只报 1 笔交易，实际 %d", len(txs))
	}
	if txs[0]["data"] != "" {
		t.Fatalf("分块时交易不该带 data，实际 %v", txs[0]["data"])
	}
	if txs[0]["data_root"] != root {
		t.Fatalf("交易应带 data_root，实际 %#v", txs[0]["data_root"])
	}
	if txs[0]["data_size"] != "300000" {
		t.Fatalf("交易应带 data_size，实际 %#v", txs[0]["data_size"])
	}

	if len(chunks) != 2 {
		t.Fatalf("应当提交 2 块，实际 %d", len(chunks))
	}

	// 第 0 块：0..262144
	if chunks[0]["offset"] != "262143" || chunks[0]["data_path"] != "p0" {
		t.Fatalf("第 0 块的 offset / data_path 不对: %+v", chunks[0])
	}
	if want := base64.RawURLEncoding.EncodeToString(data[0:262144]); chunks[0]["chunk"] != want {
		t.Fatal("第 0 块内容与 bundle 的对应区间不一致")
	}
	if chunks[0]["data_root"] != root || chunks[0]["data_size"] != "300000" {
		t.Fatalf("每块都该带上 data_root 与 data_size: %+v", chunks[0])
	}

	// 第 1 块：262144..300000
	if chunks[1]["offset"] != "299999" || chunks[1]["data_path"] != "p1" {
		t.Fatalf("第 1 块的 offset / data_path 不对: %+v", chunks[1])
	}
	if want := base64.RawURLEncoding.EncodeToString(data[262144:300000]); chunks[1]["chunk"] != want {
		t.Fatal("第 1 块内容与 bundle 的对应区间不一致")
	}
}

// 5xx 值得重试：第一次失败，第二次应当成功，最终不算失败。
func TestSubmitBundleRetriesRetryableChunkError(t *testing.T) {
	fastBackoff(t)

	data := make([]byte, 300000)
	sig, err := fakeSignedSig(nil, "0", "300000")
	if err != nil {
		t.Fatal(err)
	}
	sig.Proofs = []ChunkProof{
		{DataPath: "p0", Offset: "262143"},
		{DataPath: "p1", Offset: "299999"},
	}

	node := &chunkNode{chunkFail: 1}
	srv := httptest.NewServer(node.handler())
	defer srv.Close()

	if _, err := SubmitBundle(context.Background(), srv.URL, data, nil, sig, nil, nil); err != nil {
		t.Fatalf("可重试的失败不该让整次提交失败: %v", err)
	}

	_, chunks := node.snapshot()
	// 第一次 503、第二次成功，第一块共发两次，加上第二块，合计 3 次
	if len(chunks) != 3 {
		t.Fatalf("应当重试一次后继续，实际发了 %d 次 /chunk", len(chunks))
	}
}

// 致命错不该反复重试：invalid_proof 重发多少次结果都一样。
func TestSubmitBundleDoesNotRetryFatalChunkError(t *testing.T) {
	fastBackoff(t)

	data := make([]byte, 300000)
	sig, err := fakeSignedSig(nil, "0", "300000")
	if err != nil {
		t.Fatal(err)
	}
	sig.Proofs = []ChunkProof{
		{DataPath: "p0", Offset: "262143"},
		{DataPath: "p1", Offset: "299999"},
	}

	node := &chunkNode{chunkFatal: "invalid_proof"}
	srv := httptest.NewServer(node.handler())
	defer srv.Close()

	_, err = SubmitBundle(context.Background(), srv.URL, data, nil, sig, nil, nil)
	if err == nil {
		t.Fatal("致命错应当报出来")
	}
	if !strings.Contains(err.Error(), "invalid_proof") {
		t.Fatalf("错误信息应带上节点的原话: %v", err)
	}

	_, chunks := node.snapshot()
	if len(chunks) != 1 {
		t.Fatalf("致命错只该试一次，实际发了 %d 次", len(chunks))
	}
}

// proof 与数据对不上时要在发请求之前就拦下。
func TestSubmitBundleRejectsMismatchedProofs(t *testing.T) {
	data := make([]byte, 100)
	sig, err := fakeSignedSig(nil, "0", "100")
	if err != nil {
		t.Fatal(err)
	}
	sig.Proofs = []ChunkProof{
		{DataPath: "p0", Offset: "50"},
		{DataPath: "p1", Offset: "60"}, // 只覆盖到 61 字节
	}

	node := &chunkNode{}
	srv := httptest.NewServer(node.handler())
	defer srv.Close()

	if _, err := SubmitBundle(context.Background(), srv.URL, data, nil, sig, nil, nil); err == nil {
		t.Fatal("覆盖率对不上应当报错")
	}

	txs, chunks := node.snapshot()
	if len(txs) != 0 || len(chunks) != 0 {
		t.Fatal("切块失败时不该发出任何请求")
	}
}
