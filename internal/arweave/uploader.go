// Package arweave 负责把站点发布到 Arweave。
//
// 分工：Go 侧准备内容与 tags、上传已签名的 data item；签名由浏览器钱包完成，
// 私钥不出钱包。完整 data item 的组装（header + deep hash）也交给钱包的
// signDataItem，Go 只处理两端：待签名的载荷，和签名后的字节。
package arweave

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint 是 Turbo 的上传服务地址。
const DefaultEndpoint = "https://turbo.ardrive.io"

// Uploader 把已签名的 data item 提交到上传服务。
type Uploader struct {
	Endpoint string
	Client   *http.Client
	// RetryBackoff 是重试的基础间隔，默认 2s。测试里可以调小。
	RetryBackoff time.Duration
}

// NewUploader 创建一个上传器；endpoint 为空时用默认服务。
func NewUploader(endpoint string) *Uploader {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultEndpoint
	}
	return &Uploader{
		Endpoint: endpoint,
		Client:   &http.Client{Timeout: 5 * time.Minute},
	}
}

// Upload 提交一个已签名的 data item，返回它的 id。
//
// 对限流与 5xx 做有限退避重试：重试的代价远小于丢掉一份已经签好名的数据，
// 后者下次运行时要重新签名并重新付费。
func (u *Uploader) Upload(ctx context.Context, dataItem []byte) (string, error) {
	if len(dataItem) == 0 {
		return "", fmt.Errorf("data item 为空")
	}

	const maxAttempts = 3
	backoff := u.RetryBackoff
	if backoff <= 0 {
		backoff = 2 * time.Second
	}
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * backoff):
			}
		}

		id, retryable, err := u.attempt(ctx, dataItem)
		if err == nil {
			return id, nil
		}
		lastErr = err
		if !retryable {
			return "", err
		}
	}
	return "", lastErr
}

// attempt 发一次请求；retryable 表示这个失败是否值得重试。
func (u *Uploader) attempt(ctx context.Context, dataItem []byte) (id string, retryable bool, err error) {
	url := strings.TrimRight(u.Endpoint, "/") + "/tx"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(dataItem))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", strconv.Itoa(len(dataItem)))

	resp, err := u.client().Do(req)
	if err != nil {
		// 网络层错误（连接中断、超时）通常值得重试
		return "", true, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", true, err
	}

	if resp.StatusCode/100 != 2 {
		return "", retryableStatus(resp.StatusCode),
			fmt.Errorf("上传失败 %s: %s", resp.Status, truncate(string(body), 200))
	}

	id = parseID(body)
	if id == "" {
		return "", false, fmt.Errorf("上传响应里没有 id: %s", truncate(string(body), 200))
	}
	return id, false, nil
}

// retryableStatus 判断哪些状态码属于「稍后再试可能成功」。
// 4xx 里只有 429 是暂时的，其余（400 等）重试多少次都一样。
func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

func (u *Uploader) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return http.DefaultClient
}

// parseID 从响应里取 data item id。字段名可能是 id 或 Id，两种都认。
func parseID(body []byte) string {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return ""
	}
	for _, k := range []string{"id", "Id", "ID"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// truncate 按字符数截断，避免切断多字节字符（错误信息是给人看的）。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
