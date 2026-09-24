// Command rog 是 read-only-git 的维护器。
//
// 当前职责：把 git 仓库转换成可直接静态托管的裸仓库。
// 后续会接上 Arweave 上传与 ENS 记录更新。
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/LWDJD/read-only-git/internal/arweave"
	"github.com/LWDJD/read-only-git/internal/publish"
	"github.com/LWDJD/read-only-git/internal/repopack"
	"github.com/LWDJD/read-only-git/internal/signer"
	"github.com/LWDJD/read-only-git/internal/sitekit"
	"github.com/LWDJD/read-only-git/internal/webui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "\nx %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage(os.Stderr)
		return fmt.Errorf("缺少子命令")
	}

	switch args[0] {
	case "pack":
		return cmdPack(args[1:])
	case "site":
		return cmdSite(args[1:])
	case "publish":
		return cmdPublish(args[1:])
	case "nodes":
		return cmdNodes(args[1:])
	case "webui":
		return cmdWebui(args[1:])
	case "help", "-h", "--help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("未知子命令: %s", args[0])
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "rog - read-only-git 维护器")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "用法: rog <子命令> [参数]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "子命令:")
	fmt.Fprintln(w, "  site init [目录] [--template <id>] [--force]   铺开站点骨架（前端文件）")
	fmt.Fprintln(w, "  site list                                    列出内置模板")
	fmt.Fprintln(w, "  pack [--rebuild] <源仓库> [输出目录] [仓库名]  生成可托管的裸仓库")
	fmt.Fprintln(w, "  publish <站点目录> [目标目录]                 发布到本地目录")
	fmt.Fprintln(w, "  publish <站点目录> --arweave [选项]            发布到 Arweave（钱包签名）")
	fmt.Fprintln(w, "  nodes [地址…]                                探测网关，看发布时该填哪个 --node")
	fmt.Fprintln(w, "  webui [--site <站点目录>] [--port <端口>]      打开图形界面，功能与命令行一致")
	fmt.Fprintln(w, "  help                                         显示本说明")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "site 的选项:")
	fmt.Fprintln(w, "  init [目录]         把前端模板写进去，默认 ./public")
	fmt.Fprintln(w, "  --template <id>     用哪套模板，默认 default")
	fmt.Fprintln(w, "  --force             覆盖已存在的文件；默认只补缺失的")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "pack 的参数:")
	fmt.Fprintln(w, "  --rebuild  忽略已有产物，从零重建；默认自动（有旧产物就增量）")
	fmt.Fprintln(w, "  --proxy <地址>  拉远端仓库时用的代理，如 http://127.0.0.1:7890")
	fmt.Fprintln(w, "  <源仓库>   必填。本地路径（普通或裸仓库），或远端地址")
	fmt.Fprintln(w, "  [输出目录] 默认 ./public，站点根目录")
	fmt.Fprintln(w, "  [仓库名]   默认从源推导；写成带 .git 的也会被归一化掉")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "publish 的选项:")
	fmt.Fprintln(w, "  --arweave          发布到 Arweave，会起本地签名页等钱包签名")
	fmt.Fprintln(w, "  --l1               走 L1：把内容打成一个 ANS-104 包，签一笔交易直接提交")
	fmt.Fprintln(w, "  --node <地址>      L1 提交用的节点，默认 arweave.net")
	fmt.Fprintln(w, "  --repo <名字>      写进 data item 的 Repo 标签")
	fmt.Fprintln(w, "  --endpoint <地址>  上传服务，默认 turbo.ardrive.io（非 L1 时使用）")
	fmt.Fprintln(w, "  --from <入口 id>   从链上取回上次的发布记录，续上增量能力")
	fmt.Fprintln(w, "  --gateway <地址>   读取用的网关，默认 arweave.net")
	fmt.Fprintln(w, "  --proxy <模式>     网络出口：system（默认，跟随系统设置）/ manual / off")
	fmt.Fprintln(w, "  --proxy-url <地址> manual 模式下的代理，如 http://127.0.0.1:7890")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "publish 会复用上一次的发布记录（存在 <站点目录>/.rog/ 下），")
	fmt.Fprintln(w, "只处理内容变化的文件。记录本身也会随站点上链，")
	fmt.Fprintln(w, "换机器时用 --from 就能取回来。")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "webui 的选项:")
	fmt.Fprintln(w, "  --site <目录>   默认操作的站点目录，默认 ./public")
	fmt.Fprintln(w, "  --port <端口>   固定监听端口，默认由系统分配一个空闲的")
}

