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
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/LWDJD/read-only-git/internal/arweave"
	"github.com/LWDJD/read-only-git/internal/publish"
	"github.com/LWDJD/read-only-git/internal/repopack"
	"github.com/LWDJD/read-only-git/internal/signer"
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
	case "publish":
		return cmdPublish(args[1:])
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
	fmt.Fprintln(w, "  pack [--update] <源仓库> [输出目录] [仓库名]   生成可托管的裸仓库")
	fmt.Fprintln(w, "  publish <站点目录> [目标目录]                 发布到本地目录")
	fmt.Fprintln(w, "  publish <站点目录> --arweave [选项]            发布到 Arweave（钱包签名）")
	fmt.Fprintln(w, "  help                                         显示本说明")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "pack 的参数:")
	fmt.Fprintln(w, "  --update   目标已存在时做增量更新，保留旧 pack；默认全量重建")
	fmt.Fprintln(w, "  <源仓库>   必填。本地路径（普通或裸仓库），或远端地址")
	fmt.Fprintln(w, "  [输出目录] 默认 ./public，站点根目录")
	fmt.Fprintln(w, "  [仓库名]   默认从源推导，带不带 .git 后缀等价")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "publish 的选项:")
	fmt.Fprintln(w, "  --arweave          发布到 Arweave，会起本地签名页等钱包签名")
	fmt.Fprintln(w, "  --l1               走 L1：把内容打成一个 ANS-104 包，签一笔交易直接提交")
	fmt.Fprintln(w, "  --node <地址>      L1 提交用的节点，默认 arweave.net")
	fmt.Fprintln(w, "  --repo <名字>      写进 data item 的 Repo 标签")
	fmt.Fprintln(w, "  --endpoint <地址>  上传服务，默认 turbo.ardrive.io（非 L1 时使用）")
	fmt.Fprintln(w, "  --from <入口 id>   从链上取回上次的发布记录，续上增量能力")
	fmt.Fprintln(w, "  --gateway <地址>   读取用的网关，默认 arweave.net")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "publish 会复用上一次的发布记录（存在 <站点目录>/.rog/ 下），")
	fmt.Fprintln(w, "只处理内容变化的文件。记录本身也会随站点上链，")
	fmt.Fprintln(w, "换机器时用 --from 就能取回来。")
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
		return cmdPublishArweave(pos[0], repo, endpoint, fromEntry, gateway, useL1, node)
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
func cmdPublishArweave(siteDir, repo, endpoint, fromEntry, gateway string, useL1 bool, node string) error {
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

	svc := signer.New([]byte(signer.DefaultPage))
	if err := svc.Start(); err != nil {
		return err
	}
	defer svc.Close()

	if node == "" {
		node = arweave.DefaultNode
	}

	uploader := arweave.NewUploader(endpoint)
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
		fetched, ferr := arweave.FetchRecord(context.Background(), gateway, fromEntry,
			publish.RecordRelPath("arweave", repo))
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

func cmdPack(args []string) error {
	// 位置参数与开关混用，先把开关挑出来。
	incremental := false
	var pos []string
	for _, a := range args {
		switch a {
		case "--incremental", "--update", "-u":
			incremental = true
		default:
			pos = append(pos, a)
		}
	}
	args = pos

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		usage(os.Stdout)
		return nil
	}

	opt := repopack.Options{
		Source:      args[0],
		OutDir:      "public",
		Incremental: incremental,
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
		fmt.Printf("   %10s  %s/%s\n", humanSize(size), res.Name+".git", filepath.ToSlash(rel))
	}

	fmt.Println()
	fmt.Printf("   pack %d 个: %s\n", len(res.Packs), strings.Join(res.Packs, ", "))
	fmt.Println()
	fmt.Printf("v 已写入 %s\n", filepath.Join(outRoot, "repository.json"))
	fmt.Printf("v 完成。默认分支 %s，链路 %s，可直接部署 %s\n", res.Branch, res.Via, outRoot)
	fmt.Println()
	fmt.Printf("  git clone <你的站点>/%s.git\n", res.Name)

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
