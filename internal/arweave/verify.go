package arweave

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"fmt"
	"math/big"
)

// 提交前的本地验签：按节点的口径把「这笔交易会被怎么判」先判一遍。
//
// 为什么必须有它：节点对 POST /tx 只回一句
// "Transaction verification failed."，那是十项检查的统一出口，
// 光看它永远不知道差在哪。而其中两项（签名、id 绑定）完全可以在本地
// 精确复算——节点验的就是这两样。提交前先过这一关，
// 「页面签的对象」与「节点验的对象」是否同一个就不再靠推理，
// 而是字节级可证的。剩下的失败只可能来自费用、余额与账户状态。
//
// 依据：arweave 节点源码 ar_tx.erl（signature_data_segment_v2 /
// verify_hash / verify_signature_v2）与 ar_wallet.erl（rsa_pss, sha256）。

// VerifySignedTx 按节点口径验一笔 format-2 交易的签名与 id。
//
// plainTags 是页面旧版不回传 tags 时的回退（明文）；有 sig.Tags 时以它为准，
// 与 txPayload 发出去的那份完全同构——节点解码什么，这里就解码什么。
//
// 返回 nil 表示节点的 tx_signature_not_valid 与 tx_id_not_valid 两项必过。
func VerifySignedTx(sig *TxSignature, plainTags []Tag) error {
	if sig == nil {
		return fmt.Errorf("缺少签名字段")
	}

	owner, err := b64Decode("owner", sig.Owner)
	if err != nil {
		return err
	}
	signature, err := b64Decode("signature", sig.Signature)
	if err != nil {
		return err
	}
	lastTx, err := b64Decode("last_tx", sig.LastTx)
	if err != nil {
		return err
	}
	if sig.DataRoot == "" {
		return fmt.Errorf("data_root 不能为空：签名输入里有它")
	}
	dataRoot, err := b64Decode("data_root", sig.DataRoot)
	if err != nil {
		return err
	}

	// 一、id 必须是签名的 SHA-256（节点的 verify_hash）。
	if want := txIDFromSignature(signature); sig.ID != want {
		return fmt.Errorf("id 与签名对不上（节点会报 tx_id_not_valid）：回传 %q，按签名算应为 %q",
			sig.ID, want)
	}

	// 二、整数字段必须是规范十进制。
	//
	// 节点解析时会走「字符串 → 整数 → 规范字符串」，而签名页参与签名的
	// 是原始字符串。"039" 这种写法两边就不是同一份输入，验签必挂。
	for _, f := range []struct{ name, val string }{
		{"reward", sig.Reward},
		{"data_size", sig.DataSize},
	} {
		canon, err := canonicalInt(f.val)
		if err != nil {
			return fmt.Errorf("%s 无法解析: %w", f.name, err)
		}
		if !bytes.Equal(canon, []byte(f.val)) {
			return fmt.Errorf("%s 不是规范十进制（%q → %q），与节点解析结果不一致，验签必挂",
				f.name, f.val, canon)
		}
	}

	// 三、按节点口径构造签名输入并验签（RSA-PSS + SHA-256）。
	//
	// tags 取交易 JSON 里真正会发出去的那份（txTagsOf），再按节点的方式
	// 解码回字节：JSON 发什么，节点就解什么，签名输入就该按什么算。
	tagPairs := make([][2][]byte, 0, len(plainTags)+len(sig.Tags))
	for _, t := range txTagsOf(plainTags, sig) {
		name, err := b64Decode("tag name", t.Name)
		if err != nil {
			return err
		}
		value, err := b64Decode("tag value", t.Value)
		if err != nil {
			return err
		}
		tagPairs = append(tagPairs, [2][]byte{name, value})
	}

	seg, err := SignatureDataSegment(2, owner, []byte{}, "0", sig.Reward,
		lastTx, dataRoot, tagPairs, sig.DataSize)
	if err != nil {
		return err
	}

	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(owner), E: 65537}
	// VerifyPSS 收的是「消息的摘要」：它自己不再 hash，
	// 与 rsa_pss:verify(Data, sha256, …) 内部先 SHA-256(Data) 再验同构。
	digest := sha256.Sum256(seg)
	opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto, Hash: crypto.SHA256}
	if err := rsa.VerifyPSS(pub, crypto.SHA256, digest[:], signature, opts); err != nil {
		return fmt.Errorf("签名自验没过（节点会报 tx_signature_not_valid）：%v", err)
	}
	return nil
}