func cmdSite(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		usage(os.Stdout)
		return nil
	}
	if len(args) == 0 {
		usage(os.Stderr)
		return fmt.Errorf("用法: rog site init [目录] [--template <id>] [--force]，或 rog site list")
	}

	switch args[0] {
	case "list":
		for _, tpl := range sitekit.Templates() {
			fmt.Printf("%-10s %s（%d 个文件）\n", tpl.ID, tpl.Name, tpl.Files)
			fmt.Printf("           %s\n", tpl.Description)
		}
		return nil

	case "init":
		dir := "public"
		templateID := "default"
		force := false
		var pos []string

		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--force", "-f":
				force = true
			case "--template", "-t":
				if i+1 >= len(args) {
					return fmt.Errorf("--template 后面缺少值")
				}
				i++
				templateID = args[i]
			default:
				if strings.HasPrefix(args[i], "-") {
					return fmt.Errorf("未知开关: %s", args[i])
				}
				pos = append(pos, args[i])
			}
		}
		if len(pos) > 0 {
			dir = pos[0]
		}
		return cmdSiteInit(dir, templateID, force)

	default:
		return fmt.Errorf("未知的 site 子命令: %s（可选 init / list）", args[0])
	}
}

// cmdSiteInit 把嵌在二进制里的前端骨架铺到目录里。
//
// 有了它，光一个 exe 就能从零立起站点：先 site init 铺前端，再 pack 写仓库。
func cmdSiteInit(dir, templateID string, force bool) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	written, err := sitekit.Materialize(templateID, abs, force)
	if err != nil {
		return err
	}

	if len(written) == 0 {
		fmt.Printf("骨架已经齐了，%s 没有改动\n", abs)
		return nil
	}

	fmt.Printf("写入 %d 个文件到 %s\n", len(written), abs)
	for _, rel := range written {
		fmt.Printf("  %s\n", rel)
	}

	missing, err := sitekit.Missing(templateID, abs)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		fmt.Printf("\n注意：还缺 %d 个文件（多半是被改过或删掉了）\n", len(missing))
		for _, rel := range missing {
			fmt.Printf("  %s\n", rel)
		}
		fmt.Println("用 --force 可以把它们补回来（会覆盖同名文件）")
	}

	fmt.Println()
	fmt.Printf("下一步：rog pack <源仓库> %s\n", dir)
	return nil
}

