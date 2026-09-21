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
	"sync"
	"time"

	"github.com/LWDJD/read-only-git/internal/arweave"
)

// Request 是一次待签名任务，页面从 /api/next 拿到它。
type Request struct {
	ID   string        `json:"id"`
	Tags []arweave.Tag `json:"tags"`
}

// Service 是本机签名服务。
type Service struct {
	mu       sync.Mutex
	items    map[string]*item
	order    []string
	page     []byte
	listener net.Listener
	server   *http.Server
}

type item struct {
	id       string
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
	}
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

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/api/next", s.handleNext)
	mux.HandleFunc("/api/blob/", s.handleBlob)
	mux.HandleFunc("/api/sign/", s.handleSign)

	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = s.server.Serve(ln) }()
	return nil
}

// URL 返回签名页的地址，供用户打开。
func (s *Service) URL() string {
	if s.listener == nil {
		return ""
	}
	return "http://" + s.listener.Addr().String() + "/"
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
	it := &item{
		id:     newID(),
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
	writeJSON(w, Request{ID: next.id, Tags: next.tags})
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
