package arweave

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/LWDJD/read-only-git/internal/publish"
)

// Signer 是签名通道：把内容与 tags 交给钱包，拿回完整的 ANS-104 data item。
//
// 私钥在钱包里，Go 侧看不到，签名必须跨出进程，所以抽成接口：
// 生产实现是 localhost 服务 + 浏览器钱包插件，测试里换成一个桩。
type Signer interface {
	Sign(ctx context.Context, data []byte, tags []Tag) ([]byte, error)
}

// Target 把站点发布到 Arweave，实现 publish.Target。
type Target struct {
	Repo     string // 仓库名，写进 tags
	Uploader *Uploader
	Signer   Signer
	// TxSigner 非 nil 时走 L1：把这一轮新签的 data item 连同 manifest 打成一个
	// ANS-104 bundle，交给钱包签成一笔交易，直接提交到节点。
	// 与 Uploader 二选一。
	TxSigner TxSigner
	// Node 是提交交易用的节点地址，只在 L1 模式下用到。
	Node string
	// RecordPath 是发布记录在站点内的相对路径（用 / 分隔）。非空时会把记录
	// 也传上链并写进 manifest，换机器后能靠入口取回来。
	RecordPath string
	// Client 是所有对外请求（取记录、报交易、逐块 /chunk）用的 http client。
	// 为空时按系统代理造一个。发布失败十有八九出在这里，
	// 所以它必须是可配的，而不是隐式用标准库默认值。
	Client *http.Client
	// PendingPath 是「已签名未提交」的交易落盘位置。
	//
	// 有它，提交失败后的重试才能跳过钱包那一步。为空则不落盘。
	PendingPath string
	Logf        func(format string, args ...any)
}

func (t *Target) Name() string { return "arweave" }

