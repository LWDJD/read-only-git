package arweave

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 手工排查工具，不是自动化测试：没有环境变量就跳过。
//
// 用途：拿 arweave-js 真签的那笔交易，让 Go 侧按同样的字段拼一份交易 JSON，
// 输出到指定目录，再交给 node 侧比签名输入。节点报
// Transaction verification failed 这类错时，用它定位「我们发出去的 JSON
// 与钱包签名时的那一份是否等价」。
//
//	# 1. 生成基准（arweave-js 真签的一笔）
//	ARWEAVE_JS_PATH=<包目录> node scripts/arjs-sign-base.cjs base.json
//	# 2. 让 Go 拼一份
//	ARJS_SIGNED=base.json ARJS_OUTDIR=. go test -run TestZZProbe ./internal/arweave/
//	# 3. 比对签名输入
//	node <临时目录>/compare.cjs base.json go-tx.json
//
// 日常的自动化对拍由 txpayload_crosscheck_test.go 负责，它直接读
// testdata/arjs-signed.json，不需要这一步。
func TestZZProbe(t *testing.T) {
	base := os.Getenv("ARJS_SIGNED")
	outDir := os.Getenv("ARJS_OUTDIR")
	if base == "" || outDir == "" {
		t.Skip("手工排查工具：需要 ARJS_SIGNED 与 ARJS_OUTDIR")
	}

	raw, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		ID        string `json:"id"`
		Owner     string `json:"owner"`
		Signature string `json:"signature"`
		Reward    string `json:"reward"`
		LastTx    string `json:"last_tx"`
		DataRoot  string `json:"data_root"`
		DataSize  string `json:"data_size"`
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}

	// 明文 tags：真实流程里 Go 传给页面的是明文，拼交易 JSON 时也拿同一份
	tags := []Tag{
		{Name: "App-Name", Value: "read-only-git"},
		{Name: "Repo", Value: "demo"},
		{Name: "Path", Value: "index.html"},
	}

	sig := &TxSignature{
		ID:        ref.ID,
		Owner:     ref.Owner,
		Signature: ref.Signature,
		Reward:    ref.Reward,
		LastTx:    ref.LastTx,
		DataRoot:  ref.DataRoot,
		DataSize:  ref.DataSize,
	}

	out, err := txPayload([]byte("hij"), tags, sig)
	if err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(outDir, "go-tx.json")
	if err := os.WriteFile(dst, out, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("写到 %s（%d 字节）", dst, len(out))
}
