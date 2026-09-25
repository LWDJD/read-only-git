package arweave

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/LWDJD/read-only-git/internal/publish"
)

// 发布之后的手动核对：逐个引用问网关「这条还在不在、内容对不对」，
// 查出问题只报告，不自动动手。补文件的动作是清掉坏引用后跑一次正常发布，
// 不在这里造第二条上传路径——签名、计费、manifest 都该走已验证的链路。
//
// 为什么要有它：复用判断只看「记录里有 id」，而 id 在链上是否真的可读
// 没人查过。记录说有、链上没有的文件会被增量发布永久跳过，站点永远缺一块，
// 而且没有任何机制发现。这里就是那个机制。

// Verdict 是一个引用的核对结论。
type Verdict string

const (
	// VerdictOK 可读（开了摘要核对时内容也一致）。
	//
	// 一个网关读到就算：Arweave 上数据在链上即永久，个别网关读不到
	// 只是那一个网关的索引问题，不影响「上传成功」这个事实。
	VerdictOK Verdict = "ok"
	// VerdictMissing 能问到的网关都说 404：链上大概率真缺。
	VerdictMissing Verdict = "missing"
	// VerdictMismatch 读到了，但内容摘要全都与记录不符：内容真有问题。
	VerdictMismatch Verdict = "mismatch"
	// VerdictUnreachable 网关都连不上，问不出结论。
	//
	// 单独一档是必须的：「网关不通」混进 missing 会让 repair 把好引用删掉，
	// 那正是核对工具最不该犯的错。
	VerdictUnreachable Verdict = "unreachable"
)

// GatewayResult 是单个网关对单个引用的回答。
type GatewayResult struct {
	Gateway string
	OK      bool   // 读到了吗
	Status  int    // HTTP 状态码；0 表示网络层就没连上
	Digest  string // 开了摘要核对时算出的 sha256（十六进制）
	Err     string // 状态码问题或网络问题的说明
}

// Unreachable 报告这次失败是不是网络层的（连不上/超时），而不是网关的回答。
func (g GatewayResult) Unreachable() bool { return !g.OK && g.Status == 0 }

// ItemCheck 是一个引用的核对结果。
type ItemCheck struct {
	Path    string
	ID      string
	Verdict Verdict
	Detail  string
	Per     []GatewayResult
}

// CheckReport 是一次核对的完整报告。
type CheckReport struct {
	Entry    string
	EntryOK  bool
	EntryMsg string

	Items []ItemCheck

	OK          int
	Missing     int
	Mismatch    int
	Unreachable int
	// GatewayDiff 是 OK 里那些各网关表现不一致的条数（有的读不到、
	// 或有的内容怪），只作备注，不影响结论。
	GatewayDiff int

	// Repaired 是 repair 清掉了多少条坏引用。
	Repaired int
	// NewRoot 是 repair 后该用的入口（入口本身失效时为空）。
	NewRoot string

	Started  time.Time
	Duration time.Duration
}

// CheckOptions 是一次核对的输入。
type CheckOptions struct {
	Record   *publish.Record
	Gateways []string
	Client   *http.Client
	// CheckContent 取回内容算摘要与记录比对（默认该开：宁慢勿错）。
	CheckContent bool
	// Repair 为真时把 missing/mismatch 的引用从记录里清掉。
	// 检查本身不受影响，报告永远给全量结果。
	Repair bool
	Logf   func(format string, args ...any)
}

// 每个请求的超时与整体并发度。
//
// 核对是「问一堆小问题」的场景，单个请求不该拖住整轮；
// 并发度压在 8：对网关温和，几十个文件也在秒级跑完。
const (
	checkRequestTimeout = 30 * time.Second
	checkConcurrency    = 8
)

