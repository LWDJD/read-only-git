package arweave

import (
	"context"
	"fmt"
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
	Logf       func(format string, args ...any)
}

func (t *Target) Name() string { return "arweave" }

// Publish 发布站点，尽量复用上一次已经上链的内容。
//
// 出错时返回「部分完成的记录」而不是 nil：已经上传成功的文件在里面有 id，
// 调用方保存它之后，下次运行就能跳过这些文件，不会为已付费的内容再付一次。
// 这一点比返回值语义的洁癖重要得多。
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

	// deliver 把一份签好的 data item 送出去，返回它的 id。
	//
	// 两条路取 id 的方式不同：Turbo 由上传服务返回，L1 没有服务可问，
	// 只能按规范从签名字段自己算，而这个 id 会进 manifest，算错就全乱。
	deliver := func(signed []byte) (string, error) {
		if l1 {
			id, err := DataItemID(signed)
			if err != nil {
				return "", err
			}
			pending = append(pending, signed)
			return id, nil
		}
		return t.Uploader.Upload(ctx, signed)
	}

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

		id, err := deliver(signed)
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
		recordID, err := deliver(signedRecord)
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

	root, err := deliver(signedManifest)
	if err != nil {
		return rec, fmt.Errorf("提交 manifest 失败: %w", err)
	}
	rec.Root = root

	// L1：把这一轮新签的 data item 打成一包，签一笔交易发出去。
	// 未变的文件没有进 pending，它们的 data item 还在上一笔交易里，
	// manifest 里引用的就是那些旧 id，所以不必重复付费。
	if l1 {
		bundle := Bundle(pending)
		tags := BundleTags(t.Repo)
		if err := CheckBundleSize(len(bundle)); err != nil {
			return rec, err
		}
		sig, err := t.TxSigner.SignTx(ctx, bundle, tags)
		if err != nil {
			return rec, fmt.Errorf("签名交易失败: %w", err)
		}
		txID, err := SubmitTx(ctx, t.Node, bundle, tags, sig, nil)
		if err != nil {
			return rec, err
		}
		t.logf("打成一包 %d 个 data item（%d 字节），交易 %s；复用 %d 个，入口 %s",
			len(pending), len(bundle), txID, reused, root)
		return rec, nil
	}

	t.logf("上传 %d 个，复用 %d 个，入口 %s", uploaded, reused, root)
	return rec, nil
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
