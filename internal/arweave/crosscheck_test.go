package arweave

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

// arjsVector 是 scripts/arjs-vectors.cjs 生成的对照数据。
type arjsVector struct {
	GeneratedBy string `json:"generatedBy"`
	Fill        string `json:"fill"`
	Cases       []struct {
		Size         int      `json:"size"`
		Offsets      []int    `json:"offsets"`
		DataRoot     string   `json:"dataRoot"`
		ChunkHashes  []string `json:"chunkHashes"`
		ProofLengths []int    `json:"proofLengths"`
	} `json:"cases"`
}

// 与 arweave-js 逐字节对拍。
//
// 向量由 scripts/arjs-vectors.cjs 生成，填充规则写在文件里：data[i] = i & 0xff。
// 与 chunk_test.go 里那组硬编码用例是互补的：
//   - 硬编码那组不依赖任何外部文件，任何时候都能跑
//   - 这一组可以重新生成，能在 arweave-js 改口径时立刻发现
//
// 数据文件不在时跳过，不把「没生成过」当成失败。
func TestCrossCheckWithArweaveJS(t *testing.T) {
	const path = "testdata/arjs-vectors.json"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("没有对照数据（%s），先跑 node scripts/arjs-vectors.cjs", path)
	}

	var vec arjsVector
	if err := json.Unmarshal(raw, &vec); err != nil {
		t.Fatalf("对照数据解析失败: %v", err)
	}
	if len(vec.Cases) == 0 {
		t.Fatal("对照数据里没有用例")
	}
	t.Logf("对照来源：%s，填充规则：%s", vec.GeneratedBy, vec.Fill)

	for _, c := range vec.Cases {
		data := make([]byte, c.Size)
		for i := range data {
			data[i] = byte(i & 0xff)
		}

		proofs := make([]ChunkProof, len(c.Offsets))
		for i, off := range c.Offsets {
			proofs[i] = ChunkProof{Offset: strconv.Itoa(off)}
		}

		got, err := SplitChunks(data, proofs)
		if err != nil {
			t.Fatalf("%d 字节: %v", c.Size, err)
		}
		if len(got) != len(c.ChunkHashes) {
			t.Fatalf("%d 字节: 块数应为 %d，实际 %d", c.Size, len(c.ChunkHashes), len(got))
		}

		// 每块的内容哈希都要对得上，这才说明「切出来的东西是同一份」，
		// 而不只是边界碰巧一致。
		for i, r := range got {
			sum := sha256.Sum256(data[r.Min:r.Max])
			if hex.EncodeToString(sum[:]) != c.ChunkHashes[i] {
				t.Fatalf("%d 字节第 %d 块: 内容哈希与 arweave-js 不一致\n  想要 %s\n  实际 %s",
					c.Size, i, c.ChunkHashes[i], hex.EncodeToString(sum[:]))
			}
		}
	}
}
