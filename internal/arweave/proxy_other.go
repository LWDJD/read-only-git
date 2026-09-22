//go:build !windows

package arweave

// systemProxy 在非 Windows 上返回空串。
//
// 那些平台的「系统代理」就是这个进程的环境变量，
// 而 net/http 的 ProxyFromEnvironment 已经把 HTTP_PROXY /
// HTTPS_PROXY / NO_PROXY 解释好了，这里再读一遍只是重复劳动。
func systemProxy() string { return "" }
