package webui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status 是任务的状态。
type Status string

const (
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Task 是一次耗时操作（打包、发布、恢复）的执行记录。
//
// 把长操作做成任务而不是让它堵在一个 HTTP 请求里：页面拿到 taskId 就立刻返回，
// 日志用 SSE 实时推。中途刷新页面也不会丢进度。
type Task struct {
	id   string
	kind string

	mu      sync.Mutex
	status  Status
	logs    []string
	errMsg  string
	result  any
	data    map[string]any
	started time.Time
	ended   time.Time

	// sink 非空时日志同时写一份到磁盘。
	//
	// 为什么要有它：界面上的日志一刷新就没了，而「发布失败」这类事
	// 往往要事后回头查——过了那一瞬就什么都追不回来。
	sink io.WriteCloser

	subs map[chan string]struct{}
}

func newTask(id, kind string) *Task {
	return &Task{
		id:      id,
		kind:    kind,
		status:  StatusRunning,
		started: time.Now(),
		subs:    make(map[chan string]struct{}),
	}
}

// Logf 记一行日志，并推给所有订阅者。
//
// 推送用非阻塞写法：某个订阅者读得慢（比如页面卡住），不能拖住任务本身。
func (t *Task) Logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)

	t.mu.Lock()
	t.logs = append(t.logs, line)
	if t.sink != nil {
		// 落盘失败不能影响任务本身，所以不检查错误：
		// 日志是辅助，不是主流程。
		_, _ = fmt.Fprintln(t.sink, line)
	}
	subs := make([]chan string, 0, len(t.subs))
	for ch := range t.subs {
		subs = append(subs, ch)
	}
	t.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- line:
		default:
		}
	}
}

func (t *Task) succeed(result any) {
	t.mu.Lock()
	t.status = StatusDone
	t.result = result
	t.ended = time.Now()
	if t.sink != nil {
		_, _ = fmt.Fprintln(t.sink, "完成")
	}
	t.closeSinkLocked()
	subs := t.takeSubsLocked()
	t.mu.Unlock()

	for _, ch := range subs {
		close(ch)
	}
}

func (t *Task) fail(err error) {
	t.mu.Lock()
	t.status = StatusFailed
	t.errMsg = err.Error()
	t.ended = time.Now()
	if t.sink != nil {
		// 失败原因不走 Logf，所以这里得自己补一笔。
		// 不写的话日志文件里只剩个开头，正是最想看的那句不在。
		_, _ = fmt.Fprintf(t.sink, "失败：%s\n", t.errMsg)
	}
	t.closeSinkLocked()
	subs := t.takeSubsLocked()
	t.mu.Unlock()

	for _, ch := range subs {
		close(ch)
	}
}

// closeSinkLocked 收尾时把日志文件关掉。调用方持锁。
func (t *Task) closeSinkLocked() {
	if t.sink == nil {
		return
	}
	_ = t.sink.Close()
	t.sink = nil
}

// takeSubsLocked 取走并清空订阅者列表，调用方负责在锁外关闭通道。
//
// 先清空再关闭，是为了让并发的 unsubscribe 发现通道已不在表里而不再动它，
// 避免同一个通道被关两次。
func (t *Task) takeSubsLocked() []chan string {
	out := make([]chan string, 0, len(t.subs))
	for ch := range t.subs {
		out = append(out, ch)
	}
	t.subs = make(map[chan string]struct{})
	return out
}

// Logs 返回已有日志的快照，供刚接上的订阅者补齐历史。
func (t *Task) Logs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.logs...)
}

// SetData 给任务挂一份前端需要的附加信息。
//
// 典型用途是发布任务刚起签名服务时，把签名页地址立刻告知前端，
// 否则只能从日志里猜。
func (t *Task) SetData(key string, value any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.data == nil {
		t.data = map[string]any{}
	}
	t.data[key] = value
}

