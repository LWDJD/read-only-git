package arweave

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// RestorePlan 是从链上恢复之前的勘察结果。
//
// 先取回 manifest、把路径映射理清楚，再决定要不要往磁盘上落东西。
// 顺序这样安排的理由：目标目录一旦开始写就不该半途而废，
// 而 manifest 取不到是常见情形（入口还没被网关索引），
// 这种「还没开始就能知道会失败」的错，不该等到写了一半个文件才发现。
type RestorePlan struct {
	Entry     string
	IndexPath string            // manifest 里指定的默认入口，可能为空
	Paths     map[string]string // 站点内路径 -> data item id
}

// FetchManifest 取回入口指向的 path manifest。
func FetchManifest(ctx context.Context, gateway, entry string, client *http.Client) (*RestorePlan, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil, fmt.Errorf("入口 id 不能为空")
	}
	if strings.TrimSpace(gateway) == "" {
		gateway = DefaultGateway
	}

	body, err := fetchBytes(ctx, gateway, entry, client, 8<<20)
	if err != nil {
		return nil, fmt.Errorf("取入口失败: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("入口不是一份 manifest（%w）；"+
			"这里要填发布时给出的入口 id，不是交易 id", err)
	}
	if m.Manifest != "arweave/paths" {
		return nil, fmt.Errorf("入口的 manifest 字段是 %q，期望 arweave/paths", m.Manifest)
	}
	if len(m.Paths) == 0 {
		return nil, fmt.Errorf("manifest 里没有任何路径")
	}

	plan := &RestorePlan{
		Entry: entry,
		Paths: make(map[string]string, len(m.Paths)),
	}
	if m.Index != nil {
		plan.IndexPath = m.Index.Path
	}
	for p, e := range m.Paths {
		plan.Paths[p] = e.ID
	}
	return plan, nil
}

// RestoreInto 按 plan 把内容取回并写到 dest。
//
// dest 必须是「空目录或不存在」：已经清空、或有东西，都不动它，直接报错。
// 理由是这个动作会把文件铺满一整个目录，如果目标里原本有东西，
// 要么覆盖掉别人的工作、要么混出一份半新半旧的结果，两种都不该悄悄发生。
// 清空目录这种事交给用户自己做——他知道那里原来是什么。
func RestoreInto(ctx context.Context, gateway string, plan *RestorePlan, dest string,
	client *http.Client, logf func(string, ...any)) (int, error) {

	if plan == nil || len(plan.Paths) == 0 {
		return 0, fmt.Errorf("没有可恢复的路径")
	}
	if strings.TrimSpace(gateway) == "" {
		gateway = DefaultGateway
	}
	abs, err := filepath.Abs(strings.TrimSpace(dest))
	if err != nil {
		return 0, err
	}
	if err := checkDirEmpty(abs); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return 0, err
	}

	// 排好序再取：日志里看起来是有序的，出问题时好定位到具体哪个文件。
	names := make([]string, 0, len(plan.Paths))
	for p := range plan.Paths {
		names = append(names, p)
	}
	sort.Strings(names)

	var done int
	for _, rel := range names {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		target, err := safeRestorePath(abs, rel)
		if err != nil {
			return done, err
		}

		body, err := fetchBytes(ctx, gateway, plan.Paths[rel], client, 64<<20)
		if err != nil {
			return done, fmt.Errorf("取 %s 失败: %w", rel, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return done, err
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return done, err
		}
		done++
		if logf != nil {
			logf("取回 %d/%d：%s（%d 字节）", done, len(names), rel, len(body))
		}
	}
	return done, nil
}

// checkDirEmpty 确认目标目录不存在、或者是空的。
//
// 只看「有没有东西」而不看「有没有权限」：写权限的问题留到真写的时候
// 由系统报错带出来，那时错误信息更具体。
func checkDirEmpty(abs string) error {
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("看不了目标目录 %s: %w", abs, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("目标目录不是空的（%d 项）；请自己清空，或换一个空目录", len(entries))
	}
	return nil
}

// safeRestorePath 把 manifest 里的路径接到目标目录下，挡住越界。
//
// manifest 是从链上取回来的，属于外部输入，不能假定它里面的路径是乖的。
func safeRestorePath(root, rel string) (string, error) {
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, `\`, "/"), "/")
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("manifest 里的路径越界: %q", rel)
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}

// fetchBytes 从网关取一段原始内容。
//
// 一律走 /raw/<id>，不走 /<id>。后者对 manifest 会做一层解析：
// 直接把 index 指向的那个文件吐出来（实测过，拿到的是一段 HTML），
// 于是「取 manifest」就会失败在解 JSON 上。/raw/ 不做这层解析。
//
// limit 是读的上限：manifest 与单个文件都不该是无限的，
// 给它一个封顶值，免得被一个坏响应喂爆内存。
func fetchBytes(ctx context.Context, gateway, id string, client *http.Client, limit int64) ([]byte, error) {
	url := strings.TrimRight(gateway, "/") + "/raw/" + strings.TrimLeft(id, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := clientOrDefault(client).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// 刚发布的入口要等 bundle 落链后网关才解析得到。点明这一点，
		// 否则只看到 404 会以为是 id 抄错了。
		return nil, fmt.Errorf("网关返回 %s: %s（刚发布的入口可能需要等一会儿才可读）",
			resp.Status, truncate(string(body), 200))
	}
	return body, nil
}
