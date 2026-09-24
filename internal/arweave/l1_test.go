package arweave

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeDataItem 造一个长度恰好 n 的假 data item：
// 前 2 字节是签名类型，接着 512 字节是「签名」，其余用填充凑够。
// 真签名内容无关紧要，这里只关心布局。
func makeDataItem(fill byte, n int) []byte {
	if n < 514 {
		n = 514
	}
	item := make([]byte, n)
	for i := range item {
		item[i] = fill
	}
	return item
}

// bundle 必须是 ANS-104 结构，不是裸拼接。
//
// 依据是 arbundles 的 bundleAndSignData：
//
//	Buffer.concat([longTo32ByteArray(items.length), headers, binaries])
//
// 其中 headers 每项 64 字节（长度 32 + id 32）。
// 少了这一段，网关读前 32 字节当数量，读到的却是签名数据，直接判非法，
// 于是 bundle 里的 data item 一个都索引不出来——
// 现象就是交易在链上、而入口 id 查 404。
func TestBundleHasANS104Header(t *testing.T) {
	items := [][]byte{
		makeDataItem(0xA1, 8),
		makeDataItem(0xB2, 12),
	}
	out, err := Bundle(items)
	if err != nil {
		t.Fatalf("拼 bundle 失败: %v", err)
	}

	// 一、开头 32 字节是 item 数量，且高位补零。
	if got := binary.LittleEndian.Uint64(out[:8]); got != 2 {
		t.Fatalf("item 数量期望 2，得到 %d", got)
	}
	for i := 8; i < bundleCountSize; i++ {
		if out[i] != 0 {
			t.Fatalf("数量字段的高位应当补零，offset %d = %d", i, out[i])
		}
	}

	// 二、头部每项 64 字节：长度（32）+ id（32）。
	bodyStart := bundleCountSize + bundleItemHeaderSize*len(items)
	for i, item := range items {
		base := bundleCountSize + bundleItemHeaderSize*i
		if got := binary.LittleEndian.Uint64(out[base : base+8]); got != uint64(len(item)) {
			t.Errorf("第 %d 项长度期望 %d，得到 %d", i, len(item), got)
		}
		sum := sha256.Sum256(item[dataItemSignatureTypeSize : dataItemSignatureTypeSize+dataItemSignatureSize])
		if !bytes.Equal(out[base+32:base+64], sum[:]) {
			t.Errorf("第 %d 项的 id 不对", i)
		}
	}

	// 三、本体按顺序紧密排列。
	if len(out) != bodyStart+len(items[0])+len(items[1]) {
		t.Fatalf("总长期望 %d，得到 %d", bodyStart+len(items[0])+len(items[1]), len(out))
	}
	if !bytes.Equal(out[bodyStart:bodyStart+len(items[0])], items[0]) {
		t.Error("第一项本体位置不对")
	}
	if !bytes.Equal(out[bodyStart+len(items[0]):], items[1]) {
		t.Error("第二项本体位置不对")
	}
}

// 空的 bundle 无法构成合法 ANS-104（数量字段写 0，但一段本体都没有），应当直接报错。
func TestBundleRejectsEmpty(t *testing.T) {
	if _, err := Bundle(nil); err == nil {
		t.Fatal("空输入应当报错")
	}
	if _, err := Bundle([][]byte{}); err == nil {
		t.Fatal("空切片应当报错")
	}
}

// 短于签名段的输入算不出 id，不能默默拼出一个坏 bundle。
func TestBundleRejectsShortItem(t *testing.T) {
	if _, err := Bundle([][]byte{{1, 2, 3}}); err == nil {
		t.Fatal("data item 放不下签名段时应当报错")
	}
}

// 拼接不能改动传入的切片，否则调用方手里的 data item 会被悄悄改掉。
func TestBundleDoesNotMutateInput(t *testing.T) {
	item := makeDataItem(9, 520)
	out, err := Bundle([][]byte{item})
	if err != nil {
		t.Fatalf("拼 bundle 失败: %v", err)
	}
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
		DataRoot:  "root-b64url",
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
	// data_root 必须带上：签名算的就是它，交易里漏了就不自洽
	if got["data_root"] != "root-b64url" {
		t.Fatalf("data_root 应原样带上，实际 %#v", got["data_root"])
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
	sig := &TxSignature{ID: "id", Owner: "o", Signature: "s", DataRoot: "r"}
	if _, err := SubmitTx(context.Background(), srv.URL, big, nil, sig, nil); err == nil {
		t.Fatal("超大 bundle 应当报错")
	}
	if called {
		t.Fatal("超限时不该发出请求")
	}
}

// data_root 是签名内容的一部分，缺了就不能提交。
// 之前漏了这个字段，交易 JSON 里没有它，节点会拒。
func TestSubmitTxRejectsMissingDataRoot(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sig := &TxSignature{ID: "id", Owner: "o", Signature: "s"} // 故意不给 data_root
	_, err := SubmitTx(context.Background(), srv.URL, []byte("x"), nil, sig, nil)
	if err == nil {
		t.Fatal("缺 data_root 应当报错")
	}
	if !strings.Contains(err.Error(), "data_root") {
		t.Fatalf("错误信息应点明 data_root: %v", err)
	}
	if called {
		t.Fatal("字段不全时不该发出请求")
	}
}

func TestSubmitTxReportsNodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid signature"}`))
	}))
	defer srv.Close()

	sig := &TxSignature{ID: "id", Owner: "o", Signature: "s", DataRoot: "r"}
	_, err := SubmitTx(context.Background(), srv.URL, []byte("x"), nil, sig, nil)
	if err == nil {
		t.Fatal("节点报错时应当返回错误")
	}
	if !strings.Contains(err.Error(), "invalid signature") {
		t.Fatalf("错误信息应带上节点的原话: %v", err)
	}
}
