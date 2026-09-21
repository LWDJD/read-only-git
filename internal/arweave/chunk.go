package arweave

import (
	"fmt"
	"strconv"
)

// 分块上传用到的东西。
//
// Arweave 的单笔交易只有在不超过一个 chunk 时才允许把 data 直接装进 /tx 的 body。
// 超出就得走分块协议：先提交一笔不带 data 的交易，再逐块 POST /chunk。
//
// 这一套的难点不在切块与 Merkle 怎么写，而在「谁算 data_root」。
// format 2 交易的签名内容里含 data_root，交易提交时也必须带上它，
// 两边算出来的只要差一个字节，签出来的就是一笔废交易。
//
// 所以这里不重算一份：data_root 与各块的 proof 都由签名页里的 arweave-js
// 算好回传，Go 只负责按同一套切法切出块内容并逐块发出去。
// 这样签名的依据与提交的依据天然是同一份，不存在对不上的可能。

// 切块参数，与 arweave 的规范一致。
const (
	// MaxChunkSize 是单块上限。
	MaxChunkSize = 256 * 1024

	// MinChunkSize 是单块下限。切到最后剩余不足这个值时，
	// 要把当前这块平分，避免出现一个很小的尾块。
	MinChunkSize = 32 * 1024
)

// ChunkProof 是一块的 Merkle 证明，由签名页回传。
//
// DataPath 是 proof 的原始字节做 base64url（不是十六进制），
// Offset 是这块末字节在整份数据里的偏移，两者都以字符串形式传递，
// 与节点接口保持一致。
type ChunkProof struct {
	DataPath string `json:"data_path"`
	Offset   string `json:"offset"`
}

// ChunkRange 是一块在整份数据里的区间，左闭右开。
//
// 只记边界而不拷内容：调用方手里就是那份 bundle，切片即可，
// 没必要在内存里再铺一份。
type ChunkRange struct {
	Min   int
	Max   int
	Proof ChunkProof
}

// SplitChunks 按 proofs 给出的偏移把数据切成块。
//
// 不重算切块规则，直接用 offset 推：offset 是这块末字节的位置，
// 加一得右边界，上一块的右边界就是本块的左边界。
// 这样与 arweave-js 的切法天然一致，中间不会出现两套算法对不上的地方。
//
// 注意 offset 不代表「按固定块长切」：切块时会先把数据切成含一个零长度
// 尾块的形状并建树，树建完才把零长那条丢掉，所以某一块的 proof 可能比
// 固定块长推出的要长。用 offset 就绕开了这个细节。
func SplitChunks(data []byte, proofs []ChunkProof) ([]ChunkRange, error) {
	if len(proofs) == 0 {
		return nil, fmt.Errorf("没有 proof，无法切块")
	}

	out := make([]ChunkRange, 0, len(proofs))
	start := 0
	for i, p := range proofs {
		end, err := strconv.Atoi(p.Offset)
		if err != nil {
			return nil, fmt.Errorf("第 %d 块的 offset 不是数字: %q", i, p.Offset)
		}
		// offset 是末字节的索引，区间末端要加一
		end++
		if end < start {
			return nil, fmt.Errorf("第 %d 块的 offset 往回退: %d，上一块结束于 %d", i, end, start)
		}
		if end > len(data) {
			return nil, fmt.Errorf("第 %d 块越界: 结束于 %d，数据只有 %d 字节", i, end, len(data))
		}
		out = append(out, ChunkRange{Min: start, Max: end, Proof: p})
		start = end
	}

	// 所有块加起来必须刚好覆盖整份数据，多一个字节少一个字节都不行
	if start != len(data) {
		return nil, fmt.Errorf("proof 覆盖 %d 字节，数据有 %d 字节，对不上", start, len(data))
	}
	return out, nil
}
