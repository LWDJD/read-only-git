package arweave

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
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

// 分块提交的重试参数。
//
// arweave-js 那边是等 40 秒、连错 100 次才放弃；命令行工具不该拖这么久，
// 但「重试总比重新签名便宜」这条依然成立，给几次机会就够。
// chunkMaxAttempts 是单块的重试上限。
const chunkMaxAttempts = 5

// chunkBackoffBase 是退避基数，测试里会调小。
// 不写成常量是因为它需要能被测试改，否则跑一次重试用例要等十几秒。
var chunkBackoffBase = 2 * time.Second

// fatalChunkErrors 是重试也没有意义的失败。名单照 arweave-js 的
// FATAL_CHUNK_UPLOAD_ERRORS，出现这些说明内容本身不对。
var fatalChunkErrors = []string{
	"invalid_json",
	"chunk_too_big",
	"data_path_too_big",
	"offset_too_big",
	"data_size_too_big",
	"chunk_proof_ratio_not_attractive",
	"invalid_proof",
}

// SubmitBundle 提交一整包，按块数自动选路。
//
//	1 块：走 /tx，data 装进 body
//	多块：先 /tx 报一笔不带 data 的交易，再逐块 POST /chunk
//
// logf 可为 nil，用来报逐块进度。
func SubmitBundle(ctx context.Context, node string, data []byte, tags []Tag, sig *TxSignature, client *http.Client, logf func(string, ...any)) (string, error) {
	if strings.TrimSpace(node) == "" {
		node = DefaultNode
	}
	if sig == nil {
		return "", fmt.Errorf("缺少签名字段")
	}

	// 装得下就一步到位，不必走分块
	if len(sig.Proofs) <= 1 {
		return SubmitTx(ctx, node, data, tags, sig, client)
	}

	chunks, err := SplitChunks(data, sig.Proofs)
	if err != nil {
		return "", err
	}

	// 先把交易报上去，此时 data 为空。这一步过了，块才会有地方可挂。
	header, err := txPayload(nil, tags, sig)
	if err != nil {
		return "", err
	}
	if _, err := postJSON(ctx, node, "/tx", header, client); err != nil {
		return "", fmt.Errorf("提交交易失败: %w", err)
	}

	for i, c := range chunks {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := postChunk(ctx, node, data, c, sig, client); err != nil {
			return "", fmt.Errorf("提交第 %d/%d 块失败: %w", i+1, len(chunks), err)
		}
		if logf != nil {
			logf("已提交 %d/%d 块", i+1, len(chunks))
		}
	}
	return sig.ID, nil
}

// postChunk 提交一块，对值得重试的失败做有限退避。
func postChunk(ctx context.Context, node string, data []byte, c ChunkRange, sig *TxSignature, client *http.Client) error {
	// 字段名对齐 arweave-js 的 Transaction.getChunk()，节点就是照它解的。
	payload, err := json.Marshal(struct {
		DataRoot string `json:"data_root"`
		DataSize string `json:"data_size"`
		DataPath string `json:"data_path"`
		Offset   string `json:"offset"`
		Chunk    string `json:"chunk"`
	}{
		DataRoot: sig.DataRoot,
		DataSize: DataSizeOf(sig, data),
		DataPath: c.Proof.DataPath,
		Offset:   c.Proof.Offset,
		Chunk:    base64.RawURLEncoding.EncodeToString(data[c.Min:c.Max]),
	})
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt < chunkMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * chunkBackoffBase):
			}
		}

		_, err := postJSON(ctx, node, "/chunk", payload, client)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryableChunkError(err) {
			return err
		}
	}
	return lastErr
}

// retryableChunkError 判断这次失败值不值得重试。
//
// 网络层错误、429、5xx 值得；被节点点名的致命错不值得；
// 其余 4xx 多半是我们发的内容不对，重发多少次都一样。
func retryableChunkError(err error) bool {
	var ne *nodeError
	if !errors.As(err, &ne) {
		// 不是节点给出的回应，按网络问题处理
		return true
	}
	for _, name := range fatalChunkErrors {
		if strings.Contains(ne.Body, name) {
			return false
		}
	}
	switch ne.Status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// DataSizeOf 取这一包的字节数，优先用页面回传的值。
//
// 页面版本旧一点没带这个字段时用实际长度兜底，
// 总比把一个空串发给节点好。
func DataSizeOf(sig *TxSignature, data []byte) string {
	if sig != nil && sig.DataSize != "" {
		return sig.DataSize
	}
	return strconv.Itoa(len(data))
}
