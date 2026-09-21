package arweave

import "fmt"

// Bundle 把已签名的 data item 按顺序拼成 ANS-104 bundle。
//
// ANS-104 的 bundle 就是 data item 的简单拼接：每个 data item 自带签名与长度，
// 不需要额外的头部或索引。网关与 bundler 都是这么解析的。
//
// 注意拼接顺序无关紧要，但必须是完整的 data item 字节（含各自尾部签名），
// 少一个字节都会让整个 bundle 解析失败。
func Bundle(dataItems [][]byte) []byte {
	var total int
	for _, item := range dataItems {
		total += len(item)
	}

	out := make([]byte, 0, total)
	for _, item := range dataItems {
		out = append(out, item...)
	}
	return out
}

// BundleLimit 是一次性能提交的 bundle 上限。
//
// Arweave 的单笔交易只有在不超过一个 chunk 时才允许把 data 直接带在 /tx 里，
// 超出就得走分块协议（先提交不带 data 的交易，再逐块 POST /chunk）。
// 那条路还要算 Merkle 树，目前没实现，所以这里先把上限卡住，
// 让超出的情况早失败、给一句能看懂的提示，而不是发出去一半报个原始错误。
const BundleLimit = 256 * 1024

// CheckBundleSize 在提交前确认体积在可一次性提交的范围内。
func CheckBundleSize(size int) error {
	if size > BundleLimit {
		return fmt.Errorf("bundle 有 %d 字节，超过一次性提交上限 %d 字节；"+
			"大站点需要分块上传，当前版本还没实现", size, BundleLimit)
	}
	return nil
}