// Publish 发布站点，尽量复用上一次已经上链的内容。
//
// 出错时返回「部分完成的记录」而不是 nil：已经上传成功的文件在里面有 id，
// 调用方保存它之后，下次运行就能跳过这些文件，不会为已付费的内容再付一次。
// 这一点比返回值语义的洁癖重要得多。
//
// 但「上传成功」在两条路上含义不同：Turbo 的 Upload 返回 id 就是真的传上去了；
// L1 的 id 只是本地算出来的，代表「已打进 bundle 待提交」。
// 所以 L1 下多一道 defer：只要最后那笔交易没提交成功，
// 本轮新签的那些引用就全部撤掉，不管是从哪一步退出去的。
func (t *Target) Publish(ctx context.Context, site *publish.Site, prev *publish.Record) (*publish.Record, error) {
	l1 := t.TxSigner != nil
	if !l1 && t.Uploader == nil {
		return nil, fmt.Errorf("未配置上传器")
	}
	if t.Signer == nil {
		return nil, fmt.Errorf("未配置签名通道")
	}

	rec := &publish.Record{
		Target: t.Name(),
		Files:  make(map[string]string, len(site.Files)),
		Refs:   make(map[string]string, len(site.Files)),
		Labels: map[string]string{"repo": t.Repo},
		At:     time.Now(),
	}

	// 影响 tags 的参数变了，旧引用就不能再用：内容未变的文件如果照旧复用，
	// 会连同旧的 Repo 标签一起沿用下去，产物里就出现了两种标签共存。
	labelsMatch := prev != nil && prev.Labels["repo"] == t.Repo

	// L1 模式下这一轮新签的 data item 先攒着，最后打成一包只发一笔交易。
	// Turbo 模式下每签完一个就直接交给上传服务，攒的东西始终为空。
	var pending [][]byte

	// fresh 记下本轮新签、还没上链的路径。
	//
	// 这是 L1 特有的一件事：Turbo 那边 Upload 返回 id 就等于确实传上去了，
	// 而 L1 的 id 只是本地算出来的，代表「已打进 bundle 待提交」。
	// 那笔交易一旦提交失败，这些 id 在链上并不存在，
	// 必须从记录里撤掉——否则下次发布会以为它们已经上链而跳过，
	// 结果是站点里只剩一份清单、没有实际内容。
	var fresh []string

	// deliver 把一份签好的 data item 送出去，返回它的 id。
	//
	// 两条路取 id 的方式不同：Turbo 由上传服务返回，L1 没有服务可问，
	// 只能按规范从签名字段自己算，而这个 id 会进 manifest，算错就全乱。
	// name 用于 L1 下登记「这份还没上链」，空串表示不登记（如 manifest）。
	deliver := func(name string, signed []byte) (string, error) {
		if l1 {
			id, err := DataItemID(signed)
			if err != nil {
				return "", err
			}
			pending = append(pending, signed)
			if name != "" {
				fresh = append(fresh, name)
			}
			return id, nil
		}
		return t.Uploader.Upload(ctx, signed)
	}

	// L1 下不管从哪一步退出去，只要最后那笔交易没提交成功，
	// 本轮新签的引用就都是无效的。
	//
	// 只写在「提交失败」那一条分支上不够：签名、算 id 都可能中途出错，
	// 那些已经记进 rec 的引用同样没上链。实际就撞上过这种情况——
	// 一个文件卡在签名上，前面几个的引用留了下来，下次发布就把它们跳过了。
	submitted := false
	defer func() {
		if !l1 || submitted {
			return
		}
		for _, p := range fresh {
			delete(rec.Refs, p)
			delete(rec.Files, p)
		}
		// 入口指向的 manifest 同样没上链，不能留下这个根。
		rec.Root = ""
	}()

	var uploaded, reused int
	for _, f := range site.Files {
		if err := ctx.Err(); err != nil {
			return rec, err
		}

		// 摘要与上次一致、且上次确实拿到了 id：直接沿用，不重传。
		if labelsMatch {
			oldID := prev.Refs[f.Path]
			if oldID != "" && prev.Files[f.Path] == f.Digest {
				rec.Files[f.Path] = f.Digest
				rec.Refs[f.Path] = oldID
				reused++
				continue
			}
		}

		content, err := site.ReadFile(f.Path)
		if err != nil {
			return rec, err
		}

		signed, err := t.Signer.Sign(ctx, content, ContentTags(t.Repo, f.Path, ContentTypeFor(f.Path)))
		if err != nil {
			return rec, fmt.Errorf("签名 %s 失败: %w", f.Path, err)
		}

		id, err := deliver(f.Path, signed)
		if err != nil {
			return rec, fmt.Errorf("提交 %s 失败: %w", f.Path, err)
		}

		rec.Files[f.Path] = f.Digest
		rec.Refs[f.Path] = id
		uploaded++
	}

	// 把发布记录本身也挂上链：换机器或本地文件丢了之后，靠入口就能取回
	// 「路径 -> data item id」的映射，增量发布不必从零重传。
	//
	// 链上这份的 Root 只能是空的。入口 id 要等 manifest 传完才知道，
	// 而 manifest 又得把记录文件包含进去，这里存在先后依赖。
	// 不影响增量：复用只认 Files / Refs / Labels 三个映射。
	paths := make(map[string]string, len(rec.Refs)+1)
	for p, id := range rec.Refs {
		paths[p] = id
	}
	if t.RecordPath != "" {
		chainRec := *rec
		chainRec.Root = ""
		data, err := publish.MarshalRecord(&chainRec)
		if err != nil {
			return rec, err
		}
		signedRecord, err := t.Signer.Sign(ctx, data, RecordTags(t.Repo))
		if err != nil {
			return rec, fmt.Errorf("签名发布记录失败: %w", err)
		}
		recordID, err := deliver(t.RecordPath, signedRecord)
		if err != nil {
			return rec, fmt.Errorf("提交发布记录失败: %w", err)
		}
		paths[t.RecordPath] = recordID
	}

	// 入口：把「路径 -> data item id」固化成 manifest。
	// 旧版本 manifest 依然可达，只是入口指向了新的这一个。
	manifestBytes, err := NewManifest(paths, EntryPath(site)).Bytes()
	if err != nil {
		return rec, err
	}

	signedManifest, err := t.Signer.Sign(ctx, manifestBytes, ManifestTags(t.Repo))
	if err != nil {
		return rec, fmt.Errorf("签名 manifest 失败: %w", err)
	}

	root, err := deliver("", signedManifest)
	if err != nil {
		return rec, fmt.Errorf("提交 manifest 失败: %w", err)
	}
	rec.Root = root

	// L1：把这一轮新签的 data item 打成一包，签一笔交易发出去。
	// 未变的文件没有进 pending，它们的 data item 还在上一笔交易里，
	// manifest 里引用的就是那些旧 id，所以不必重复付费。
	//
	// 体积不再卡在 256 KiB：超过一块时 SubmitBundle 会自动走分块协议，
	// 先报交易再逐块补。
	if l1 {
		if err := t.submitL1(ctx, pending, root, reused); err != nil {
			return rec, err
		}
		submitted = true
		return rec, nil
	}

	t.logf("上传 %d 个，复用 %d 个，入口 %s", uploaded, reused, root)
	return rec, nil
}

