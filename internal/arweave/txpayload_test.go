package arweave

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func sampleSig() *TxSignature {
	return &TxSignature{
		ID:        "kR6wVn5vQ0k3m1Z9t2PqYxLJH8sN4bC7dE0fG1hI2j3",
		Owner:     "owner-bytes",
		Signature: "sig-bytes",
		Reward:    "0",
		LastTx:    "",
		DataRoot:  "lRYJT9RJzpYhNAZajBAmJQSonbtkn7t79HDdiP0qfaA",
		DataSize:  "2",
	}
}

// 交易 JSON 里的 tags 必须是 base64url，不能是明文。
//
// 期望值来自 arweave-js 的实测输出（scripts/arjs-tx-shape.cjs 打印的
// toJSON()），不是猜的。这一条错了，节点会直接回 Invalid JSON；
// 而且签名也对不上，因为签名时页面里的 addTag 用的就是编码后的形式。
func TestTxPayloadEncodesTagsAsB64Url(t *testing.T) {
	raw, err := txPayload([]byte("hi"), []Tag{
		{Name: "App-Name", Value: "read-only-git"},
		{Name: "Content-Type", Value: "text/html; charset=utf-8"},
		{Name: "Path", Value: "index.html"},
	}, sampleSig())
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Format   int    `json:"format"`
		Tags     []Tag  `json:"tags"`
		Data     string `json:"data"`
		DataSize string `json:"data_size"`
		DataRoot string `json:"data_root"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("载荷应当是合法 JSON: %v", err)
	}

	// 这三对是 arweave-js 吐出来的原值
	want := []Tag{
		{Name: "QXBwLU5hbWU", Value: "cmVhZC1vbmx5LWdpdA"},
		{Name: "Q29udGVudC1UeXBl", Value: "dGV4dC9odG1sOyBjaGFyc2V0PXV0Zi04"},
		{Name: "UGF0aA", Value: "aW5kZXguaHRtbA"},
	}
	if !reflect.DeepEqual(got.Tags, want) {
		t.Fatalf("tags 应当 base64url 编码\n got %+v\nwant %+v", got.Tags, want)
	}

	if got.Data != "aGk" {
		t.Fatalf("data 应当是 base64url 无填充，实际 %q", got.Data)
	}
	if got.DataSize != "2" {
		t.Fatalf("data_size 应当是字符串，实际 %q", got.DataSize)
	}
	if got.Format != 2 {
		t.Fatalf("format 应当是数字 2，实际 %v", got.Format)
	}
	if got.DataRoot != sampleSig().DataRoot {
		t.Fatalf("data_root 应当原样带过去，实际 %q", got.DataRoot)
	}
}

// 签名回传的 tags 与明文不同源时（钱包改写过），交易 JSON 必须跟签名那份走。
//
// 这就是多块 L1 那个 400 的病灶：此前 JSON 里的 tags 是 Go 拿明文自己编码的，
// 而签名是对 setSignature 之后的 tx.tags 做的，两份不同源。
func TestTxPayloadPrefersSignatureTags(t *testing.T) {
	sig, err := fakeSignedSig(BundleTags("demo"), "0", "2")
	if err != nil {
		t.Fatal(err)
	}
	// 模拟钱包改写后的 tags（已是交易 JSON 形态）
	sig.Tags = []Tag{{Name: "QXBw", Value: "eA"}}

	out, err := txPayload([]byte("hi"), BundleTags("other-repo"), sig)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tags []Tag `json:"tags"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Tags, sig.Tags) {
		t.Fatalf("JSON 里该是签名回传的 tags，实际 %+v", got.Tags)
	}
}

// 空 tags 要输出成 []，不能是 null。
//
// tags 为 nil 时 json.Marshal 会给 null，而节点对 null 也不客气。
func TestTxPayloadEmptyTagsIsArray(t *testing.T) {
	raw, err := txPayload(nil, nil, sampleSig())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"tags":[]`)) {
		t.Fatalf("空 tags 应当是 []，实际 %s", raw)
	}
}

// 分块提交的第一步不带 data，但 data_root 与 data_size 必须在。
//
// 这两个字段是节点把后续 /chunk 挂到这笔交易上的依据。
func TestTxPayloadChunkedHeaderKeepsRootAndSize(t *testing.T) {
	raw, err := txPayload(nil, BundleTags("demo"), sampleSig())
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Data     string   `json:"data"`
		DataRoot string   `json:"data_root"`
		DataSize string   `json:"data_size"`
		Tags     []Tag    `json:"tags"`
		Fields   []string `json:"-"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	if got.Data != "" {
		t.Fatalf("不带 data 时该是空串，实际 %q", got.Data)
	}
	if got.DataRoot == "" {
		t.Fatal("data_root 不能少")
	}
	if got.DataSize == "" {
		t.Fatal("data_size 不能少")
	}
	if len(got.Tags) == 0 {
		t.Fatal("外层 bundle 的 tags 不能少")
	}
}
