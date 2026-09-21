package arweave

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultNode 是提交交易用的节点。
const DefaultNode = "https://arweave.net"

// 装 bundle 的外层交易必须带上的标签。网关靠这两个识别它是一整包。
const (
	BundleFormatTag  = "Bundle-Format"
	BundleVersionTag = "Bundle-Version"
	BundleFormat     = "binary"
	BundleVersion    = "2.0.0"

	// BundleContentType 是外层交易 data 的内容类型。
	BundleContentType = "application/octet-stream"
)

// BundleTags 是外层交易要带的标签。
//
// 每个文件自己的标签在各自的 data item 里，这里只放「这是一整包」的标记。
func BundleTags(repo string) []Tag {
	tags := []Tag{
		{Name: TagAppName, Value: AppName},
		{Name: TagContentType, Value: BundleContentType},
		{Name: BundleFormatTag, Value: BundleFormat},
		{Name: BundleVersionTag, Value: BundleVersion},
	}
	if repo != "" {
		tags = append(tags, Tag{Name: TagRepo, Value: repo})
	}
	return tags
}

// TxSignature 是钱包签完一笔交易后回传的字段。
//
// 刻意不含 data：签名内容里已经绑定了 data_root，而 bundle 字节本来就在 Go 这边，
// 让它回传只会多一次 base64 膨胀。
//
// DataRoot 不能省。format 2 交易的签名内容里就含它，节点验签时也按它校，
// 交易 JSON 里漏掉这个字段，签名字段本身就不再自洽。
type TxSignature struct {
	ID        string `json:"id"`
	Owner     string `json:"owner"`
	Signature string `json:"signature"`
	Reward    string `json:"reward"`
	LastTx    string `json:"last_tx"`
	DataRoot  string `json:"data_root"`
}

// TxSigner 是交易签名通道：把 bundle 与 tags 交给钱包，拿回交易的签名字段。
//
// 与 Signer 的区别在于签的对象不同：Signer 签的是一个个 data item，
// TxSigner 签的是「data 为整个 bundle」的那笔外层交易。
type TxSigner interface {
	SignTx(ctx context.Context, data []byte, tags []Tag) (*TxSignature, error)
}

// SubmitTx 用签名字段与 bundle 字节拼出一笔交易，POST 给节点，返回交易 id。
func SubmitTx(ctx context.Context, node string, bundle []byte, tags []Tag, sig *TxSignature, client *http.Client) (string, error) {
	if strings.TrimSpace(node) == "" {
		node = DefaultNode
	}
	if sig == nil {
		return "", fmt.Errorf("缺少签名字段")
	}
	if sig.ID == "" || sig.Owner == "" || sig.Signature == "" {
		return "", fmt.Errorf("签名字段不完整：id / owner / signature 缺一不可")
	}
	// data_root 与签名是绑在一起的：签名算的就是它。
	// 交易里漏掉它，节点拿到的就是一笔自称与签名不符的交易。
	if sig.DataRoot == "" {
		return "", fmt.Errorf("签名字段缺少 data_root：交易签名绑定了它，不能省")
	}
	if err := CheckBundleSize(len(bundle)); err != nil {
		return "", err
	}

	// 字段名对齐 arweave-js 的 Transaction.toJSON()，节点就是照它解的。
	// data_size 是字符串不是数字，这点容易写错。
	tx := struct {
		Format    int    `json:"format"`
		ID        string `json:"id"`
		LastTx    string `json:"last_tx"`
		Owner     string `json:"owner"`
		Tags      []Tag  `json:"tags"`
		Target    string `json:"target"`
		Quantity  string `json:"quantity"`
		Data      string `json:"data"`
		DataRoot  string `json:"data_root"`
		DataSize  string `json:"data_size"`
		Reward    string `json:"reward"`
		Signature string `json:"signature"`
	}{
		Format:    2,
		ID:        sig.ID,
		LastTx:    sig.LastTx,
		Owner:     sig.Owner,
		Tags:      tags,
		Target:    "",
		Quantity:  "0",
		Data:      base64.RawURLEncoding.EncodeToString(bundle),
		DataRoot:  sig.DataRoot,
		DataSize:  strconv.Itoa(len(bundle)),
		Reward:    sig.Reward,
		Signature: sig.Signature,
	}

	body, err := json.Marshal(tx)
	if err != nil {
		return "", err
	}

	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	url := strings.TrimRight(node, "/") + "/tx"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("提交交易失败 %s: %s", resp.Status, truncate(string(respBody), 300))
	}
	return sig.ID, nil
}
