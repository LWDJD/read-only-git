// Package signer 提供一个本地服务，把「待签名内容」交给浏览器里的钱包插件，
// 拿回签名后的 ANS-104 data item。
//
// 为什么需要这一层：私钥在钱包里，Go 侧签不了名，也没有办法直接调用浏览器扩展。
// 唯一可行的通道是「本地 HTTP + 用户打开的页面」：Go 把内容摆在只有本机能访问的
// 端点上，页面取走、交给钱包签、再把结果送回来。私钥始终不出钱包。
package signer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/LWDJD/read-only-git/internal/arweave"
)

// Request 是一次待签名任务，页面从 /api/next 拿到它。
//
// Kind 告诉页面这次要做什么：给 data item 签个名，还是给一笔交易签个名。
type Request struct {
	ID   string        `json:"id"`
	Kind string        `json:"kind,omitempty"`
	Tags []arweave.Tag `json:"tags"`
}

// 任务类型。dataitem 回传签名字节，tx 回传一笔交易的字段。
const (
	KindDataItem = "dataitem"
	KindTx       = "tx"
)

// Service 是本机签名服务。
type Service struct {
	mu       sync.Mutex
	items    map[string]*item
	order    []string
	page     []byte
	token    string
	listener net.Listener
	server   *http.Server
}

type item struct {
	id       string
	kind     string
	data     []byte
	tags     []arweave.Tag
	result   chan []byte
	finished bool
}

// New 创建一个签名服务；page 是交给浏览器的签名页 HTML。
func New(page []byte) *Service {
	return &Service{
		items: make(map[string]*item),
		page:  page,
		token: newToken(),
	}
}

// newToken 生成一个随机的会话 token，方式与 webui 那边一致。
//
// 两处各自实现一份而不共用一个内部包：它只有十行，
// 为此把两个不相干的包绁在一起不划算。
func newToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// Start 在 127.0.0.1 的随机端口上开始监听。
//
// 只绑本机回环地址：这个端点会把待上链的内容原样吐出来，不能让同网段的
// 其他人碰到。
func (s *Service) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.listener = ln

	s.server = &http.Server{
		Handler:           s.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = s.server.Serve(ln) }()
	return nil
}

// Routes 返回服务的一整套路由。
//
// 单独拿出来是为了让 webui 能把它挂到自己的 mux 下：那样就不必再起一个
// 端口、让用户多开一个页面。CLI 那条路仍旧自己监听。
func (s *Service) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.guard(s.handlePage))
	mux.HandleFunc("/api/next", s.guard(s.handleNext))
	mux.HandleFunc("/api/blob/", s.guard(s.handleBlob))
	mux.HandleFunc("/api/sign/", s.guard(s.handleSign))
	// arweave-js 的浏览器构建随页面一起发，内嵌而不是走 CDN：
	// 签名页在本机跑，不该依赖外网才能工作。
	//
	// 它不套 token：是公开的第三方库，不含任何与本机状态有关的东西，
	// 而且 <script src> 带不了请求头。
	mux.HandleFunc("/vendor/arweave.js", s.handleVendor(arweaveBundle, "text/javascript; charset=utf-8"))
	mux.HandleFunc("/vendor/arweave-LICENSE.txt", s.handleVendor(arweaveLicense, "text/plain; charset=utf-8"))
	return mux
}

// SetToken 换一个访问 token。
//
// webui 把它挂到自己的 mux 上时用这个：请求那时已经在 webui 的 guard
// 里验过一次，两边共用一个 token，页面才不用同时带着两个。
func (s *Service) SetToken(t string) {
	if t == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = t
}

// URL 返回签名页的地址，带上访问 token。
func (s *Service) URL() string {
	if s.listener == nil {
		return ""
	}
	return s.baseURL() + "?token=" + s.token
}

// baseURL 返回不含 token 的根地址（带尾斜杠），供内部与测试拼接路径。
func (s *Service) baseURL() string {
	if s.listener == nil {
		return ""
	}
	return "http://" + s.listener.Addr().String() + "/"
}

// Token 返回本次会话的访问 token。
func (s *Service) Token() string { return s.token }