func cmdPublish(args []string) error {
	// 先把开关挑出来，剩下的按位置参数处理。
	toArweave := false
	repo := ""
	endpoint := ""
	fromEntry := ""
	gateway := ""
	useL1 := false
	node := ""
	proxyMode := ""
	proxyURL := ""
	var pos []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			usage(os.Stdout)
			return nil
		case "--arweave":
			toArweave = true
		case "--repo":
			if i+1 >= len(args) {
				return fmt.Errorf("--repo 后面缺少值")
			}
			i++
			repo = args[i]
		case "--endpoint":
			if i+1 >= len(args) {
				return fmt.Errorf("--endpoint 后面缺少值")
			}
			i++
			endpoint = args[i]
		case "--from":
			if i+1 >= len(args) {
				return fmt.Errorf("--from 后面缺少值")
			}
			i++
			fromEntry = args[i]
		case "--proxy":
			if i+1 >= len(args) {
				return fmt.Errorf("--proxy 后面缺少值")
			}
			i++
			proxyMode = args[i]
		case "--proxy-url":
			if i+1 >= len(args) {
				return fmt.Errorf("--proxy-url 后面缺少值")
			}
			i++
			proxyURL = args[i]
		case "--gateway":
			if i+1 >= len(args) {
				return fmt.Errorf("--gateway 后面缺少值")
			}
			i++
			gateway = args[i]
		case "--l1":
			useL1 = true
		case "--node":
			if i+1 >= len(args) {
				return fmt.Errorf("--node 后面缺少值")
			}
			i++
			node = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("未知开关: %s", args[i])
			}
			pos = append(pos, args[i])
		}
	}

	if len(pos) == 0 {
		usage(os.Stderr)
		return fmt.Errorf("缺少站点目录")
	}

	if toArweave {
		return cmdPublishArweave(pos[0], repo, endpoint, fromEntry, gateway, useL1, node,
			arweave.ProxyConfig{Mode: arweave.ProxyMode(proxyMode), URL: proxyURL})
	}

	if len(pos) < 2 {
		return fmt.Errorf("用法: rog publish <站点目录> <目标目录>，或 rog publish <站点目录> --arweave")
	}
	return cmdPublishLocal(pos[0], pos[1])
}

func cmdPublishLocal(siteDir, destDir string) error {
	site, err := publish.Scan(siteDir)
	if err != nil {
		return err
	}
	if len(site.Files) == 0 {
		return fmt.Errorf("%s 里没有可发布的文件", siteDir)
	}

	target := &publish.Local{
		Dir:  destDir,
		Logf: func(format string, a ...any) { fmt.Printf("  "+format+"\n", a...) },
	}

	statePath := publish.StatePath(site.Root, target.Name(), destDir)
	prev, err := publish.LoadRecord(statePath)
	if err != nil {
		// 记录损坏只意味着复用信息丢失，按首次发布处理即可。
		fmt.Fprintf(os.Stderr, "! %v（按首次发布处理）\n", err)
		prev = nil
	}

	fmt.Printf("站点   %s（%d 个文件，%s）\n", site.Root, len(site.Files), humanSize(site.TotalSize()))
	fmt.Printf("目标   %s\n", destDir)
	// 有记录但没有引用，说明上次连第一个文件都没成，仍按首次发布表述。
	if prev == nil || len(prev.Refs) == 0 {
		fmt.Println("> 首次发布")
	} else {
		fmt.Printf("> 增量发布，%d 个文件有变化\n", len(publish.Changed(site, prev)))
	}

	rec, err := target.Publish(context.Background(), site, prev)
	// 即使中途失败也要保存记录：已经完成的文件在里面有引用，
	// 下次运行能直接复用，不会为已付费的内容再付一次。
	if rec != nil {
		if saveErr := publish.SaveRecord(statePath, rec); saveErr != nil {
			fmt.Fprintf(os.Stderr, "! 记录保存失败: %v\n", saveErr)
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "> 已保存部分记录，下次可复用其中 %d 个文件\n", len(rec.Refs))
		}
	}
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("v 完成，入口 %s\n", rec.Root)
	fmt.Printf("v 记录写入 %s\n", statePath)
	return nil
}

