package arweave

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBundleConcatenatesInOrder(t *testing.T) {
	got := Bundle([][]byte{{1, 2}, {3}, {4, 5, 6}})
	want := []byte{1, 2, 3, 4, 5, 6}
	if !bytes.Equal(got, want) {
		t.Fatalf("拼接结果不对: %v", got)
	}
}

// 空输入不该 panic，也不该凭空多出字节。
func TestBundleEmpty(t *testing.T) {
	if got := Bundle(nil); len(got) != 0 {
		t.Fatalf("空输入应得空结果，实际 %v", got)
	}
	if got := Bundle([][]byte{}); len(got) != 0 {
		t.Fatalf("空切片应得空结果，实际 %v", got)
	}
}

// 拼接不能改动传入的切片，否则调用方手里的 data item 会被悄悄改掉。
func TestBundleDoesNotMutateInput(t *testing.T) {
	item := []byte{9, 9, 9}
	out := Bundle([][]byte{item})
	out[0] = 0
	if item[0] != 9 {
		t.Fatal("Bundle 不该改动传入的切片")
	}
}

func TestCheckBundleSizeBoundary(t *testing.T) {
	if err := CheckBundleSize(BundleLimit); err != nil {
		t.Fatalf("正好等于上限应当放行: %v", err)
	}
	err := CheckBundleSize(BundleLimit + 1)
	if err == nil {
		t.Fatal("超出上限应当报错")
	}
	if !strings.Contains(err.Error(), "分块") {
		t.Fatalf("错误信息应提到分块，实际: %v", err)
	}
}

func TestSubmitTxPostsTransactionJSON(t *testing.T) {
	var got map[string]any
	var contentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tx" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		contentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"tx-id"}`))
	}))
	defer srv.Close()

	bundle := []byte("BUNDLEBYTES")
	sig := &TxSignature{
		ID:        "tx-id",
		Owner:     "owner-b64",
		Signature: "sig-b64",
		Reward:    "12345",
		LastTx:    "anchor-1",
	}

	id, err := SubmitTx(context.Background(), srv.URL, bundle, BundleTags("demo"), sig, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id != "tx-id" {
		t.Fatalf("应返回交易 id，实际 %q", id)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("提交格式应当是 JSON，实际 %q", contentType)
	}

	if got["format"] != float64(2) {
		t.Fatalf("format 应为 2，实际 %v", got["format"])
	}
	if got["data"] != base64.RawURLEncoding.EncodeToString(bundle) {
		t.Fatalf("data 应是 bundle 的 base64url，实际 %v", got["data"])
	}
	// data_size 在 Arweave 的交易 JSON 里是字符串，写成数字节点会拒
	if got["data_size"] != "11" {
		t.Fatalf("data_size 应为字符串 \"11\"，实际 %#v", got["data_size"])
	}
	if got["quantity"] != "0" {
		t.Fatalf("quantity 应为 \"0\"，实际 %#v", got["quantity"])
	}
	if got["target"] != "" {
		t.Fatalf("target 应为空串，实际 %#v", got["target"])
	}
	if got["reward"] != "12345" {
		t.Fatalf("reward 应原样带上，实际 %#v", got["reward"])
	}
	if got["last_tx"] != "anchor-1" {
		t.Fatalf("last_tx 应原样带上，实际 %#v", got["last_tx"])
	}
	if got["owner"] != "owner-b64" || got["signature"] != "sig-b64" || got["id"] != "tx-id" {
		t.Fatalf("签名相关字段应原样带上: %+v", got)
	}
}

// 外层交易必须带上 Bundle-Format 与 Bundle-Version，否则网关不认它是 bundle。
func TestBundleTagsMarkItAsBundle(t *testing.T) {
	tags := BundleTags("demo")
	seen := map[string]string{}
	for _, tag := range tags {
		seen[tag.Name] = tag.Value
	}
	if seen[BundleFormatTag] != BundleFormat {
		t.Fatalf("缺少 %s=%s: %+v", BundleFormatTag, BundleFormat, tags)
	}
	if seen[BundleVersionTag] != BundleVersion {
		t.Fatalf("缺少 %s=%s: %+v", BundleVersionTag, BundleVersion, tags)
	}
	if seen[TagRepo] != "demo" {
		t.Fatalf("应当带上仓库名: %+v", tags)
	}
}

// repo 为空时不该出现空的 Repo 标签。
func TestBundleTagsSkipEmptyRepo(t *testing.T) {
	for _, tag := range BundleTags("") {
		if tag.Name == TagRepo {
			t.Fatalf("仓库名为空时不该有 Repo 标签: %+v", tag)
		}
	}
}

func TestSubmitTxRejectsIncompleteSignature(t *testing.T) {
	cases := []*TxSignature{
		nil,
		{},
		{ID: "id", Owner: "owner"}, // 缺 signature
	}
	for _, sig := range cases {
		if _, err := SubmitTx(context.Background(), "http://127.0.0.1:1", []byte("x"), nil, sig, nil); err == nil {
			t.Fatalf("签名字段不全时应当提前报错，实际放行了 %+v", sig)
		}
	}
}

// 体积超限要在发请求之前就拦下，别把半个站点发出去再报错。
func TestSubmitTxRejectsOversizeBundle(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	big := make([]byte, BundleLimit+1)
	sig := &TxSignature{ID: "id", Owner: "o", Signature: "s"}
	if _, err := SubmitTx(context.Background(), srv.URL, big, nil, sig, nil); err == nil {
		t.Fatal("超大 bundle 应当报错")
	}
	if called {
		t.Fatal("超限时不该发出请求")
	}
}

func TestSubmitTxReportsNodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid signature"}`))
	}))
	defer srv.Close()

	sig := &TxSignature{ID: "id", Owner: "o", Signature: "s"}
	_, err := SubmitTx(context.Background(), srv.URL, []byte("x"), nil, sig, nil)
	if err == nil {
		t.Fatal("节点报错时应当返回错误")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Fatalf("错误信息应带上节点的原话: %v", err)
	}
}
