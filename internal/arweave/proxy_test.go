package arweave

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// 三种模式各自的 Transport 行为。
//
// off 必须把 Proxy 置空：不置空的话 http.Transport 默认会读环境变量，
// 用户点了「关闭代理」却仍旧走了代理，这是最容易被忽略的一种失败。
func TestNewTransportProxyModes(t *testing.T) {
	tr, err := NewTransport(ProxyConfig{Mode: ProxyOff})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Proxy != nil {
		t.Fatal("off 模式下不该设 Proxy")
	}

	tr, err = NewTransport(ProxyConfig{Mode: ProxyManual, URL: "127.0.0.1:7890"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, "https://arweave.net/", nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.String() != "http://127.0.0.1:7890" {
		t.Fatalf("manual 模式应当指向给定地址，实际 %v", u)
	}

	tr, err = NewTransport(ProxyConfig{Mode: ProxySystem})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Proxy == nil {
		t.Fatal("system 模式下应当有一个取代理的函数")
	}

	// 空模式等同 system：什么都没说就是想跟着系统走
	tr, err = NewTransport(ProxyConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Proxy == nil {
		t.Fatal("空模式应当按 system 处理")
	}

	if _, err := NewTransport(ProxyConfig{Mode: "bogus"}); err == nil {
		t.Fatal("未知模式应当报错")
	}
}

// 手动代理真的会被用上，而不只是设了个字段。
//
// 假代理收到请求并直接回应，目标域名是个不存在的 .invalid：
// 如果流量没经过代理，这一步会栽在 DNS 解析上。
func TestManualProxyIsActuallyUsed(t *testing.T) {
	var hits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxy.Close()

	client, err := NewClient(ProxyConfig{Mode: ProxyManual, URL: proxy.URL}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get("http://example.invalid/hello")
	if err != nil {
		t.Fatalf("请求应当被代理接住，实际 %v", err)
	}
	defer resp.Body.Close()

	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("代理一次都没收到请求")
	}
}

// 关闭代理时不经过代理：请求直接奔向目标地址（于是在网络层失败），
// 而不是被代理服务器接住。
func TestProxyOffBypassesProxy(t *testing.T) {
	var hits int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer proxy.Close()

	client, err := NewClient(ProxyConfig{Mode: ProxyOff}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// 目标是另一个连不上的地址，代理在别处。off 模式下请求只会奔向目标，
	// 所以这一趟会失败，但失败点与代理无关。
	if resp, err := client.Get("http://127.0.0.1:1/nothing"); err == nil {
		resp.Body.Close()
	}

	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("off 模式下请求不该落到代理上，实际收到 %d 次", n)
	}
}

// 地址不带协议时按 http 补上。
func TestNormalizeProxyURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		bad  bool
	}{
		{"127.0.0.1:7890", "http://127.0.0.1:7890", false},
		{"  127.0.0.1:7890  ", "http://127.0.0.1:7890", false},
		{"http://127.0.0.1:7890", "http://127.0.0.1:7890", false},
		{"https://proxy.corp:8080", "https://proxy.corp:8080", false},
		// SOCKS 要走另一套拨号器，这里不假装支持
		{"socks5://127.0.0.1:1080", "", true},
		{"", "", true},
		{":8080", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeProxyURL(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("NormalizeProxyURL(%q) 应当报错，实际 %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeProxyURL(%q) 出错: %v", c.in, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("NormalizeProxyURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Windows 的 ProxyServer 有两种写法：共用一个，或按协议分开。
func TestPickProxyServer(t *testing.T) {
	cases := []struct{ in, want string }{
		{"127.0.0.1:7890", "127.0.0.1:7890"},
		{"http=127.0.0.1:7890;https=127.0.0.1:7891", "127.0.0.1:7891"},
		{"https=127.0.0.1:7891;http=127.0.0.1:7890", "127.0.0.1:7891"},
		{"http=127.0.0.1:7890", "127.0.0.1:7890"},
		// 只认得 http/https；别的协议只好当兜底
		{"ftp=1.2.3.4:21", "1.2.3.4:21"},
		{"ftp=1.2.3.4:21;http=127.0.0.1:7890", "127.0.0.1:7890"},
		{"", ""},
		{"   ", ""},
		{"http=;https=127.0.0.1:7891", "127.0.0.1:7891"},
	}
	for _, c := range cases {
		if got := pickProxyServer(c.in); got != c.want {
			t.Errorf("pickProxyServer(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseProxyMode(t *testing.T) {
	ok := map[string]ProxyMode{
		"":       ProxySystem,
		"system": ProxySystem,
		"SYSTEM": ProxySystem,
		"manual": ProxyManual,
		"off":    ProxyOff,
		"none":   ProxyOff,
		"direct": ProxyOff,
	}
	for in, want := range ok {
		got, err := ParseProxyMode(in)
		if err != nil {
			t.Errorf("ParseProxyMode(%q) 出错: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseProxyMode(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ParseProxyMode("nope"); err == nil {
		t.Fatal("未知模式应当报错")
	}
}
