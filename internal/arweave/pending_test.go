package arweave

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/LWDJD/read-only-git/internal/publish"
)

// 签好的交易要能落盘并读回：这是「重试不必重新签名」的地基。
func TestPendingRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")

	bundle := []byte("bundle-bytes")
	tags := []Tag{{Name: "App-Name", Value: "read-only-git"}}
	sig := &TxSignature{
		ID: "the-id", Owner: "the-owner", Signature: "the-sig",
		Reward: "0", LastTx: "lt", DataRoot: "root", DataSize: "12",
	}

	if err := SavePending(path, bundle, tags, sig); err != nil {
		t.Fatal(err)
	}

	p := LoadPending(path)
	if p == nil {
		t.Fatal("应当能读回来")
	}
	if !p.SameBundle(bundle) {
		t.Fatal("包体应当一致")
	}
	if p.Sig == nil || p.Sig.ID != "the-id" || p.Sig.DataRoot != "root" {
		t.Fatalf("签名字段没存全: %+v", p.Sig)
	}
	if !reflect.DeepEqual(p.Tags, tags) {
		t.Fatalf("tags 应当用明文存回来，实际 %+v", p.Tags)
	}

	ClearPending(path)
	if LoadPending(path) != nil {
		t.Fatal("清掉之后不该还能读到")
	}
}

// 包体变了，旧签名就不能再用。
//
// 不检查这个的后果很隐蔽：站点内容改了，却把新包配上旧签名提交，
// 节点只会回一句验证失败，看不出真正的原因。
func TestPendingRejectsDifferentBundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	if err := SavePending(path, []byte("original"), nil, &TxSignature{ID: "x"}); err != nil {
		t.Fatal(err)
	}

	p := LoadPending(path)
	if p == nil {
		t.Fatal("应当能读回来")
	}
	if p.SameBundle([]byte("changed!")) {
		t.Fatal("包体不同不该判为同一个")
	}
	if p.SameBundle([]byte("short")) {
		t.Fatal("长度不同也不该算同一个")
	}
	if !p.SameBundle([]byte("original")) {
		t.Fatal("包体相同应当判为同一个")
	}
}

// 损坏的记录当作没有，而不是报错。
//
// 这只是一份加速用的缓存，丢了顶多让用户多点一次钱包，
// 不该因此把整条发布拦下来。
func TestLoadPendingIgnoresBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	if err := os.WriteFile(path, []byte("{ 这不是 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	if LoadPending(path) != nil {
		t.Fatal("损坏的文件应当被当作没有")
	}
	if LoadPending(filepath.Join(t.TempDir(), "not-there.json")) != nil {
		t.Fatal("不存在的文件应当返回 nil")
	}
}

// 空路径表示不落盘，各项操作都应当是安全的空操作。
func TestPendingEmptyPathIsNoop(t *testing.T) {
	if err := SavePending("", nil, nil, nil); err != nil {
		t.Fatalf("空路径不该报错: %v", err)
	}
	if LoadPending("") != nil {
		t.Fatal("空路径应当返回 nil")
	}
	ClearPending("") // 不该 panic
}

// 落盘的是可解析的 JSON，bundle 走标准库的 base64。
func TestPendingFileShapeIsJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	if err := SavePending(path, []byte{1, 2, 3}, []Tag{{Name: "A", Value: "B"}}, &TxSignature{ID: "x"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("应当是可解析的 JSON: %v", err)
	}
	for _, k := range []string{"bundle", "sig", "tags", "at"} {
		if _, ok := probe[k]; !ok {
			t.Fatalf("缺少字段 %s", k)
		}
	}
}

// countingTxSigner 数签名次数，并回一份固定的签名字段。
type countingTxSigner struct {
	calls atomic.Int32
}

func (s *countingTxSigner) SignTx(ctx context.Context, data []byte, tags []Tag) (*TxSignature, error) {
	s.calls.Add(1)
	sig, err := fakeSignedSig(tags, "0", "999")
	if err != nil {
		return nil, err
	}
	// 单块：走 /tx 带 data，不必真的分块
	sig.Proofs = []ChunkProof{{DataPath: "path", Offset: "998"}}
	return sig, nil
}

// 提交失败 → 留下待提交交易 → 重试时直接用，不再碰签名通道。
//
// 这是这一项功能的验收点：签名是用户在钱包里点过确认的动作，
// 一次网络失败不该让它作废。countingTxSigner 数着被调用的次数，
// 第二次提交它不该再动。
func TestL1ReusesPendingSignatureOnRetry(t *testing.T) {
	siteRoot := t.TempDir()
	pendingPath := publish.PendingPath(siteRoot, "arweave", "demo")

	var attempts int32
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			// 第一次让节点拒掉，模拟签名对不上或节点侧失败
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("Transaction verification failed."))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer node.Close()

	signer := &countingTxSigner{}
	// 假的 data item 也要够长：Bundle 要按签名段算 id，
	// 短于 514 字节的根本拼不出合法 bundle。
	items := [][]byte{makeDataItem(0x11, 520), makeDataItem(0x22, 600)}

	mkTarget := func() *Target {
		return &Target{
			Repo:        "demo",
			TxSigner:    signer,
			Node:        node.URL,
			PendingPath: pendingPath,
		}
	}

	// 第一次：失败，但签名已经落下
	if err := mkTarget().submitL1(context.Background(), items, "entry-id", 0); err == nil {
		t.Fatal("第一次提交应当失败")
	}
	if n := signer.calls.Load(); n != 1 {
		t.Fatalf("第一次应当签一次，实际 %d", n)
	}
	if LoadPending(pendingPath) == nil {
		t.Fatal("失败之后应当留下一份待提交交易")
	}

	// 第二次：同一个包体，应当复用签名直接重传
	if err := mkTarget().submitL1(context.Background(), items, "entry-id", 0); err != nil {
		t.Fatalf("重试应当成功: %v", err)
	}
	if n := signer.calls.Load(); n != 1 {
		t.Fatalf("重试不该再签名，签名次数应当还是 1，实际 %d", n)
	}
	if LoadPending(pendingPath) != nil {
		t.Fatal("提交成功后应当清掉待提交记录")
	}
}

// 站点内容变了，就不能拿旧签名去提交。
//
// 这条与「复用」是一体两面：复用只在包体一致时成立。
func TestL1DoesNotReuseStaleSignature(t *testing.T) {
	siteRoot := t.TempDir()
	pendingPath := publish.PendingPath(siteRoot, "arweave", "demo")

	// 先放一份「上一次」的待提交记录，包体与这次不同
	if err := SavePending(pendingPath, []byte("old-bundle"), nil, &TxSignature{ID: "old"}); err != nil {
		t.Fatal(err)
	}

	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer node.Close()

	signer := &countingTxSigner{}
	target := &Target{
		Repo:        "demo",
		TxSigner:    signer,
		Node:        node.URL,
		PendingPath: pendingPath,
	}

	if err := target.submitL1(context.Background(), [][]byte{makeDataItem(0x33, 520)}, "entry-id", 0); err != nil {
		t.Fatalf("提交应当成功: %v", err)
	}
	if n := signer.calls.Load(); n != 1 {
		t.Fatalf("包体不同时必须重新签名，实际签了 %d 次", n)
	}
}
