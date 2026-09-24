package arweave

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LWDJD/read-only-git/internal/publish"
)

// richStubSigner 产出的「签名结果」长度符合 data item 的头部布局，
// 这样 DataItemID 才能从里面取到签名段。签名值随内容变化，
// 不同文件才会得到不同的 id，否则复用判断会被假数据掩盖。
type richStubSigner struct {
	mu    sync.Mutex
	calls int
}

func (s *richStubSigner) Sign(ctx context.Context, data []byte, tags []Tag) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++

	sig := make([]byte, dataItemSignatureSize)
	sum := sha256.Sum256(data)
	copy(sig, sum[:])

	out := make([]byte, 0, dataItemSignatureTypeSize+len(sig)+16+len(data))
	out = append(out, 0x02, 0x00) // 签名类型：RSA-4096
	out = append(out, sig...)
	out = append(out, make([]byte, 16)...) // owner 的占位
	return append(out, data...), nil
}

func (s *richStubSigner) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// stubTxSigner 记录每次被要求签的交易，以及提交给它的数据。
type stubTxSigner struct {
	mu      sync.Mutex
	bundles [][]byte
	tagSets [][]Tag
}

func (s *stubTxSigner) SignTx(ctx context.Context, data []byte, tags []Tag) (*TxSignature, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bundles = append(s.bundles, append([]byte(nil), data...))
	s.tagSets = append(s.tagSets, tags)
	return &TxSignature{
		ID:        "tx-id",
		Owner:     "owner",
		Signature: "sig",
		Reward:    "1000",
		LastTx:    "anchor",
		DataRoot:  "stub-root",
	}, nil
}

func (s *stubTxSigner) bundleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bundles)
}

// fakeNode 收下提交上来的交易，把请求体留给人看。
func fakeNode(t *testing.T) (string, func() map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var last map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tx" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		last = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"tx-id"}`))
	}))
	t.Cleanup(srv.Close)

	return srv.URL, func() map[string]any {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// L1 模式下所有新签的 data item 应当只凑成一笔交易提交，
// 而不是每个文件各发一次。
func TestL1PublishSubmitsSingleTransaction(t *testing.T) {
	site := writeSite(t, map[string]string{
		"index.html": "<h1>hi</h1>",
		"a.txt":      "hello",
	})

	nodeURL, lastPosted := fakeNode(t)
	signer := &richStubSigner{}
	txSigner := &stubTxSigner{}
	rel := publish.RecordRelPath("arweave", "demo")

	target := &Target{
		Repo:       "demo",
		Signer:     signer,
		TxSigner:   txSigner,
		Node:       nodeURL,
		RecordPath: rel,
	}

	rec, err := target.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 2 个内容 + 1 份记录 + 1 份 manifest
	if signer.count() != 4 {
		t.Fatalf("应当签 4 份 data item，实际 %d", signer.count())
	}
	if txSigner.bundleCount() != 1 {
		t.Fatalf("应当只签一笔交易，实际 %d", txSigner.bundleCount())
	}

	posted := lastPosted()
	if posted == nil {
		t.Fatal("节点没收到任何交易")
	}

	// 提交上去的 data 必须就是签过的那份 bundle，一字不差，
	// 否则节点算出的 data_root 与签名对不上。
	encoded, _ := posted["data"].(string)
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, txSigner.bundles[0]) {
		t.Fatal("提交的 data 应当与签名的 bundle 完全一致")
	}

	// 标了标签，网关才知道这是一整包
	tags := txSigner.tagSets[0]
	seen := map[string]string{}
	for _, tag := range tags {
		seen[tag.Name] = tag.Value
	}
	if seen[BundleFormatTag] != BundleFormat || seen[BundleVersionTag] != BundleVersion {
		t.Fatalf("外层交易缺少 bundle 标记: %+v", tags)
	}

	// 记录文件不进内容引用，但入口要有值
	if _, ok := rec.Refs[rel]; ok {
		t.Fatalf("记录文件不该出现在内容引用里: %+v", rec.Refs)
	}
	if rec.Root == "" {
		t.Fatal("入口应当有值")
	}
	if rec.Refs["index.html"] == "" || rec.Refs["a.txt"] == "" {
		t.Fatalf("内容引用应当齐全: %+v", rec.Refs)
	}
}