// cmdPublishArweave 把站点发布到 Arweave。
//
// 签名在浏览器钱包里完成，所以这里要起一个本机服务、等用户在页面里签完。
// 流程是阻塞的：Target.Publish 内部会等每一次签名回来才继续。
//
// 两条路：默认逐个把 data item 交给上传服务；useL1 时攒成一包，
// 让钱包签一笔以该包为 data 的交易，直接提交到节点，不经过任何打包服务。
func cmdPublishArweave(siteDir, repo, endpoint, fromEntry, gateway string, useL1 bool, node string,
	proxy arweave.ProxyConfig) error {
	site, err := publish.Scan(siteDir)
	if err != nil {
		return err
	}
	if len(site.Files) == 0 {
		return fmt.Errorf("%s 里没有可发布的文件", siteDir)
	}
	if repo == "" {
		repo = filepath.Base(site.Root)
	}

	// 一个 client 贯穿整轮发布：报交易、逐块 /chunk、取记录都走它。
	// 分开造的话，代理设置很容易只对其中几步生效，
	// 而失败的那一步往往正好是没生效的那一步。
	mode, err := arweave.ParseProxyMode(string(proxy.Mode))
	if err != nil {
		return err
	}
	if mode == arweave.ProxyManual && proxy.URL == "" {
		return fmt.Errorf("手动代理模式需要 --proxy-url")
	}
	client, err := arweave.NewClient(arweave.ProxyConfig{Mode: mode, URL: proxy.URL}, 0)
	if err != nil {
		return err
	}

	svc := signer.New([]byte(signer.DefaultPage))
	if err := svc.Start(); err != nil {
		return err
	}
	defer svc.Close()

	if node == "" {
		node = arweave.DefaultNode
	}

	uploader := arweave.NewUploaderWithClient(endpoint, client)
	// 记录身份用仓库名而不是上传端点：data item id 是内容寻址的，
	// 换一个端点，同一份内容仍然是同一个 id，用端点分键只会白白重传一遍。
	statePath := publish.StatePath(site.Root, "arweave", repo)

	prev, err := publish.LoadRecord(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "! %v（按首次发布处理）\n", err)
		prev = nil
	}

	// --from：把链上那份发布记录取回来，续上增量能力。
	// 换机器、本地 .rog 丢了之后走这条路，不必从零重传。
	if fromEntry != "" {
		fetched, ferr := arweave.FetchRecordWithClient(context.Background(), gateway, fromEntry,
			publish.RecordRelPath("arweave", repo), client)
		if ferr != nil {
			return ferr
		}
		if err := publish.SaveRecord(statePath, fetched); err != nil {
			return err
		}
		fmt.Printf("v 已从链上取回发布记录，含 %d 个文件引用\n", len(fetched.Refs))
		prev = fetched
	}

	target := &arweave.Target{
		Repo:       repo,
		Signer:     svc,
		RecordPath: publish.RecordRelPath("arweave", repo),
		// 签好但没提交成功的交易落在这里，重试时直接复用，
		// 不必再让用户去钱包里点一次。
		PendingPath: publish.PendingPath(site.Root, "arweave", repo),
		Client:      client,
		Logf: func(format string, a ...any) {
			fmt.Printf("  "+format+"\n", a...)
		},
	}
	if useL1 {
		target.TxSigner = svc
		target.Node = node
	} else {
		target.Uploader = uploader
	}

	fmt.Printf("站点   %s（%d 个文件，%s）\n", site.Root, len(site.Files), humanSize(site.TotalSize()))
	fmt.Printf("仓库   %s\n", repo)
	if useL1 {
		fmt.Printf("提交   %s（L1，打成一包直接发交易）\n", node)
	} else {
		fmt.Printf("上传   %s\n", uploader.Endpoint)
	}
	if prev == nil || len(prev.Refs) == 0 {
		fmt.Println("> 首次发布")
	} else {
		fmt.Printf("> 增量发布，%d 个文件有变化\n", len(publish.Changed(site, prev)))
	}

	fmt.Println()
	fmt.Println("请在浏览器里打开签名页：")
	fmt.Println()
	fmt.Printf("  %s\n", svc.URL())
	fmt.Println()
	fmt.Println("连接钱包后点「开始签名」，签完这里会自动继续。")
	openBrowser(svc.URL())
	fmt.Println()

	// 给整轮等签名加个上限：用户关掉页面或没装钱包时，不该把进程永久挂住。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	rec, err := target.Publish(ctx, site, prev)
	if rec != nil {
		if saveErr := publish.SaveRecord(statePath, rec); saveErr != nil {
			fmt.Fprintf(os.Stderr, "! 记录保存失败: %v\n", saveErr)
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "> 已保存部分记录，下次可复用其中 %d 个文件\n", len(rec.Refs))
		}
	}
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("v 完成，入口 %s\n", rec.Root)
	fmt.Printf("  网关预览 https://arweave.net/%s\n", rec.Root)
	fmt.Printf("v 记录写入 %s\n", statePath)
	fmt.Println()
	fmt.Println("下一步：把这个入口写进 ENS 的 contenthash 记录。")
	return nil
}

