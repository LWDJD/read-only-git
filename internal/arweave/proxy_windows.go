//go:build windows

package arweave

import (
	"syscall"
	"unsafe"
)

// Windows 的代理设置在 Internet 选项里，落在 HKCU 下这个键。
const proxyRegPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// 标准库的 syscall 没有导出注册表操作，直接绑 advapi32。
// 与 repopack 的 LockFileEx 一样，用动态绑定守住零依赖。
var (
	advapi32        = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKey  = advapi32.NewProc("RegOpenKeyExW")
	procRegQueryVal = advapi32.NewProc("RegQueryValueExW")
	procRegCloseKey = advapi32.NewProc("RegCloseKey")
)

const (
	hkeyCurrentUser = 0x80000001
	keyQueryValue   = 0x0001
	// 注册表值类型
	regTypeSZ    = 1
	regTypeDWORD = 4
)

// systemProxy 读 Windows 的代理设置，返回一个可交给 NormalizeProxyURL 的地址。
//
// 为什么不读环境变量：Windows 上用户是在「Internet 选项」里设代理的，
// 那写进注册表，而 HTTP_PROXY 通常是空的。只看环境变量会得出
// 「没设代理」，然后请求就直接出去了，报错还看不出是代理没生效。
//
// 不支持的是自动配置脚本（AutoConfigURL，即 PAC）：那要跑 JS 才知道走哪，
// 不属于这里能装的范畴。返回空串表示「系统里没有可用的显式代理」。
func systemProxy() string {
	if !proxyEnable() {
		return ""
	}
	return pickProxyServer(regString(proxyRegPath, "ProxyServer"))
}

// proxyEnable 看 ProxyEnable 是否为 1。
func proxyEnable() bool {
	v, ok := regDWORD(proxyRegPath, "ProxyEnable")
	return ok && v == 1
}

func regString(path, name string) string {
	h, ok := regOpenKey(path)
	if !ok {
		return ""
	}
	defer procRegCloseKey.Call(uintptr(h))

	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return ""
	}

	var typ, size uint32
	// 先问需要多大
	ret, _, _ := procRegQueryVal.Call(
		uintptr(h), uintptr(unsafe.Pointer(n)), 0,
		uintptr(unsafe.Pointer(&typ)), 0,
		uintptr(unsafe.Pointer(&size)),
	)
	if ret != 0 || size == 0 || typ != regTypeSZ {
		return ""
	}

	buf := make([]uint16, size/2+1)
	ret, _, _ = procRegQueryVal.Call(
		uintptr(h), uintptr(unsafe.Pointer(n)), 0,
		uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func regDWORD(path, name string) (uint32, bool) {
	h, ok := regOpenKey(path)
	if !ok {
		return 0, false
	}
	defer procRegCloseKey.Call(uintptr(h))

	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, false
	}

	var typ, val, size uint32
	size = 4
	ret, _, _ := procRegQueryVal.Call(
		uintptr(h), uintptr(unsafe.Pointer(n)), 0,
		uintptr(unsafe.Pointer(&typ)),
		uintptr(unsafe.Pointer(&val)),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret != 0 || typ != regTypeDWORD {
		return 0, false
	}
	return val, true
}

func regOpenKey(path string) (syscall.Handle, bool) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, false
	}
	var h syscall.Handle
	ret, _, _ := procRegOpenKey.Call(
		uintptr(hkeyCurrentUser),
		uintptr(unsafe.Pointer(p)),
		0,
		uintptr(keyQueryValue),
		uintptr(unsafe.Pointer(&h)),
	)
	if ret != 0 {
		return 0, false
	}
	return h, true
}
