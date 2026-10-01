package arweave

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// ANS-104 的 data item 头部布局是定长的：
//
//	offset 0    signature type   2 字节
//	offset 2    signature        512 字节（RSA-4096 的签名）
//	offset 514  owner            512 字节
//	...         target / anchor / tags / data
//
// 这里只关心签名那一段，它是算 id 的唯一依据。
const (
	dataItemSignatureTypeSize = 2
	dataItemSignatureSize     = 512
)

// DataItemID 从一个已签名的 data item 里算出它的 id。
//
// 规范规定 id 就是签名字段的 SHA-256，取 base64url。
// Turbo 路径上 id 由上传服务返回；走 L1 时没有服务可问，必须自己算。
// 而 manifest 里存的正是这些 id，算错会让整站的路径映射集体失效，
// 所以这个函数要有测试钉住。
func DataItemID(signed []byte) (string, error) {
	raw, err := dataItemIDRaw(signed)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// dataItemIDRaw 与 DataItemID 同源，返回 32 字节原值。
//
// 分出一个不算 base64 的版本，是因为 bundle 头部里存的 id 就是这 32 字节，
// 走一趟字符串编解码再解回来只会多一个出错面。
func dataItemIDRaw(signed []byte) ([32]byte, error) {
	start := dataItemSignatureTypeSize
	end := start + dataItemSignatureSize
	if len(signed) < end {
		return [32]byte{}, fmt.Errorf("data item 只有 %d 字节，放不下签名段", len(signed))
	}
	return sha256.Sum256(signed[start:end]), nil
}