// guard 给处理器套上 token 校验。
//
// 这个端点会把待上链的内容原样吐出来，比 webui 那边更要紧：
// 光绑回环还不够，同机的任意网页都能向它发请求。
func (s *Service) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.tokenOK(r) {
			http.Error(w, "缺少或错误的 token，请用启动时打印的地址访问", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// tokenOK 接受两种带法：header 给普通请求，query 是给 EventSource 一类
// 无法自定义请求头的场合留的。
func (s *Service) tokenOK(r *http.Request) bool {
	if r.Header.Get("X-Rog-Token") == s.token {
		return true
	}
	return r.URL.Query().Get("token") == s.token
}

// Close 关掉服务。
func (s *Service) Close() error {
	if s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

// Sign 实现 arweave.Signer：把内容排进待签队列，等页面把结果送回来。
func (s *Service) Sign(ctx context.Context, data []byte, tags []arweave.Tag) ([]byte, error) {
	return s.enqueue(ctx, KindDataItem, data, tags)
}

// SignTx 实现 arweave.TxSigner：让钱包给一笔「data 就是这个数据」的交易签名。
//
// 页面回传的是交易字段而不是整笔交易：data 可能是整个 bundle，回传它要多走
// 一次 base64，而那串字节 Go 这边本来就有，自己拼更省。
func (s *Service) SignTx(ctx context.Context, data []byte, tags []arweave.Tag) (*arweave.TxSignature, error) {
	raw, err := s.enqueue(ctx, KindTx, data, tags)
	if err != nil {
		return nil, err
	}
	var sig arweave.TxSignature
	if err := json.Unmarshal(raw, &sig); err != nil {
		return nil, fmt.Errorf("钱包回传的交易字段无法解析: %w", err)
	}
	if sig.ID == "" || sig.Owner == "" || sig.Signature == "" {
		return nil, fmt.Errorf("钱包回传的交易字段不完整（缺 id / owner / signature）")
	}
	// data_root 同样不能缺：签名算的就是它，交易 JSON 里要用。
	if sig.DataRoot == "" {
		return nil, fmt.Errorf("钱包回传的交易字段不完整（缺 data_root）")
	}
	return &sig, nil
}

// enqueue 把一次签名任务排进队列，并等页面把结果送回来。
func (s *Service) enqueue(ctx context.Context, kind string, data []byte, tags []arweave.Tag) ([]byte, error) {
	it := &item{
		id:     newID(),
		kind:   kind,
		data:   data,
		tags:   tags,
		result: make(chan []byte, 1),
	}

	s.mu.Lock()
	s.items[it.id] = it
	s.order = append(s.order, it.id)
	s.mu.Unlock()

	select {
	case signed := <-it.result:
		if len(signed) == 0 {
			return nil, fmt.Errorf("钱包返回了空的签名结果")
		}
		return signed, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Pending 返回还没被处理的待签名任务数（供 CLI 提示进度）。
func (s *Service) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, id := range s.order {
		if it := s.items[id]; it != nil && !it.finished {
			n++
		}
	}
	return n
}

// ---------- HTTP ----------

// handleVendor 提供内嵌的静态资源。
func (s *Service) handleVendor(body []byte, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}
}

func (s *Service) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.page)
}

func (s *Service) handleNext(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	var next *item
	for _, id := range s.order {
		if it := s.items[id]; it != nil && !it.finished {
			next = it
			break
		}
	}
	s.mu.Unlock()

	if next == nil {
		// 空 id 表示「暂时没有任务」，页面据此决定继续等待还是收工
		writeJSON(w, Request{})
		return
	}
	writeJSON(w, Request{ID: next.id, Kind: next.kind, Tags: next.tags})
}

func (s *Service) handleBlob(w http.ResponseWriter, r *http.Request) {
	id := trimPrefix(r.URL.Path, "/api/blob/")
	it := s.get(id)
	if it == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(it.data)
}

func (s *Service) handleSign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "需要 POST", http.StatusMethodNotAllowed)
		return
	}
	id := trimPrefix(r.URL.Path, "/api/sign/")

	it := s.get(id)
	if it == nil {
		http.NotFound(w, r)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	already := it.finished
	it.finished = true
	s.mu.Unlock()

	if already {
		http.Error(w, "这个任务已经签过了", http.StatusConflict)
		return
	}

	it.result <- body
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) get(id string) *item {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.items[id]
}

// ---------- 小工具 ----------

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func trimPrefix(path, prefix string) string {
	if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
		return path[len(prefix):]
	}
	return ""
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