// CheckSite 逐个引用核对链上可读性。
//
// 多网关并行：同一条引用在一个网关读不到不等于链上没有，
// 「能问到的网关都说 404」才判 missing；部分读到判 partial（疑似索引未完成）；
// 网关根本连不上时判 unreachable，绝不与 missing 混淆。
func CheckSite(ctx context.Context, opt CheckOptions) (*CheckReport, error) {
	if opt.Record == nil {
		return nil, fmt.Errorf("没有发布记录可核对")
	}
	gateways := make([]string, 0, len(opt.Gateways))
	for _, g := range opt.Gateways {
		g = strings.TrimRight(strings.TrimSpace(g), "/")
		if g != "" {
			gateways = append(gateways, g)
		}
	}
	if len(gateways) == 0 {
		gateways = append(gateways, DefaultGateway)
	}

	// 网关预检：先问一遍 /info，连不上的直接标出来并剔除，
	// 不带着死网关跑完全场（逐文件问它只会把每一条都拖到超时）。
	probeCtx, cancel := context.WithTimeout(ctx, checkRequestTimeout)
	live := make([]string, 0, len(gateways))
	for _, g := range gateways {
		if _, err := ProbeNode(probeCtx, g, opt.Client); err != nil {
			if opt.Logf != nil {
				opt.Logf("网关 %s 不可达，本次跳过：%v", g, err)
			}
			continue
		}
		live = append(live, g)
	}
	cancel()
	if len(live) == 0 {
		return nil, fmt.Errorf("所有网关都连不上（试过 %s），查不出结论；检查网络出口或代理设置",
			strings.Join(gateways, "、"))
	}
	gateways = live
	logf := opt.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	rep := &CheckReport{Entry: opt.Record.Root, Started: time.Now()}
	defer func() { rep.Duration = time.Since(rep.Started) }()

	// 一、入口 manifest。
	if opt.Record.Root == "" {
		rep.EntryOK = false
		rep.EntryMsg = "记录里没有入口 id"
		logf("入口缺失：记录里没有入口 id")
	} else {
		plan, err := FetchManifest(ctx, gateways[0], opt.Record.Root, opt.Client)
		if err != nil {
			rep.EntryOK = false
			rep.EntryMsg = err.Error()
			logf("入口 %s 不可读：%v", shortID(opt.Record.Root), err)
		} else {
			rep.EntryOK = true
			rep.EntryMsg = fmt.Sprintf("%d 个路径", len(plan.Paths))
			logf("入口 %s 可读（%d 个路径）", shortID(opt.Record.Root), len(plan.Paths))
		}
	}

	// 二、逐个引用，并发问网关。
	paths := make([]string, 0, len(opt.Record.Refs))
	for p := range opt.Record.Refs {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var mu sync.Mutex
	sem := make(chan struct{}, checkConcurrency)
	var wg sync.WaitGroup

	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			item := ItemCheck{Path: p, ID: opt.Record.Refs[p]}
			item.Per = fetchAllGateways(ctx, opt.Client, gateways, item.ID, opt.CheckContent)
			item.Verdict, item.Detail = judgeItem(item, opt.Record.Files[p], opt.CheckContent)

			mu.Lock()
			defer mu.Unlock()
			switch item.Verdict {
			case VerdictOK:
				rep.OK++
				if item.Detail != "" {
					rep.GatewayDiff++
				}
			case VerdictMissing:
				rep.Missing++
			case VerdictMismatch:
				rep.Mismatch++
			case VerdictUnreachable:
				rep.Unreachable++
			}
			rep.Items = append(rep.Items, item)
			logf("  %-10s %s（%s）", item.Verdict, p, shortID(item.ID))
		}(p)
	}
	wg.Wait()

	// 报告顺序要稳定：路径排序，与完成先后无关。
	sort.Slice(rep.Items, func(i, j int) bool { return rep.Items[i].Path < rep.Items[j].Path })

	// 三、repair：把坏引用从记录里清掉。清完走正常增量发布即可补上。
	if opt.Repair {
		n := RepairRecord(opt.Record, rep)
		rep.Repaired = n
		if !rep.EntryOK {
			// 入口本身失效时连 Root 一起清，下次发布会重新生成 manifest
			opt.Record.Root = ""
			rep.NewRoot = ""
		} else {
			rep.NewRoot = opt.Record.Root
		}
		logf("已清掉 %d 条坏引用；跑一次正常发布即可补上缺失的文件", n)
	}

	return rep, nil
}

