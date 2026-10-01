package arweave

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// txShape 是交易 JSON 里参与签名的那些字段。
//
// 按 arweave-js 的 getSignatureData() 抄的：format、owner、target、
// quantity、reward、last_tx、tags、data_size、data_root 九项。
// 少一项或多一项，节点验签就会失败。
type txShape struct {
	Format    int    `json:"format"`
	ID        string `json:"id"`
	LastTx    string `json:"last_tx"`
	Owner     string `json:"owner"`
	Tags      []Tag  `json:"tags"`
	Target    string `json:"target"`
	Quantity  string `json:"quantity"`
	Data      string `json:"data"`
	DataSize  string `json:"data_size"`
	DataRoot  string `json:"data_root"`
	Reward    string `json:"reward"`
	Signature string `json:"signature"`
}

// 与 arweave-js 对拍：同一批字段拼出来的交易 JSON，必须与它逐字段一致。
//
// 基准文件由 scripts/arjs-sign-base.cjs 生成，是 arweave-js 真签的一笔交易。
// 签名输入一致，验签就必然一致，这是交易能被节点接受的前提。
//
// 参考：《arweave-js 的 Transaction.getSignatureData()》——
// format 2 的签名输入是 deepHash([format, owner, target, quantity, reward,
// last_tx, tags, data_size, data_root])，其中 data_size 是明文字节，
// 其余带字节语义的字段都是 base64url 解码后的原始字节。
func TestTxPayloadMatchesArweaveJS(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "arjs-signed.json"))
	if err != nil {
		t.Fatalf("读基准失败（用 scripts/arjs-sign-base.cjs 重新生成）: %v", err)
	}

	var base txShape
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}

	// 明文 tags：真实流程里 Go 传给页面的是明文，
	// 拼交易 JSON 时拿的也是同一份（txPayload 内部负责编码）。
	plainTags := []Tag{
		{Name: "App-Name", Value: "read-only-git"},
		{Name: "Repo", Value: "demo"},
		{Name: "Path", Value: "index.html"},
	}

	sig := &TxSignature{
		ID:        base.ID,
		Owner:     base.Owner,
		Signature: base.Signature,
		Reward:    base.Reward,
		LastTx:    base.LastTx,
		DataRoot:  base.DataRoot,
		DataSize:  base.DataSize,
	}

	out, err := txPayload([]byte("hij"), plainTags, sig)
	if err != nil {
		t.Fatal(err)
	}

	var got txShape
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("载荷应当是合法 JSON: %v", err)
	}

	// 逐项比。sig.ID 等原样透传，但 tags 与 data 是我们自己编的，
	// 这两项才是真正会对不上的地方。
	if got.Format != base.Format {
		t.Errorf("format: got %d want %d", got.Format, base.Format)
	}
	if got.Owner != base.Owner {
		t.Error("owner 应当原样透传")
	}
	if got.Target != base.Target {
		t.Errorf("target: got %q want %q", got.Target, base.Target)
	}
	if got.Quantity != base.Quantity {
		t.Errorf("quantity: got %q want %q", got.Quantity, base.Quantity)
	}
	if got.Reward != base.Reward {
		t.Errorf("reward: got %q want %q", got.Reward, base.Reward)
	}
	if got.LastTx != base.LastTx {
		t.Errorf("last_tx: got %q want %q", got.LastTx, base.LastTx)
	}
	if got.DataSize != base.DataSize {
		t.Errorf("data_size: got %q want %q", got.DataSize, base.DataSize)
	}
	if got.DataRoot != base.DataRoot {
		t.Errorf("data_root: got %q want %q", got.DataRoot, base.DataRoot)
	}
	if got.Data != base.Data {
		t.Errorf("data 的 base64url 不对: got %q want %q", got.Data, base.Data)
	}
	if got.Signature != base.Signature {
		t.Error("signature 应当原样透传")
	}
	if !reflect.DeepEqual(got.Tags, base.Tags) {
		t.Fatalf("tags 应当 base64url 编码\n got %+v\nwant %+v", got.Tags, base.Tags)
	}
}