// cmdWebui 起一个本机图形界面，把维护器的功能都摆出来。
//
// 它只是命令行之上的一层壳：所有操作都走内部同一套实现。
// 只绑 127.0.0.1，不对外开放。
func cmdWebui(args []string) error {
	siteDir := "public"
	port := 0

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-h", "--help":
			usage(os.Stdout)
			return nil
		case "--site":
			if i+1 >= len(args) {
				return fmt.Errorf("--site 后面缺少值")
			}
			i++
			siteDir = args[i]
		case "--port":
			if i+1 >= len(args) {
				return fmt.Errorf("--port 后面缺少值")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 || n > 65535 {
				return fmt.Errorf("--port 需要 0 到 65535 之间的数字，实际 %q", args[i])
			}
			port = n
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("未知开关: %s", args[i])
			}
			siteDir = args[i]
		}
	}

	srv := webui.New(siteDir, port)
	if err := srv.Start(); err != nil {
		return err
	}
	defer srv.Close()

	fmt.Println("维护台已启动：")
	fmt.Println()
	fmt.Printf("  %s\n", srv.URL())
	fmt.Println()
	fmt.Printf("站点   %s\n", siteDir)
	// 日志落在哪里要打出来：它的价值在于出事时找得到。
	if dir, err := webui.DefaultLogDir(); err == nil {
		fmt.Printf("日志   %s\n", dir)
	}
	fmt.Println("按 Ctrl+C 退出。")
	fmt.Println()

	openBrowser(srv.URL())

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
	fmt.Println("\n已退出。")
	return nil
}

// openBrowser 尽力打开默认浏览器。
//
// 打不开也不影响流程：地址已经打印出来了，用户可以自己点。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// cmdNodes 探测发布时可用的网关，告诉用户此刻该填哪个。
//
// 存在的理由：交易是先 POST 给网关、再由网关转发给节点，
// 这一跳不通时提交会「看似成功、实则没到场」。与其在失败之后翻日志猜，
// 不如在发布之前花两秒看清出口。
//
// 只读 GET /info，不花 AR，失败也不留痕，可以随时跑。
func cmdNodes(args []string) error {
	proxyMode := ""
	proxyURL := ""
	var pos []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--proxy":
			if i+1 >= len(args) {
				return fmt.Errorf("--proxy 后面缺少值")
			}
			i++
			proxyMode = args[i]
		case "--proxy-url":
			if i+1 >= len(args) {
				return fmt.Errorf("--proxy-url 后面缺少值")
			}
			i++
			proxyURL = args[i]
		case "-h", "--help":
			fmt.Fprintln(os.Stdout, "用法: rog nodes [地址…] [--proxy system|manual|off] [--proxy-url <地址>]")
			fmt.Fprintln(os.Stdout, "  探测提交交易用的网关，报出各自的高度、队列与耗时。")
			fmt.Fprintln(os.Stdout, "  不填地址就测内置清单。只读 /info，不花 AR。")
			return nil
		default:
			pos = append(pos, args[i])
		}
	}

	// 地址也可以直接列在命令后面；不列就测内置清单。
	nodes := pos
	if len(nodes) == 0 {
		nodes = arweave.KnownNodes
	}

	mode := arweave.ProxySystem
	if proxyMode != "" {
		m, err := arweave.ParseProxyMode(proxyMode)
		if err != nil {
			return err
		}
		mode = m
	}
	if mode == arweave.ProxyManual && proxyURL == "" {
		return fmt.Errorf("手动代理模式需要 --proxy-url")
	}
	client, err := arweave.NewClient(arweave.ProxyConfig{Mode: mode, URL: proxyURL}, 0)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "探测 %d 个网关（只读 /info，不花 AR）…\n\n", len(nodes))

	// 探测本身也要有上限，否则一个黑洞地址会把整条命令堵死。
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	probes := arweave.ProbeNodes(ctx, nodes, client)

	var fastest string
	for _, p := range probes {
		if p.Err != nil {
			fmt.Fprintf(os.Stdout, "  x  %-26s 不通：%v\n", p.URL, p.Err)
			continue
		}
		info := p.Info
		if fastest == "" {
			fastest = info.URL
		}
		fmt.Fprintf(os.Stdout, "  v  %-26s 高度 %-10d 队列 %-4d %.0fms\n",
			info.URL, info.Height, info.QueueLength, float64(info.Latency.Microseconds())/1000)
	}

	fmt.Fprintln(os.Stdout)
	if fastest == "" {
		return fmt.Errorf("没有可用网关；检查网络或代理设置")
	}
	fmt.Fprintf(os.Stdout, "建议用 %s：发布时填 --node %s\n", fastest, fastest)
	return nil
}

