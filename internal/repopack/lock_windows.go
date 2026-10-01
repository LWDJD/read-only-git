//go:build windows

package repopack

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// 标准库的 syscall 没有导出 LockFileEx，这里直接绑 kernel32。
// 用动态绑定而不是引入 golang.org/x/sys/windows，是为了守住零依赖。
var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx = kernel32.NewProc("LockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

// lockFile 用 LockFileEx 独占锁住文件的第一个字节。
//
// 用操作系统级锁而不是「文件是否存在」判断占用：进程退出（包含被强杀）时
// 内核会释放锁，不会留下需要人工清理的残留，也没有 PID 复用的误判问题。
func lockFile(f *os.File) error {
	var overlapped syscall.Overlapped
	ret, _, err := procLockFileEx.Call(
		f.Fd(),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0,
		1, 0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if ret == 0 {
		return fmt.Errorf("加锁失败: %w", err)
	}
	return nil
}
