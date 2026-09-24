package arweave

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	// DataSize 是这一包的字节数，与 DataRoot 一起描述这份数据。
	DataSize string `json:"data_size"`
	// Proofs 是各块的 Merkle 证明，按块序排列，只在需要分块时用得上。
	// 单块时 /tx 直接带 data，节点自己会算，用不着它。
	Proofs []ChunkProof `json:"proofs,omitempty"`
	// Uploaded 表示这份交易已经由签名页自己提交上链了。
	//
	// 署名与提交在同一边完成时，就不存在「两处拼出来的 JSON 是否等价」
	// 这个问题——这也正是把提交交给 arweave-js 的理由。
	// 为 true 时 Go 侧不重复提交，直接用 ID。
	Uploaded bool `json:"uploaded,omitempty"`
	// Status 是提交后节点给出的状态码（200/202 表示节点手里有它）。
	// 仅用于写日志：事后翻的时候这个值比什么都直接。
	Status int `json:"status,omitempty"`
	// PostStatus 是 POST /tx 那一刻节点回的状态码。
	// 与 Status 不同：那是「节点受理了吗」，这是「事后还查得到吗」。
	// 实测碰上过前者 2xx、后者 404 的情况，两个都得看。
	PostStatus int `json:"postStatus,omitempty"`
	// PostBody 是 POST /tx 的响应原话（截断）。
	// 节点拒绝或丢弃时，原因就写在这里。
	PostBody string `json:"postBody,omitempty"`
	// Chunks 是这一包会被切成多少块（页面算的）。
	//
	// 大于 1 时页面不提交，只把 proofs 回传过来，由 Go 走
	// /tx → 逐块 /chunk。写进日志是为了能一眼看出这一包走了哪条路：
	// 单块的毛病与多块的毛病完全不同。
	Chunks int `json:"chunkCount,omitempty"`
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
	body, err := txPayload(bundle, tags, sig)
	if err != nil {
		return "", err
	}

	if _, err := postJSON(ctx, node, "/tx", body, client); err != nil {
		return "", err
	}
	return sig.ID, nil
}

// txPayload 拼出交易 JSON 的字节。
//
// data 传 nil 时 data 字段为空串，用于分块提交的第一步：
// 先把交易报上去，内容再逐块补。
func txPayload(data []byte, tags []Tag, sig *TxSignature) ([]byte, error) {
	// data_size 以页面回传的为准，缺失时用实际长度兜底。
	dataSize := DataSizeOf(sig, data)

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
		Tags:      encodeTxTags(tags),
		Target:    "",
		Quantity:  "0",
		Data:      base64.RawURLEncoding.EncodeToString(data),
		DataRoot:  sig.DataRoot,
		DataSize:  dataSize,
		Reward:    sig.Reward,
		Signature: sig.Signature,
	}
	return json.Marshal(tx)
}

// encodeTxTags 把 tags 编成交易 JSON 里的形式。
//
// 这里反直觉：交易 JSON 里的 tags **不是明文**，name 与 value 都要 base64url。
// 根据是 arweave-js 的实现：addTag 先编码再存
//
//	this.tags.push(new Tag(stringToB64Url(name), stringToB64Url(value)))
//
// 而 toJSON() 原样输出这份内部表示。节点按 base64url 解，
// 发明文会被它解成乱码并直接拒掉（报的就是 Invalid JSON）。
//
// 注意只在交易 JSON 里编码。data item（ANS-104）的 tags 是明文 UTF-8，
// 那一条路走钱包的 signDataItem，两者不是一回事，不要一起改。
//
// tags 为空时返回空切片而不是 nil：节点对 `"tags":null` 也不客气。
func encodeTxTags(tags []Tag) []Tag {
	out := make([]Tag, 0, len(tags))
	for _, t := range tags {
		out = append(out, Tag{
			Name:  base64.RawURLEncoding.EncodeToString([]byte(t.Name)),
			Value: base64.RawURLEncoding.EncodeToString([]byte(t.Value)),
		})
	}
	return out
}

// nodeError 是节点返回的非 2xx 响应。
//
// 单独建一个类型是为了让重试逻辑能按状态码判断，
// 而不是去字符串里找线索。
type nodeError struct {
	Status     int
	StatusText string
	Body       string
}

func (e *nodeError) Error() string {
	return fmt.Sprintf("节点返回 %s: %s", e.StatusText, truncate(e.Body, 300))
}

// postJSON 往节点发一个 JSON 请求。
//
// 非 2xx 时同样返回响应体，调用方可以用它判断是否值得重试。
func postJSON(ctx context.Context, node, path string, body []byte, client *http.Client) ([]byte, error) {
	client = clientOrDefault(client)
	url := strings.TrimRight(node, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return respBody, &nodeError{
			Status:     resp.StatusCode,
			StatusText: resp.Status,
			Body:       string(respBody),
		}
	}
	return respBody, nil
}
