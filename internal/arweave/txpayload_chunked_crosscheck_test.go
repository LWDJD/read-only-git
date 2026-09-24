package arweave

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// chunkedBase 是 scripts/arjs-chunked-base.cjs 生成的多块基准。
//
// 三样东西：
//   - TxBody   arweave-js 的上传器在多块时 POST /tx 的完整 JSON（data 置空）
//   - PageSig  签名页回传给 Go 的 TxSignature JSON
//   - SignatureDataHex 签名输入九项的深哈希，对不上时用它定位
type chunkedBase struct {
	GeneratedBy      string          `json:"generatedBy"`
	Size             int             `json:"size"`
	TxBody           json.RawMessage `json:"txBody"`
	PageSig          TxSignature     `json:"pageSig"`
	SignatureDataHex string          `json:"signatureDataHex"`
}

// 多块路径的交易 JSON 必须与 arweave-js 的上传器发出的一模一样。
//
// 单块那组对拍（TestTxPayloadMatchesArweaveJS）证明不了这条路：
// 单块是页面自己 POST，多块才是 Go 拼 JSON。真链上报的
// 400 Transaction verification failed 就出在这条路上，而此前
// 这一路的 JSON 形状从未与 arweave-js 对拍过。
//
// 这里连「页面回传 → Go 解析」也一起走了：PageSig 直接反序列化成
// TxSignature，正是生产里 signer.SignTx 做的事。
func TestTxPayloadChunkedMatchesArweaveJS(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "arjs-chunked-signed.json"))
	if err != nil {
		t.Fatalf("读基准失败（用 scripts/arjs-chunked-base.cjs 重新生成）: %v", err)
	}

	var base chunkedBase
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}

	sig := &base.PageSig
	if sig.DataSize != strconv.Itoa(base.Size) {
		t.Fatalf("回传的 data_size 应当是 %q，实际 %q", strconv.Itoa(base.Size), sig.DataSize)
	}
	if len(sig.Proofs) < 2 {
		t.Fatalf("基准应当是多块交易，实际只有 %d 块", len(sig.Proofs))
	}
	if sig.Uploaded {
		t.Fatal("多块时页面不该自己提交（uploaded 该是 false）")
	}

	// 明文 tags 是生产里传给页面、也是传给 txPayload 的同一份
	tags := BundleTags("demo")

	out, err := txPayload(nil, tags, sig)
	if err != nil {
		t.Fatal(err)
	}

	var got, want map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("载荷应当是合法 JSON: %v", err)
	}
	if err := json.Unmarshal(base.TxBody, &want); err != nil {
		t.Fatalf("基准应当是合法 JSON: %v", err)
	}

	// 键集先比：多一个少一个字段都会让节点的解析结果变形
	for k := range want {
		if _, ok := got[k]; !ok {
			t.Errorf("缺字段 %q（arweave-js 发的 /tx body 里有）", k)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("多出字段 %q（arweave-js 发的 /tx body 里没有）", k)
		}
	}

	// 再逐字段比。data 两边都该是空串：多块时交易本体不带数据。
	if !reflect.DeepEqual(got, want) {
		for k, wv := range want {
			gv := got[k]
			if !reflect.DeepEqual(gv, wv) {
				gb, _ := json.Marshal(gv)
				wb, _ := json.Marshal(wv)
				t.Errorf("字段 %s 不一致\n got %s\nwant %s", k, gb, wb)
			}
		}
		t.Fatal("Go 拼出的 /tx body 与 arweave-js 的不一致")
	}
}
