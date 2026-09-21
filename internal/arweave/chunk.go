package arweave

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
