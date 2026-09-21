// Package publish 抽象「把站点发布到一个目标」这件事。
//
// 不同目标的增量机制差异很大：Arweave 靠复用 data item id，IPFS 靠内容寻址，
// 平台类目标（Cloudflare Pages / EdgeOne）靠各自的部署机制，本地目录靠摘要比对。
// 所以接口只抽象「发布」这个动作，把「能不能复用」留给实现自己决定。
package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileInfo 描述站点里的一个文件。
type FileInfo struct {
	Path   string `json:"path"`   // 相对路径，用 / 分隔
	Digest string `json:"digest"` // 内容 sha256（十六进制）
	Size   int64  `json:"size"`
}

// Site 是要发布的站点。
type Site struct {
	Root  string     `json:"-"`
	Files []FileInfo `json:"files"`
}

// Record 是一次发布的记录，供下一次复用。
//
// Files 统一记内容摘要（sha256），Changed 靠它判断哪些文件变了；
// Refs 记各存储自己的引用（data item id / CID / 路径），由实现决定是否用。
// 分开存是因为两者语义不同：摘要用于比对，引用用于复用与入口组装，
// 混在一起会让增量判断失真。
//
// Labels 记「会影响引用有效性」的元数据（对 Arweave 就是 tags 里的仓库名）。
// 这些值变了之后，旧引用即使内容未变也不能再复用，否则会沿用旧的标签。
type Record struct {
	Target string            `json:"target"`
	Root   string            `json:"root"`           // 整体入口引用
	Files  map[string]string `json:"files"`          // 路径 -> 内容摘要
	Refs   map[string]string `json:"refs,omitempty"` // 路径 -> 存储引用
	Labels map[string]string `json:"labels,omitempty"`
	At     time.Time         `json:"at"`
}

// Target 是发布目标的抽象。
//
// prev 是上一次的发布记录，可为 nil。实现应尽量复用其中仍然有效的内容，
// 但怎么算「有效」由实现决定。
type Target interface {
	Name() string
	Publish(ctx context.Context, site *Site, prev *Record) (*Record, error)
}

// StateDir 是站点里存放发布记录的目录，不参与发布。
const StateDir = ".rog"

// Scan 扫描站点目录，生成文件清单。
func Scan(root string) (*Site, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	site := &Site{Root: abs}

	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		// .rog 是工具状态，无论它以目录还是文件形态出现都不该参与发布。
		// 只判目录的话，一个名为 .rog 的普通文件会被当成站点内容拷到目标，
		// 直到写记录时才因类型不对报错，期间已经产生了副作用。
		if rel == StateDir {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		digest, err := digestFile(path)
		if err != nil {
			return err
		}
		site.Files = append(site.Files, FileInfo{
			Path:   filepath.ToSlash(rel),
			Digest: digest,
			Size:   info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(site.Files, func(i, j int) bool { return site.Files[i].Path < site.Files[j].Path })
	return site, nil
}

// Digest 计算一段内容的 sha256（十六进制）。
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Dir 返回站点里某个相对路径对应的本地绝对路径。
func (s *Site) Dir(rel string) string {
	return filepath.Join(s.Root, filepath.FromSlash(rel))
}

// ReadFile 读取站点里的一个文件。
func (s *Site) ReadFile(rel string) ([]byte, error) {
	return os.ReadFile(s.Dir(rel))
}

// TotalSize 返回清单里所有文件的总字节数。
func (s *Site) TotalSize() int64 {
	var n int64
	for _, f := range s.Files {
		n += f.Size
	}
	return n
}

// Find 按路径查找清单项。
func (s *Site) Find(rel string) (FileInfo, bool) {
	for _, f := range s.Files {
		if f.Path == rel {
			return f, true
		}
	}
	return FileInfo{}, false
}

// Changed 返回相对 prev 有变化的路径（新增或内容变化）。
func Changed(site *Site, prev *Record) []string {
	var out []string
	for _, f := range site.Files {
		if prev == nil || prev.Files[f.Path] != f.Digest {
			out = append(out, f.Path)
		}
	}
	return out
}

// StatePath 返回某个发布目标的记录文件路径，identity 是该目标的身份标识。
//
// 名字里带上身份标识的短哈希：同一个站点发到两个不同目的地（两个本地目录、
// 两个仓库名）时，若共用一个记录文件，会互相覆盖 root、复用判断也会串味。
//
// 选什么当 identity 很关键：它应当是「决定复用能否成立」的东西。
// 例如 Arweave 用仓库名而不是上传端点——data item id 是内容寻址的，
// 换个端点同一份内容依然是同一个 id，拿端点分键只会白白重传一遍。
func StatePath(siteRoot, target, identity string) string {
	if identity == "" {
		identity = "default"
	}
	sum := sha256.Sum256([]byte(identity))
	suffix := hex.EncodeToString(sum[:4])
	return filepath.Join(siteRoot, StateDir, fmt.Sprintf("publish-%s-%s.json", target, suffix))
}

// LoadRecord 读取发布记录；文件不存在时返回 (nil, nil)。
//
// 记录损坏时返回错误，调用方应当容忍它（按首次发布处理），而不是中断发布：
// 损坏的记录只意味着复用信息丢失，不该让整个发布失败。
func LoadRecord(path string) (*Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("发布记录损坏: %w", err)
	}
	if rec.Files == nil {
		rec.Files = map[string]string{}
	}
	if rec.Refs == nil {
		rec.Refs = map[string]string{}
	}
	return &rec, nil
}

// SaveRecord 原子写入发布记录：先写临时文件再改名，避免中断留下半截 JSON。
func SaveRecord(path string, rec *Record) error {
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err == nil && !info.IsDir() {
		return fmt.Errorf("%s 已存在但不是目录，请先移除它", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WithoutState 从清单里剔除状态目录相关的条目（双保险，Scan 已经跳过）。
func WithoutState(files []FileInfo) []FileInfo {
	out := files[:0:0]
	for _, f := range files {
		if f.Path == StateDir || strings.HasPrefix(f.Path, StateDir+"/") {
			continue
		}
		out = append(out, f)
	}
	return out
}

func digestFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