// Snapshot 返回给前端看的当前状态。
func (t *Task) Snapshot() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := map[string]any{
		"id":     t.id,
		"kind":   t.kind,
		"status": string(t.status),
		"logs":   append([]string(nil), t.logs...),
	}
	if t.errMsg != "" {
		out["error"] = t.errMsg
	}
	if t.result != nil {
		out["result"] = t.result
	}
	if t.data != nil {
		out["data"] = t.data
	}
	return out
}

// Subscribe 订阅后续日志。第二个返回值用于退订，调用方必须调用它。
func (t *Task) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 64)

	t.mu.Lock()
	// 已经结束的任务不会再有新日志，直接关掉通道让调用方收尾
	if t.status != StatusRunning {
		close(ch)
		t.mu.Unlock()
		return ch, func() {}
	}
	t.subs[ch] = struct{}{}
	t.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			t.mu.Lock()
			if _, ok := t.subs[ch]; ok {
				delete(t.subs, ch)
				close(ch)
			}
			t.mu.Unlock()
		})
	}
	return ch, cancel
}

// Store 保存所有任务。
type Store struct {
	mu    sync.Mutex
	tasks map[string]*Task
	seq   int64
	// logDir 非空时，每个任务的日志同时写一份到这里。
	logDir string
	// retain 是保留多少份历史日志，超出就删最旧的。
	retain int
}

func NewStore() *Store {
	return &Store{tasks: make(map[string]*Task), retain: 50}
}

// SetLogDir 打开日志落盘。
//
// 为什么不在这里报错：日志是辅助能力，目录建不出来最多是这次的
// 任务没有文件，不该因此把整个界面拦住。
func (s *Store) SetLogDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logDir = dir
}

// LogDir 返回日志落脚点。抽出来是为了让用例与界面都能问出它。
func (s *Store) LogDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logDir
}

// Run 建一个任务并异步执行它，返回任务 id。
func (s *Store) Run(kind string, fn func(*Task)) string {
	s.mu.Lock()
	s.seq++
	id := fmt.Sprintf("%s-%d", kind, s.seq)
	t := newTask(id, kind)
	if f := s.openLogFileLocked(id, kind); f != nil {
		t.sink = f
		t.logs = append(t.logs, "日志文件已开，出问题时可以回头翻")
	}
	s.tasks[id] = t
	s.mu.Unlock()

	go func() {
		defer func() {
			// 任务里的 panic 不该带走整个服务，转成一条失败记录
			if r := recover(); r != nil {
				t.fail(fmt.Errorf("任务内部出错: %v", r))
			}
		}()
		fn(t)
	}()

	return id
}

func (s *Store) Get(id string) *Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tasks[id]
}

// openLogFileLocked 为一次任务开一个日志文件。调用方持锁。
//
// 文件名以时间开头，字典序就是时间序，清理旧文件时直接按名字排即可。
func (s *Store) openLogFileLocked(id, kind string) io.WriteCloser {
	if s.logDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.logDir, 0o755); err != nil {
		return nil
	}
	s.pruneLogsLocked()

	name := time.Now().Format("2006-01-02_15-04-05") + "_" + kind + ".log"
	f, err := os.Create(filepath.Join(s.logDir, name))
	if err != nil {
		return nil
	}
	_, _ = fmt.Fprintf(f, "# 任务 %s（%s）开始于 %s\n", id, kind, time.Now().Format("2006-01-02 15:04:05"))
	return f
}

// pruneLogsLocked 只留最近 retain 份日志。调用方持锁。
//
// 认不出目录内容就算了：这是清理，不是主流程，失败不影响什么。
func (s *Store) pruneLogsLocked() {
	entries, err := os.ReadDir(s.logDir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	if len(names) < s.retain {
		return
	}
	sort.Strings(names)
	for _, n := range names[:len(names)-s.retain+1] {
		_ = os.Remove(filepath.Join(s.logDir, n))
	}
}
