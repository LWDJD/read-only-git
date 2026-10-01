package arweave

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// PendingTx 是一笔「已经签好名、但还没提交成功」的交易。
//
// 它存在的理由只有一个：签名是用户在钱包里点过确认的动作，
// 不该因为一次网络失败就作废。把签名结果连同包体一起落到磁盘上，
// 下次就能直接重传，不必再让人去钱包里点一遍。
type PendingTx struct {
	// Bundle 是待提交的字节（JSON 里走 base64）。
	Bundle []byte `json:"bundle"`
	// Tags 是外层交易的标签，用明文存：提交时会按交易 JSON 的规矩编码。
	Tags []Tag `json:"tags"`
	// Sig 是钱包签好的字段。
	Sig *TxSignature `json:"sig"`
	// At 是签名完成的时间，用来判断这份是否还值得复用。
	At time.Time `json:"at"`
}

// SavePending 把一笔签好的交易写到磁盘。
func SavePending(path string, bundle []byte, tags []Tag, sig *TxSignature) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(&PendingTx{Bundle: bundle, Tags: tags, Sig: sig, At: time.Now()})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadPending 读回一笔待提交的交易；没有或读不出来时返回 nil。
//
// 读不出来就当没有，不报错：这只是一份加速用的缓存，
// 丢了顶多让用户多点一次钱包，不该因此拦住整条发布。
func LoadPending(path string) *PendingTx {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var p PendingTx
	if err := json.Unmarshal(data, &p); err != nil || p.Sig == nil {
		return nil
	}
	return &p
}

// ClearPending 删掉那份待提交记录。提交成功后调用。
func ClearPending(path string) {
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// SameBundle 判断这份待提交记录的包体是不是当前这一份。
//
// 站点内容变了，包体就会变，旧签名自然不能再拿来用。
func (p *PendingTx) SameBundle(bundle []byte) bool {
	if p == nil || len(p.Bundle) != len(bundle) {
		return false
	}
	for i := range bundle {
		if p.Bundle[i] != bundle[i] {
			return false
		}
	}
	return true
}