// submitL1 走 L1 的收尾：签一笔以整包为 data 的交易，提交到节点。
//
// 这里面有一件事值得单独说：签名结果会先落到 PendingPath，提交成功再删。
// 签名是用户在钱包里点过确认的动作，一次网络失败不该让它作废；
// 留着它，下一次发布就能直接重传，不必再让人去钱包里点一遍。
//
// 抽成独立方法是为了能被单独测：直接走 Publish 会连带签一大堆 data item。
func (t *Target) submitL1(ctx context.Context, pending [][]byte, root string, reused int) error {
	bundle, err := Bundle(pending)
	if err != nil {
		return err
	}
	tags := BundleTags(t.Repo)

	// 先看有没有上次签好但没提交成功的交易。包体没变，说明这份签名仍然对得上。
	// 但签名本身还要过一遍本地验签：旧版页面回传的签名可能与明文 tags 不自洽
	// （病灶见 TxSignature.Tags 的注释），直接复用只会再被拒一次。验不过就作废重签。
	if p := LoadPending(t.PendingPath); p.SameBundle(bundle) {
		if err := VerifySignedTx(p.Sig, p.Tags); err != nil {
			t.logf("上次的签名本地验签没过（%v），作废重签", err)
			ClearPending(t.PendingPath)
		} else {
			t.logf("复用上次签好的交易（%d 字节），不必再签一次", len(bundle))
			txID, err := SubmitBundle(ctx, t.Node, p.Bundle, p.Tags, p.Sig, t.Client, t.logf)
			if err != nil {
				return err
			}
			ClearPending(t.PendingPath)
			t.logf("交易 %s；复用 %d 个，入口 %s", txID, reused, root)
			return nil
		}
	}

	sig, err := t.TxSigner.SignTx(ctx, bundle, tags)
	if err != nil {
		return fmt.Errorf("签名交易失败: %w", err)
	}

	// 页面已经提交过了，直接用它的 ID。
	if sig.Uploaded {
		// 两个状态都要记：POST 那一刻节点回了什么，事后还查不查得到。
		// 「节点接受了但随后查不到」这类问题，只有响应原话能说清。
		t.logf("交易 %s（单块，页面已提交；POST %d，事后状态 %d，reward %s）",
			sig.ID, sig.PostStatus, sig.Status, sig.Reward)
		if body := strings.TrimSpace(sig.PostBody); body != "" {
			t.logf("节点原话：%s", truncate(body, 300))
		}
		if sig.Status == http.StatusNotFound {
			t.logf("! 事后查不到这笔交易。刚提交时 404 是正常的（节点异步收录），" +
				"但如果过了几分钟仍是 404，说明它没被节点留住")
		}
		t.logf("确认情况可查：%s/tx/%s", DefaultGateway, sig.ID)
		ClearPending(t.PendingPath)
		return nil
	}

	// 多块：页面只签名并回传 proofs，提交由 Go 走 /tx → 逐块 /chunk。
	// 这条路每一步都有日志，失败会退避重试，致命错会单独挑出来。
	if sig.Chunks > 1 {
		t.logf("这一包切成 %d 块，由本地提交（页面只签名）", sig.Chunks)
	}

	// 兑底：页面拿不到节点（或旧版页面只回传字段）时，走 Go 自己的提交。
	// 签好就先落盘：万一提交失败，下一次就能直接重传。
	// 落盘失败只提醒一句，不拦住发布——顶多是重试时要再签一次。
	if err := SavePending(t.PendingPath, bundle, tags, sig); err != nil {
		t.logf("! 待提交交易落盘失败，重试时需要重新签名: %v", err)
	}

	// client 传 t.Client：没配时 SubmitBundle 内部会按系统代理兑底。
	txID, err := SubmitBundle(ctx, t.Node, bundle, tags, sig, t.Client, t.logf)
	if err != nil {
		return err
	}
	ClearPending(t.PendingPath)

	t.logf("打成一包 %d 个 data item（%d 字节），交易 %s；复用 %d 个，入口 %s",
		len(pending), len(bundle), txID, reused, root)
	return nil
}

// EntryPath 返回默认入口路径，通常是站点根下的 index.html。
func EntryPath(site *publish.Site) string {
	if _, ok := site.Find("index.html"); ok {
		return "index.html"
	}
	return ""
}

// ContentTypeFor 按扩展名给出 Content-Type，认不出的按二进制处理。
//
// 网关会用它决定怎么回给浏览器，所以这里宁可保守：不确定就 octet-stream，
// 不要让网关把二进制当文本塞给页面。
func ContentTypeFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	case ".xml":
		return "application/xml; charset=utf-8"
	case ".pdf":
		return "application/pdf"
	case ".wasm":
		return "application/wasm"
	case ".zip":
		return "application/zip"
	case ".gz":
		return "application/gzip"
	default:
		return "application/octet-stream"
	}
}

func (t *Target) logf(format string, args ...any) {
	if t.Logf != nil {
		t.Logf(format, args...)
	}
}