func cmdPack(args []string) error {
	// 位置参数与开关混用，先把开关挑出来。
	rebuild := false
	proxy := ""
	var pos []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--rebuild", "--full", "-r":
			rebuild = true
		case "--proxy":
			if i+1 >= len(args) {
				return fmt.Errorf("--proxy 后面缺少值")
			}
			i++
			proxy = args[i]
		default:
			pos = append(pos, args[i])
		}
	}
	args = pos

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage(os.Stdout)
		return nil
	}

	opt := repopack.Options{
		Source:  args[0],
		OutDir:  "public",
		Rebuild: rebuild,
		// 只对拉远端仓库有意义；本地源用不上。
		Proxy: proxy,
		Logf: func(format string, a ...any) {
			fmt.Printf(format+"\n", a...)
		},
	}
	if len(args) > 1 {
		opt.OutDir = args[1]
	}
	if len(args) > 2 {
		opt.Name = args[2]
	}

	res, err := repopack.Pack(opt)
	if err != nil {
		return err
	}

	outRoot, err := filepath.Abs(opt.OutDir)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("文件清单 (%d 个, %s)\n", len(res.Files), humanSize(res.TotalSize))
	for _, rel := range res.Files {
		var size int64
		if info, err := os.Stat(filepath.Join(res.Target, rel)); err == nil {
			size = info.Size()
		}
		fmt.Printf("   %10s  %s/%s\n", humanSize(size), res.Name, filepath.ToSlash(rel))
	}

	fmt.Println()
	fmt.Printf("   pack %d 个: %s\n", len(res.Packs), strings.Join(res.Packs, ", "))
	fmt.Println()
	fmt.Printf("v 已写入 %s\n", filepath.Join(outRoot, "repository.json"))
	fmt.Printf("v 完成。默认分支 %s，链路 %s，可直接部署 %s\n", res.Branch, res.Via, outRoot)
	fmt.Println()
	fmt.Printf("  git clone <你的站点>/%s\n", res.Name)

	if !fileExists(filepath.Join(outRoot, "index.html")) {
		fmt.Println()
		fmt.Printf("! %s 里没有 index.html，现在还不能直接当站点部署。\n", outRoot)
		fmt.Println("  还差前端文件：把 index.html 和 src/ 放进同一个目录。")
	}

	fmt.Println()
	return nil
}

func humanSize(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.2f MiB", float64(size)/1024/1024)
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
