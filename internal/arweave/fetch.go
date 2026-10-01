package arweave

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	urlpkg "net/url"
	"strings"

	"github.com/LWDJD/read-only-git/internal/publish"
)

// DefaultGateway 是读取内容用的网关。
const DefaultGateway = "https://arweave.net"

// FetchRecord 从网关取回一份发布记录。
//
// entry 是入口 manifest 的 id，relPath 是记录在站点内的相对路径
// （见 publish.RecordRelPath）。网关会把 <入口id>/<路径> 解析到对应的
// data item，所以取回记录不需要额外的指针。
func FetchRecord(ctx context.Context, gateway, entry, relPath string) (*publish.Record, error) {
	return FetchRecordWithClient(ctx, gateway, entry, relPath, nil)
}

// FetchRecordWithClient 同 FetchRecord，但可以指定出口。
//
// client 为 nil 时按系统代理造一个，与不配置时期望的行为一致。
func FetchRecordWithClient(ctx context.Context, gateway, entry, relPath string, client *http.Client) (*publish.Record, error) {
	if strings.TrimSpace(entry) == "" {
		return nil, fmt.Errorf("入口 id 不能为空")
	}
	if strings.TrimSpace(gateway) == "" {
		gateway = DefaultGateway
	}

	url := strings.TrimRight(gateway, "/") + "/" + urlpkg.PathEscape(strings.TrimLeft(entry, "/")) + "/" + escapeRelPath(relPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := clientOrDefault(client).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if len(body) >= 8<<20 {
		return nil, fmt.Errorf("发布记录超过 8 MiB，拒绝解析")
	}
	if resp.StatusCode != http.StatusOK {
		// 刚发布的 manifest 要等 bundle 落链后网关才解析得到。这里点明这一点，
		// 否则只看到 404 会以为是路径写错了。
		return nil, fmt.Errorf("取记录失败 %s: %s（入口可能还没被网关索引，稍后再试）",
			resp.Status, truncate(string(body), 200))
	}

	var rec publish.Record
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, fmt.Errorf("发布记录格式不对: %w", err)
	}
	if rec.Files == nil {
		rec.Files = map[string]string{}
	}
	if rec.Refs == nil {
		rec.Refs = map[string]string{}
	}
	// 链上那份的 Root 是空的（写它的时候入口 id 还不存在），这里补上调用方给的值。
	rec.Root = entry
	return &rec, nil
}

// escapeRelPath 把相对路径按段转义后拼回，防止 .. / ? # 之类改写请求路径。
func escapeRelPath(rel string) string {
	parts := strings.Split(strings.TrimLeft(rel, "/"), "/")
	for i, p := range parts {
		parts[i] = urlpkg.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
