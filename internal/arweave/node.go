package arweave

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// KnownNodes 是提交交易时可以选用的公共网关清单，顺序即偏好。
//
// 之所以需要一份清单：交易是先 POST 给网关，再由网关转发给节点，
// 这一跳选谁直接决定交易能不能到场。实测见过一个地址整段时间不响应
// （连 /info 都超时），而同一时刻另外几个一切正常。换一个地址就好，
// 与手续费无关——这也是检查网关比调 reward 更该先做的事。
//
// 只收实测能在 GET /info 上返回 200 与当前高度的地址。
// 加新地址之前先跑一次 `rog nodes`，把结果记在这里。
var KnownNodes = []string{
	DefaultNode,
	"https://ardrive.net",
	"https://permagate.io",
}

// NodeInfo 是我们从 GET /info 里关心的几个字段。
type NodeInfo struct {
	URL         string
	Height      int64
	QueueLength int64
	Network     string
	Latency     time.Duration
}

// Available 表示这次探测拿到了 200。
func (n *NodeInfo) Available() bool { return n != nil && n.Network != "" }

// ProbeNode 向一个网关要 /info，用它判断这个地址此刻能不能用。
//
// 这是「提交之前先看清出口」的手段：/info 只读、不花 AR、失败也不留痕，
// 可以随时跑。它证明不了「提交一定成功」，但能排除掉「这个出口根本不通」。
func ProbeNode(ctx context.Context, node string, client *http.Client) (*NodeInfo, error) {
	base := strings.TrimRight(strings.TrimSpace(node), "/")
	if base == "" {
		return nil, fmt.Errorf("节点地址为空")
	}
	client = clientOrDefault(client)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/info", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	latency := time.Since(start)

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &nodeError{Status: resp.StatusCode, StatusText: resp.Status, Body: string(body)}
	}

	// 字段名照节点的 /info 来。真正会看的是 height（确认在跟链）
	// 与 queue_length（待处理队列有多深）。
	var raw struct {
		Height      int64  `json:"height"`
		QueueLength int64  `json:"queue_length"`
		Network     string `json:"network"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析 /info 失败: %w", err)
	}
	return &NodeInfo{
		URL:         base,
		Height:      raw.Height,
		QueueLength: raw.QueueLength,
		Network:     raw.Network,
		Latency:     latency,
	}, nil
}

// NodeProbe 是一次探测的结果，可用的与不可用的都记。
//
// URL 与 Info 分开存：失败时 Info 是空的，但地址必须留住，
// 否则没法告诉用户是哪个网关不通。
type NodeProbe struct {
	URL  string
	Info *NodeInfo
	Err  error
}

// ProbeNodes 并发探测一批网关，按「可用的在前、快慢其次」排好序返回。
//
// 不通的也一并返回：哪些地址此刻不通，本身就是有用的信息。
func ProbeNodes(ctx context.Context, nodes []string, client *http.Client) []NodeProbe {
	if len(nodes) == 0 {
		return nil
	}
	probes := make([]NodeProbe, len(nodes))
	done := make(chan int, len(nodes))
	for i, n := range nodes {
		go func(i int, n string) {
			info, err := ProbeNode(ctx, n, client)
			probes[i] = NodeProbe{URL: n, Info: info, Err: err}
			done <- i
		}(i, n)
	}
	for range nodes {
		<-done
	}
	sort.SliceStable(probes, func(a, b int) bool {
		pa, pb := probes[a], probes[b]
		if (pa.Err == nil) != (pb.Err == nil) {
			return pa.Err == nil
		}
		if pa.Info == nil || pb.Info == nil {
			return false
		}
		return pa.Info.Latency < pb.Info.Latency
	})
	return probes
}
