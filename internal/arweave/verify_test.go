package arweave

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 按节点口径复刻的签名输入，必须与 arweave-js 的 getSignatureData() 逐字节相同。
//
// 这一条钉住的是此前从未对拍过的那层：JSON 语义 → 节点解析 → 签名输入。
// 形状对不代表节点解出来的东西对（整数规范化、tags 解码都是这层的事）。
// 基准里的 signatureDataHex 是 arweave-js 对同一笔交易算出的深哈希。
func TestSignatureDataSegmentMatchesArweaveJS(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "arjs-chunked-signed.json"))
	if err != nil {
		t.Fatalf("读基准失败: %v", err)
	}
	var base chunkedBase
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}

	seg := mustSegment(t, &base.PageSig, BundleTags("demo"))
	got := hex.EncodeToString(seg)
	if got != base.SignatureDataHex {
		t.Fatalf("签名输入与 arweave-js 不一致\n got %s\nwant %s", got, base.SignatureDataHex)
	}
}

// 基准那笔交易（arweave-js 真签的）在节点口径下必须验签通过。
func TestVerifySignedTxAcceptsArweaveJSBaseline(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "arjs-chunked-signed.json"))
	if err != nil {
		t.Fatalf("读基准失败: %v", err)
	}
	var base chunkedBase
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}

	if err := VerifySignedTx(&base.PageSig, BundleTags("demo")); err != nil {
		t.Fatalf("arweave-js 签的交易本地验签应当通过: %v", err)
	}
}

// 字段被换过一个字节就必须拦下，且指出是哪一类问题。
func TestVerifySignedTxRejectsTampering(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "arjs-chunked-signed.json"))
	if err != nil {
		t.Fatalf("读基准失败: %v", err)
	}
	var base chunkedBase
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}

	t.Run("data_size 被改", func(t *testing.T) {
		sig := base.PageSig
		sig.DataSize = "2891675"
		if err := VerifySignedTx(&sig, BundleTags("demo")); err == nil {
			t.Fatal("data_size 与签名不符时必须报错")
		}
	})

	t.Run("tags 被改", func(t *testing.T) {
		sig := base.PageSig
		sig.Tags = encodeTxTags(BundleTags("other-repo"))
		if err := VerifySignedTx(&sig, nil); err == nil {
			t.Fatal("tags 与签名不符时必须报错")
		}
	})

	t.Run("id 与签名不符", func(t *testing.T) {
		sig := base.PageSig
		sig.ID = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		err := VerifySignedTx(&sig, BundleTags("demo"))
		if err == nil {
			t.Fatal("id 错了必须报错")
		}
	})

	t.Run("整数带前导零", func(t *testing.T) {
		sig := base.PageSig
		sig.Reward = "0" + sig.Reward
		err := VerifySignedTx(&sig, BundleTags("demo"))
		if err == nil {
			t.Fatal("非规范整数必须报错：节点会把它规整后验签，两边输入不同")
		}
	})
}

// mustSegment 构造签名输入的深哈希，供对拍。
func mustSegment(t *testing.T, sig *TxSignature, tags []Tag) []byte {
	t.Helper()
	owner, err := b64Decode("owner", sig.Owner)
	if err != nil {
		t.Fatal(err)
	}
	lastTx, err := b64Decode("last_tx", sig.LastTx)
	if err != nil {
		t.Fatal(err)
	}
	dataRoot, err := b64Decode("data_root", sig.DataRoot)
	if err != nil {
		t.Fatal(err)
	}
	tagPairs := make([][2][]byte, 0, len(tags))
	for _, tg := range tags {
		tagPairs = append(tagPairs, [2][]byte{[]byte(tg.Name), []byte(tg.Value)})
	}
	seg, err := SignatureDataSegment(2, owner, []byte{}, "0", sig.Reward,
		lastTx, dataRoot, tagPairs, sig.DataSize)
	if err != nil {
		t.Fatal(err)
	}
	return seg
}
