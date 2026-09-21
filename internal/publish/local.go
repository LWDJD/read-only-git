package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Local 把站点发布到一个本地目录。
//
// 它是最简单的一个 Target，用来把发布链路跑通；也可以当作「先落盘，
// 再交给别的工具上传」的中间步骤。
//
// 出错时与 arweave 实现一样返回部分记录，保持接口行为一致。
type Local struct {
	Dir  string
	Logf func(format string, args ...any)
}

func (l *Local) Name() string { return "local" }

func (l *Local) Publish(ctx context.Context, site *Site, prev *Record) (*Record, error) {
	if l.Dir == "" {
		return nil, fmt.Errorf("未指定目标目录")
	}
	abs, err := filepath.Abs(l.Dir)
	if err != nil {
		return nil, err
	}

	rec := &Record{
		Target: l.Name(),
		Root:   abs,
		Files:  make(map[string]string, len(site.Files)),
		Refs:   make(map[string]string, len(site.Files)),
		At:     time.Now(),
	}

	var reused, copied int
	for _, f := range site.Files {
		if err := ctx.Err(); err != nil {
			return rec, err
		}

		dst := filepath.Join(abs, filepath.FromSlash(f.Path))

		// 上次记录里摘要一致、且目标文件内容确实没被动过，才跳过复制。
		// 只检查「文件存在」会让外部篡改被静默保留下来。
		if prev != nil && prev.Files[f.Path] == f.Digest && fileMatches(dst, f.Digest) {
			rec.Files[f.Path] = f.Digest
			rec.Refs[f.Path] = f.Path
			reused++
			continue
		}

		if err := copyFile(filepath.Join(site.Root, filepath.FromSlash(f.Path)), dst); err != nil {
			return rec, err
		}
		rec.Files[f.Path] = f.Digest
		rec.Refs[f.Path] = f.Path
		copied++
	}

	l.logf("复用 %d 个，复制 %d 个", reused, copied)
	return rec, nil
}

func (l *Local) logf(format string, args ...any) {
	if l.Logf != nil {
		l.Logf(format, args...)
	}
}

// fileMatches 判断目标文件的内容摘要是否与期望一致。
func fileMatches(path, want string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == want
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
