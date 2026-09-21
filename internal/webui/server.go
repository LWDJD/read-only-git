// Package webui 提供一个本机图形界面，把维护器的功能都摆出来。
//
// 它只是命令行之上的一层壳：所有实际操作都走内部同一套实现，
// 不在界面侧另起一套逻辑。界面本身内嵌、零依赖，不联网加载任何资源。
//
// 一条硬约定：磁盘是唯一事实来源。界面不缓存文件状态，每次写操作完成后
// 前端重新拉 /api/state；打包与发布前一律重新扫描并逐文件重算摘要。
// 管理员绕过界面直接改目录时，界面不能骗人。
package webui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Server 是本机 webui 服务。
type Server struct {
	siteDir string
	port    int
	tasks   *Store

	listener net.Listener
	server   *http.Server
}

// New 创建一个 webui 服务。
//
// siteDir 是默认操作的站点目录；port 为 0 时由系统挑一个空闲端口，
// 传具体值时固定监听该端口，方便反复访问同一个地址。
func New(siteDir string, port int) *Server {
	return &Server{
		siteDir: siteDir,
		port:    port,
		tasks:   NewStore(),
	}
}

// Start 在 127.0.0.1 上开始监听。
//
// 只绑回环地址：这个服务能改站点文件、能发起发布，不能让同网段的其他人碰到。
func (s *Server) Start() error {
	addr := "127.0.0.1:0"
	if s.port > 0 {
		addr = fmt.Sprintf("127.0.0.1:%d", s.port)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = ln

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/pack", s.handlePack)
	mux.HandleFunc("/api/publish", s.handlePublish)
	mux.HandleFunc("/api/restore", s.handleRestore)
	mux.HandleFunc("/api/files/replace", s.handleFileReplace)
	mux.HandleFunc("/api/files/delete", s.handleFileDelete)
	mux.HandleFunc("/api/task/", s.handleTask)

	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = s.server.Serve(ln) }()
	return nil
}

// URL 返回界面地址。
func (s *Server) URL() string {
	if s.listener == nil {
		return ""
	}
	return "http://" + s.listener.Addr().String() + "/"
}

// SiteDir 返回默认站点目录。
func (s *Server) SiteDir() string { return s.siteDir }

// Close 关掉服务。
func (s *Server) Close() error {
	if s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

// ---------- 任务 ----------

// handleTask 同时照料两种请求：
//
//	/api/task/<id>          取当前状态
//	/api/task/<id>/events   SSE 流
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/task/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}

	if id, ok := strings.CutSuffix(rest, "/events"); ok {
		s.streamTask(w, r, id)
		return
	}

	t := s.tasks.Get(rest)
	if t == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("没有这个任务: %s", rest))
		return
	}
	writeJSON(w, t.Snapshot())
}

func (s *Server) streamTask(w http.ResponseWriter, r *http.Request, id string) {
	t := s.tasks.Get(id)
	if t == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("没有这个任务: %s", id))
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("当前连接不支持流式响应"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	// 先把已经产生的日志补一遍：页面中途刷新也能看到完整过程
	for _, line := range t.Logs() {
		fmt.Fprintf(w, "data: %s\n\n", escapeSSE(line))
	}
	flusher.Flush()

	ch, cancel := t.Subscribe()
	defer cancel()

	for {
		select {
		case <-r.Context().Done():
			return
		case line, alive := <-ch:
			if !alive {
				// 任务收尾，通道被关：发一个结束事件让前端停止重连
				fmt.Fprint(w, "event: end\ndata: done\n\n")
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", escapeSSE(line))
			flusher.Flush()
		}
	}
}

// escapeSSE 把换行转义成字面量：SSE 的 data 字段里出现裸换行会截断消息。
func escapeSSE(s string) string {
	return strings.ReplaceAll(s, "\n", "\\n")
}