// 第二次发布内容没变时，内容文件不该重签，只有记录与 manifest 会重新签。
func TestL1PublishReusesUnchangedFiles(t *testing.T) {
	site := writeSite(t, map[string]string{
		"index.html": "<h1>hi</h1>",
		"a.txt":      "hello",
	})
	rel := publish.RecordRelPath("arweave", "demo")

	nodeURL, _ := fakeNode(t)
	first := &richStubSigner{}
	firstTarget := &Target{
		Repo: "demo", Signer: first, TxSigner: &stubTxSigner{},
		Node: nodeURL, RecordPath: rel,
	}
	rec1, err := firstTarget.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.count() != 4 {
		t.Fatalf("首次应当签 4 份，实际 %d", first.count())
	}

	// 拿着上一次的记录再发一次，内容一字未改
	second := &richStubSigner{}
	secondTx := &stubTxSigner{}
	secondTarget := &Target{
		Repo: "demo", Signer: second, TxSigner: secondTx,
		Node: nodeURL, RecordPath: rel,
	}
	rec2, err := secondTarget.Publish(context.Background(), site, rec1)
	if err != nil {
		t.Fatal(err)
	}

	// 只重签记录与 manifest，两个内容文件复用
	if second.count() != 2 {
		t.Fatalf("内容未变时应当只签 2 份（记录与 manifest），实际 %d", second.count())
	}
	if secondTx.bundleCount() != 1 {
		t.Fatalf("应当仍然只发一笔交易，实际 %d", secondTx.bundleCount())
	}

	// 复用意味着 id 保持一致，也就意味着没有重复付费
	if rec2.Refs["index.html"] != rec1.Refs["index.html"] {
		t.Fatal("复用后内容引用的 id 应当不变")
	}
	if rec2.Refs["a.txt"] != rec1.Refs["a.txt"] {
		t.Fatal("复用后内容引用的 id 应当不变")
	}
}

// id 从签名段算出来，同一个签名字节必然得到同一个 id；
// 换个签名结果，id 必须跟着变。
func TestDataItemIDIsStableAndSensitive(t *testing.T) {
	makeItem := func(fill byte) []byte {
		out := make([]byte, dataItemSignatureTypeSize+dataItemSignatureSize+8)
		out[0], out[1] = 0x02, 0x00
		for i := dataItemSignatureTypeSize; i < dataItemSignatureTypeSize+dataItemSignatureSize; i++ {
			out[i] = fill
		}
		return out
	}

	a1, err := DataItemID(makeItem(0xAB))
	if err != nil {
		t.Fatal(err)
	}
	a2, err := DataItemID(makeItem(0xAB))
	if err != nil {
		t.Fatal(err)
	}
	if a1 != a2 {
		t.Fatal("同一份签名应当得到同一个 id")
	}

	b, err := DataItemID(makeItem(0xCD))
	if err != nil {
		t.Fatal(err)
	}
	if a1 == b {
		t.Fatal("签名不同时 id 必须不同")
	}

	// base64url 的 32 字节固定是 43 个字符
	if len(a1) != 43 {
		t.Fatalf("id 长度应为 43，实际 %d: %q", len(a1), a1)
	}
	if strings.ContainsAny(a1, "+/=") {
		t.Fatalf("id 用的是 base64url，不该含 + / =: %q", a1)
	}
}

// 太短的输入要报错，而不是从越界的切片里算出个假 id。
func TestDataItemIDRejectsShortInput(t *testing.T) {
	if _, err := DataItemID([]byte{1, 2, 3}); err == nil {
		t.Fatal("放不下签名段的输入应当报错")
	}
}

