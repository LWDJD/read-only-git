package arweave

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbeNodeParsesInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" {
			t.Errorf("期望请求 /info，得到 %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"height":2007271,"queue_length":3,"network":"arweave.N.1"}`))
	}))
	defer srv.Close()

	info, err := ProbeNode(context.Background(), srv.URL, srv.Client())
	if err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if info.Height != 2007271 {
		t.Errorf("height 期望 2007271，得到 %d", info.Height)
	}
	if info.QueueLength != 3 {
		t.Errorf("queue_length 期望 3，得到 %d", info.QueueLength)
	}
	if info.Network != "arweave.N.1" {
		t.Errorf("network 期望 arweave.N.1，得到 %q", info.Network)
	}
	if !info.Available() {
		t.Error("Available() 应为 true")
	}
	if info.URL != srv.URL {
		t.Errorf("URL 期望 %q，得到 %q", srv.URL, info.URL)
	}
}

// 末尾带斜杠的地址要能被规整，否则会拼出 //info。
func TestProbeNodeTrimsTrailingSlash(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		_, _ = w.Write([]byte(`{"height":1,"network":"n"}`))
	}))
	defer srv.Close()

	if _, err := ProbeNode(context.Background(), srv.URL+"/", srv.Client()); err != nil {
		t.Fatalf("探测失败: %v", err)
	}
	if got != "/info" {
		t.Errorf("路径期望 /info，得到 %q", got)
	}
}

func TestProbeNodeEmptyURL(t *testing.T) {
	if _, err := ProbeNode(context.Background(), "   ", http.DefaultClient); err == nil {
		t.Fatal("空地址应当报错")
	}
}

func TestProbeNodeNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("gateway down"))
	}))
	defer srv.Close()

	_, err := ProbeNode(context.Background(), srv.URL, srv.Client())
	if err == nil {
		t.Fatal("503 应当报错")
	}
	if _, ok := err.(*nodeError); !ok {
		t.Errorf("应当返回 *nodeError，得到 %T", err)
	}
}

// 排好序之后，可用的排在不通的之前；同可用时快的在前。
func TestProbeNodesSortsAvailableFirst(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(60 * time.Millisecond)
		_, _ = w.Write([]byte(`{"height":10,"network":"n"}`))
	}))
	defer slow.Close()

	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"height":10,"network":"n"}`))
	}))
	defer fast.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer dead.Close()

	probes := ProbeNodes(context.Background(), []string{dead.URL, slow.URL, fast.URL}, fast.Client())
	if len(probes) != 3 {
		t.Fatalf("期望 3 条结果，得到 %d", len(probes))
	}
	if probes[0].Err != nil {
		t.Fatalf("第一条应当可用，得到错误 %v", probes[0].Err)
	}
	if probes[0].Info.URL != fast.URL {
		t.Errorf("第一条期望最快的那个，得到 %q", probes[0].Info.URL)
	}
	if probes[1].Info == nil || probes[1].Info.URL != slow.URL {
		t.Errorf("第二条期望慢的那个")
	}
	if probes[2].Err == nil {
		t.Error("最后一条应当是不通的那个")
	}
}

// 不通的探测结果里，Info 为空但地址必须留住——要能告诉用户是哪个挂了。
func TestProbeNodesKeepsFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("nope"))
	}))
	defer srv.Close()

	probes := ProbeNodes(context.Background(), []string{srv.URL}, srv.Client())
	if len(probes) != 1 || probes[0].Err == nil {
		t.Fatal("期望一条失败的探测结果")
	}
	if probes[0].URL != srv.URL {
		t.Errorf("失败结果也要留住地址，期望 %q，得到 %q", srv.URL, probes[0].URL)
	}
	if probes[0].Info != nil {
		t.Error("失败时 Info 应为空")
	}
}

func TestProbeNodesEmptyInput(t *testing.T) {
	if probes := ProbeNodes(context.Background(), nil, http.DefaultClient); probes != nil {
		t.Errorf("空输入应返回 nil，得到 %v", probes)
	}
}

// KnownNodes 的第一项必须是默认节点，界面上它排在最前。
func TestKnownNodesStartsWithDefault(t *testing.T) {
	if len(KnownNodes) == 0 {
		t.Fatal("KnownNodes 不应为空")
	}
	if KnownNodes[0] != DefaultNode {
		t.Errorf("第一项期望 %q，得到 %q", DefaultNode, KnownNodes[0])
	}
	for _, n := range KnownNodes {
		if n == "" || n[len(n)-1] == '/' {
			t.Errorf("节点地址不应为空或以斜杠结尾: %q", n)
		}
	}
}
