package arweave

import (
	"encoding/binary"
	"fmt"
)

// Bundle 把已签名的 data item 按规范拼成 ANS-104 bundle。
//
// 结构（照 arbundles 的 bundleAndSignData，逐字节对齐）：
//
//	[32 字节]      item 数量（32 字节小端）
//	[64 字节 × N]  每项：前 32 字节是该项的字节长度，后 32 字节是该项的 id
//	[本体]         data item 按顺序紧密排列，无分隔
//
// 这里最容易想当然的地方：以为 bundle 就是 data item 的裸拼接。不是。
// 少了头部，网关读前 32 字节当 item 数量，读到的却是一段签名数据，
// 直接判非法，于是里面的 data item 一个都索引不出来。
// 现象就是「交易在链上、网关却打不开」——内容存下来了，但没人能按 id 找到它。
//
// 头部的 id 不是索引，是校验：网关要靠它确认每一项的身份。
const (
	// bundleCountSize 是头部开头「item 数量」占的字节数。
	bundleCountSize = 32
	// bundleItemHeaderSize 是头部里每项占的字节数：长度 32 + id 32。
	bundleItemHeaderSize = 64
)

func Bundle(dataItems [][]byte) ([]byte, error) {
	if len(dataItems) == 0 {
		return nil, fmt.Errorf("bundle 里至少要有一个 data item")
	}

	total := bundleCountSize + bundleItemHeaderSize*len(dataItems)
	for _, item := range dataItems {
		total += len(item)
	}
	out := make([]byte, 0, total)

	// 一、item 数量。
	out = append(out, longTo32ByteArray(uint64(len(dataItems)))...)

	// 二、每项的「长度 + id」。
	//
	// 顺序必须与第三段的排列一致，否则网关算出的偏移全错。
	// 这里不做排序，就用传入顺序：调用方按什么次序攒的，就按什么次序写。
	for _, item := range dataItems {
		rawID, err := dataItemIDRaw(item)
		if err != nil {
			return nil, err
		}
		out = append(out, longTo32ByteArray(uint64(len(item)))...)
		out = append(out, rawID[:]...)
	}

	// 三、本体。
	for _, item := range dataItems {
		out = append(out, item...)
	}
	return out, nil
}

// longTo32ByteArray 把整数写成 32 字节小端，与 arbundles 的 longTo32ByteArray 一致。
//
// 只有低 8 字节有效，高位补零。写成 32 字节而不是 8，是格式要求，
// 与「数量」或「长度」的实际范围无关。
func longTo32ByteArray(n uint64) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint64(b[:8], n)
	return b
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