// fetchAllGateways 并发问所有网关。
func fetchAllGateways(ctx context.Context, client *http.Client, gateways []string, id string, checkContent bool) []GatewayResult {
	out := make([]GatewayResult, len(gateways))
	var wg sync.WaitGroup
	for i, gw := range gateways {
		wg.Add(1)
		go func(i int, gw string) {
			defer wg.Done()
			gr := GatewayResult{Gateway: gw}
			digest, status, err := fetchDigest(ctx, client, gw, id, checkContent)
			gr.Status = status
			if err != nil {
				gr.Err = err.Error()
			} else {
				gr.OK = true
				gr.Digest = digest
			}
			out[i] = gr
		}(i, gw)
	}
	wg.Wait()
	return out
}

// RepairRecord 从记录里删掉 report 里判为 missing/mismatch 的引用，返回删了几条。
//
// 只动记录不动链上：补传的机制就是增量发布——记录里没有的 id 会被重新
// 签名、重新上传。这样不存在「第二条上传路径」，也就没有它的流程风险。
//
// partial 与 unreachable 一律不清：前者是索引问题，后者是网络问题，
// 清了会让已经付过费的内容重新上传一遍。
func RepairRecord(rec *publish.Record, rep *CheckReport) int {
	if rec == nil || rep == nil {
		return 0
	}
	n := 0
	for _, item := range rep.Items {
		if item.Verdict != VerdictMissing && item.Verdict != VerdictMismatch {
			continue
		}
		if _, ok := rec.Refs[item.Path]; ok {
			delete(rec.Refs, item.Path)
			delete(rec.Files, item.Path)
			n++
		}
	}
	return n
}

// fetchDigest 取一个引用。
//
// checkContent 为真时读全文算 sha256；否则只确认可读（读一个字节就走），
// 不为了「在不在」把整个站点下载两遍。status 为 0 表示网络层就没连上，
// 与「网关回答 404」是两回事，判定时必须分开。
func fetchDigest(ctx context.Context, client *http.Client, gateway, id string, checkContent bool) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, checkRequestTimeout)
	defer cancel()

	url := strings.TrimRight(gateway, "/") + "/raw/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := clientOrDefault(client).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return "", resp.StatusCode, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 120))
	}
	if !checkContent {
		if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1)); err != nil {
			return "", resp.StatusCode, err
		}
		return "", resp.StatusCode, nil
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(resp.Body, 256<<20)); err != nil {
		return "", resp.StatusCode, err
	}
	return hex.EncodeToString(h.Sum(nil)), resp.StatusCode, nil
}

// judgeItem 把各网关的回答聚成一条结论。
//
// 判定口径：一个网关读到且内容对就算成功。Arweave 上数据在链上即永久，
// 别的网关读不到、或回了怪东西，都是那一个网关自己的问题，
// 不能反过来把成功标成可疑——那只会让人白担心。
func judgeItem(item ItemCheck, wantDigest string, checkContent bool) (Verdict, string) {
	okGood := 0 // 读到且内容对
	okBad := 0  // 读到但内容不对
	reachable := 0
	missing := 0
	var netErrs []string

	for _, g := range item.Per {
		if g.Unreachable() {
			netErrs = append(netErrs, g.Gateway+": "+g.Err)
			continue
		}
		reachable++
		if !g.OK {
			missing++
			continue
		}
		if checkContent && wantDigest != "" && g.Digest != wantDigest {
			okBad++
		} else {
			okGood++
		}
	}

	switch {
	case reachable == 0:
		return VerdictUnreachable, "网关都连不上，无法判断：" + strings.Join(netErrs, "; ")
	case okGood+okBad == 0:
		return VerdictMissing, fmt.Sprintf("%d 个网关都说不存在", missing)
	case okGood > 0:
		// 至少一份读到且内容对：链上就是好的。网关差异只作备注。
		detail := ""
		if okBad > 0 || missing > 0 {
			detail = fmt.Sprintf("%d/%d 个网关正常", okGood, len(item.Per))
		}
		return VerdictOK, detail
	default:
		// 读到的都对不上：内容真有问题
		return VerdictMismatch, "读到的网关内容摘要都与记录不符"
	}
}

// MarshalReport 序列化报告（webui 结果展示用）。
func MarshalReport(rep *CheckReport) (json.RawMessage, error) {
	b, err := json.Marshal(rep)
	return json.RawMessage(b), err
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12] + "…"
	}
	return id
}