// L1 模式下超过单块上限、却没有分块证明时要报错，不能硬发。
//
// 这个桩签名器只回签名字段、不带 proof，正是「页面版本旧了」的形状。
func TestL1PublishRejectsOversizeWithoutProofs(t *testing.T) {
	// 一个稍大的文件就足以把 bundle 推过单块上限
	big := strings.Repeat("x", BundleLimit+1024)
	site := writeSite(t, map[string]string{"big.txt": big})

	nodeURL, _ := fakeNode(t)
	txSigner := &stubTxSigner{}
	target := &Target{
		Repo: "demo", Signer: &richStubSigner{}, TxSigner: txSigner, Node: nodeURL,
	}

	_, err := target.Publish(context.Background(), site, nil)
	if err == nil {
		t.Fatal("超过单块上限却没有分块证明，应当报错而不是硬发")
	}
	if !strings.Contains(err.Error(), "分块") {
		t.Fatalf("错误信息应点明分块: %v", err)
	}
}

// 有分块证明时，超过一块的 bundle 应当走分块提交：
// 交易先报（不带 data），内容再逐块补。
func TestL1PublishChunksLargeBundle(t *testing.T) {
	big := strings.Repeat("A", 200*1024)
	site := writeSite(t, map[string]string{
		"index.html": "<h1>hi</h1>",
		"a.bin":      big,
		"b.bin":      big,
	})

	node := &chunkNode{}
	srv := httptest.NewServer(node.handler())
	defer srv.Close()

	target := &Target{
		Repo:     "demo",
		Signer:   &richStubSigner{},
		TxSigner: &chunkingTxSigner{},
		Node:     srv.URL,
	}

	rec, err := target.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Root == "" {
		t.Fatal("应当有入口")
	}

	txs, chunks := node.snapshot()
	if len(txs) != 1 {
		t.Fatalf("应当只报 1 笔交易，实际 %d", len(txs))
	}
	if txs[0]["data"] != "" {
		t.Fatal("分块时交易不该带 data")
	}
	if len(chunks) < 2 {
		t.Fatalf("这么大的 bundle 应当分多块提交，实际 %d 块", len(chunks))
	}
	for i, c := range chunks {
		if c["data_root"] == "" {
			t.Fatalf("第 %d 块缺 data_root", i)
		}
	}
}

// uploadedStubSigner 模拟「页面已经自己提交了」的那一类回传。
type uploadedStubSigner struct {
	sig *TxSignature
}

func (s *uploadedStubSigner) SignTx(context.Context, []byte, []Tag) (*TxSignature, error) {
	return s.sig, nil
}

// 页面提交之后，日志要把 POST 的状态与节点原话都记下来。
//
// 线上撞过「POST 说受理、事后查不到」，那时日志里只有事后状态，
// 看不出节点当时到底回了什么，也就无从定位。这条钉住两样都要有。
func TestL1LogsPostStatusAndBody(t *testing.T) {
	var lines []string
	target := &Target{
		Repo: "demo",
		TxSigner: &uploadedStubSigner{sig: &TxSignature{
			ID: "tx-abc", Uploaded: true,
			PostStatus: 202, Status: 404,
			Reward: "3300994621", PostBody: "some node message",
		}},
		Logf: func(format string, args ...any) {
			lines = append(lines, fmt.Sprintf(format, args...))
		},
	}
	if err := target.submitL1(context.Background(), [][]byte{makeDataItem(0x44, 520)}, "root-id", 0); err != nil {
		t.Fatalf("提交失败: %v", err)
	}

	joined := strings.Join(lines, "\n")
	for _, want := range []string{"POST 202", "事后状态 404", "some node message", "3300994621"} {
		if !strings.Contains(joined, want) {
			t.Errorf("日志里应当有 %q，实际：\n%s", want, joined)
		}
	}
}

// 事后 404 时要额外提醒一句：刚提交时 404 正常，久了就是没留住。
func TestL1WarnsOnStaleStatus(t *testing.T) {
	var lines []string
	target := &Target{
		Repo: "demo",
		TxSigner: &uploadedStubSigner{sig: &TxSignature{
			ID: "tx-abc", Uploaded: true, PostStatus: 200, Status: 404, Reward: "1",
		}},
		Logf: func(format string, args ...any) {
			lines = append(lines, fmt.Sprintf(format, args...))
		},
	}
	if err := target.submitL1(context.Background(), [][]byte{makeDataItem(0x55, 520)}, "root-id", 0); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "没被节点留住") {
		t.Errorf("事后 404 时应当提醒一句，实际：\n%s", strings.Join(lines, "\n"))
	}
}
