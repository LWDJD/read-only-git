package arweave

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ProxyMode 是代理的三种模式。
type ProxyMode string

const (
	// ProxySystem 用系统设置：Windows 读 Internet 选项里的代理，
	// 其余平台读 HTTP_PROXY / HTTPS_PROXY / NO_PROXY。
	ProxySystem ProxyMode = "system"
	// ProxyManual 用显式给出的地址。
	ProxyManual ProxyMode = "manual"
	// ProxyOff 不走代理，直连。
	ProxyOff ProxyMode = "off"
)

// ProxyConfig 描述一次对外请求要走什么出口。
type ProxyConfig struct {
	Mode ProxyMode
	// URL 是 Mode 为 manual 时的代理地址，形如 http://127.0.0.1:7890。
	// 不带协议时按 http 处理。
	URL string
}

// NewClient 按代理配置造一个 http.Client。
//
// 这是对外请求唯一的 client 来源。各处自己 `&http.Client{}` 看着省事，
// 但「代理设了不生效」这类问题会极难排查：总有一处漏了，
// 而那一处往往正好是失败的那一步。
func NewClient(cfg ProxyConfig, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	tr, err := NewTransport(cfg)
	if err != nil {
		return nil, err
	}
	return &http.Client{Timeout: timeout, Transport: tr}, nil
}

// NewTransport 按代理配置造一个 Transport。
func NewTransport(cfg ProxyConfig) (*http.Transport, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("默认 Transport 类型意外：%T", http.DefaultTransport)
	}
	// Clone 而不是自己拼字段：连接池、超时、HTTP/2 选项都跟着标准库走，
	// 将来标准库改了默认值也自动跟上。
	tr := base.Clone()

	switch cfg.Mode {
	case ProxyOff:
		tr.Proxy = nil

	case ProxyManual:
		addr, err := NormalizeProxyURL(cfg.URL)
		if err != nil {
			return nil, err
		}
		tr.Proxy = http.ProxyURL(addr)

	case "", ProxySystem:
		// 先摆上环境变量那份（非 Windows 上这就是全部）
		tr.Proxy = http.ProxyFromEnvironment
		// Windows 的代理设在注册表里，环境变量常是空的，再问一次
		if raw := systemProxy(); raw != "" {
			if u, err := NormalizeProxyURL(raw); err == nil {
				tr.Proxy = http.ProxyURL(u)
			}
		}

	default:
		return nil, fmt.Errorf("未知的代理模式: %q（可选 system / manual / off）", cfg.Mode)
	}

	return tr, nil
}

// NormalizeProxyURL 把用户填的地址补成完整 URL。
//
// 界面上没人愿意先打 http://，所以不带协议时按 http 补上。
// 只认 http 与 https：SOCKS 要走另一套拨号器，这个函数不假装支持它。
func NormalizeProxyURL(raw string) (*url.URL, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, fmt.Errorf("代理地址不能为空")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("代理地址解析失败: %w", err)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("代理地址缺少主机: %q", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("代理只支持 http 与 https，实际 %q", u.Scheme)
	}
	return u, nil
}

// clientOrDefault 取一个可用的 client：没配就按系统代理造一个。
//
// 各处兑底都走它，免得有人直接退回 http.DefaultClient——
// 那样在设了代理的机器上会绕开配置，而且没有任何提示。
func clientOrDefault(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	if def, err := NewClient(ProxyConfig{Mode: ProxySystem}, 0); err == nil {
		return def
	}
	return http.DefaultClient
}

// ParseProxyMode 把界面或命令行传来的字符串转成模式。
//
// 空串当作 system：什么都不说就是想跟着系统走。
func ParseProxyMode(s string) (ProxyMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ProxySystem, nil
	case "system":
		return ProxySystem, nil
	case "manual":
		return ProxyManual, nil
	case "off", "none", "direct":
		return ProxyOff, nil
	default:
		return "", fmt.Errorf("未知的代理模式: %q（可选 system / manual / off）", s)
	}
}

// pickProxyServer 从 Windows 的 ProxyServer 值里挑一个可用地址。
//
// 它有两种写法：
//
//	127.0.0.1:7890                              所有协议共用
//	http=127.0.0.1:7890;https=127.0.0.1:7890    分协议指定
//
// 分协议时优先 https：发往节点与网关的都是 https，它是真正被用到的那一条。
// 键的顺序不固定（"http" 在前也常见），所以不能遇到第一个就返回。
// 返回的字符串可能还不带协议，由 NormalizeProxyURL 去补。
func pickProxyServer(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "=") {
		return raw
	}

	byScheme := map[string]string{}
	var first string
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if first == "" {
			first = v
		}
		byScheme[strings.ToLower(strings.TrimSpace(k))] = v
	}

	if v, ok := byScheme["https"]; ok {
		return v
	}
	if v, ok := byScheme["http"]; ok {
		return v
	}
	return first
}
