//go:build !windows

package repopack

import (
	"os"
	"syscall"
)

// lockFile 用 flock 独占锁住文件。进程退出（包含被强杀）时由内核释放，
// 不会留下需要人工清理的残留。
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
