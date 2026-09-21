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

// BundleLimit 是单笔交易能直接装下的体积上限，也就是一个 chunk。
//
// 超过它并不是错误：SubmitBundle 会自动走分块协议（先报不带 data 的交易，
// 再逐块 POST /chunk）。这个常量只在「本该分块却没有分块证明」时用得上。
const BundleLimit = MaxChunkSize

// CheckBundleSize 确认体积还在「一笔交易直接装下」的范围内。
//
// 只有单块路径会调它。走到这里却超过上限，说明这一包本该分块，
// 却没有可用的 proof：多半是签名页版本旧了，或是回传的证明丢了。
// 这种情况不能硬发，早点说清楚比发一半被节点拒好。
func CheckBundleSize(size int) error {
	if size > BundleLimit {
		return fmt.Errorf("bundle 有 %d 字节，超过单笔交易上限 %d 字节，却没有可用的分块证明；"+
			"请确认签名页已更新，或重开后重新签名", size, BundleLimit)
	}
	return nil
}
