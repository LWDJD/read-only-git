package arweave

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"math/big"
)

// 节点侧签名输入的复刻。
//
// 节点收到 /tx 后，验签前先把 JSON 解成交易记录、再按
// ar_tx:signature_data_segment_v2 构造签名输入。我们此前只对拍过
// 「JSON 形状」，没对拍过「JSON 解出来之后节点拿什么去验签」——
// 两者之间的语义差（比如整数的规范化、tags 的解码）就是
// 400 Transaction verification failed 最后的藏身处。
//
// 这里逐字复刻 ar_deep_hash 与 signature_data_segment_v2（denomination=0），
// 依据是 arweave 节点源码 apps/arweave/src/{ar_tx,ar_deep_hash}.erl。
// denomination>0 的分支不复刻：arweave-js 造的交易一律 denomination=0。

// deepHash 复刻 ar_deep_hash:hash/1（与 arweave-js 的 deepHash 同构）。
//
//	blob: SHA384( SHA384("blob" + len) || SHA384(data) )
//	list: 从 SHA384("list" + len) 起折叠，每步 SHA384(acc || hash(item))
func deepHash(data []byte) []byte {
	tag := append([]byte("blob"), []byte(fmt.Sprintf("%d", len(data)))...)
	inner := append(sha384Sum(tag), sha384Sum(data)...)
	return sha384Sum(inner)
}

func deepHashList(items []any) []byte {
	acc := sha384Sum(append([]byte("list"), []byte(fmt.Sprintf("%d", len(items)))...))
	for _, item := range items {
		var h []byte
		switch v := item.(type) {
		case []byte:
			h = deepHash(v)
		case []any:
			h = deepHashList(v)
		default:
			panic(fmt.Sprintf("deepHash: 不支持的元素类型 %T", item))
		}
		pair := append(append([]byte{}, acc...), h...)
		acc = sha384Sum(pair)
	}
	return acc
}

func sha384Sum(b []byte) []byte {
	sum := sha512.Sum384(b)
	return sum[:]
}

// SignatureDataSegment 按节点口径构造 format-2 交易的签名输入深哈希。
//
// 九项的顺序、编码照 ar_tx:signature_data_segment_v2（denomination=0）：
// format、owner、target、quantity、reward、last_tx、tags、data_size、data_root。
// 其中 format / quantity / reward / data_size 走**整数的规范十进制**，
// 其余是 base64url 解码后的原始字节，tags 是「解码字节对」的列表。
//
// 整数为什么必须规范化：节点把 JSON 字符串解析成整数、再用
// integer_to_binary 还原，"039" 会被还原成 "39"。签名页若用原始字符串
// 参与签名，两边就不是同一份输入。VerifySignedTx 会把这种不对规范的
// 字符串拦下来。
func SignatureDataSegment(format int, owner, target []byte, quantity, reward string,
	lastTx, dataRoot []byte, tags [][2][]byte, dataSize string) ([]byte, error) {
	return SignatureDataSegmentWithDenomination("", format, owner, target, quantity, reward,
		lastTx, dataRoot, tags, dataSize)
}

// SignatureDataSegmentWithDenomination 同上，但显式指定 denomination。
// 非空时按节点的 List2 形态把 denomination 插在最前（十项）。
func SignatureDataSegmentWithDenomination(denomination string, format int, owner, target []byte,
	quantity, reward string, lastTx, dataRoot []byte, tags [][2][]byte, dataSize string) ([]byte, error) {
	q, err := canonicalInt(quantity)
	if err != nil {
		return nil, fmt.Errorf("quantity 不是规范整数: %w", err)
	}
	r, err := canonicalInt(reward)
	if err != nil {
		return nil, fmt.Errorf("reward 不是规范整数: %w", err)
	}
	ds, err := canonicalInt(dataSize)
	if err != nil {
		return nil, fmt.Errorf("data_size 不是规范整数: %w", err)
	}

	tagItems := make([]any, 0, len(tags))
	for _, t := range tags {
		tagItems = append(tagItems, []any{t[0], t[1]})
	}

	items := []any{
		[]byte(fmt.Sprintf("%d", format)),
		owner,
		target,
		q,
		r,
		lastTx,
		tagItems,
		ds,
		dataRoot,
	}
	if denomination != "" {
		d, err := canonicalInt(denomination)
		if err != nil {
			return nil, fmt.Errorf("denomination 不是规范整数: %w", err)
		}
		items = append([]any{d}, items...)
	}
	return deepHashList(items), nil
}

// canonicalInt 把十进制字符串还原成节点 integer_to_binary 的形态。
//
// 返回的既是规范字节（喂给深哈希），也供调用方比对原串是否本就规范。
func canonicalInt(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("空字符串")
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return nil, fmt.Errorf("%q", s)
	}
	if n.Sign() < 0 {
		return nil, fmt.Errorf("负数 %q", s)
	}
	return []byte(n.String()), nil
}

// b64Decode 解 base64url（无填充），错误信息带字段名。
func b64Decode(field, s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%s 不是合法的 base64url: %w", field, err)
	}
	return b, nil
}

// txIDFromSignature 复刻 verify_hash：id = SHA256(签名)。
func txIDFromSignature(sig []byte) string {
	sum := sha256.Sum256(sig)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
