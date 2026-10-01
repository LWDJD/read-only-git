package webui

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 前端可以把一条消息记进任务日志。
//
// 必要性来自实测：界面上的「签名失败：…」是前端写的，而日志文件只收
// 后端的 Logf，结果出了事翻日志，最关键的那句偏偏不在。
// 日志里只有三行、停在「等待钱包确认」，全靠猜。
func TestTaskNoteLandsInTaskLog(t *testing.T) {
	site := t.TempDir()
	srv := newTestServer(t, site)

	// 起一个能跑一会儿的任务：拿一个不存在的源，它会很快失败，
	// 但失败之前任务已经建好，note 能挂上去。
	_, out := postJSON(t, srv.baseURL()+"api/pack", map[string]any{
		"source": filepath.Join(t.TempDir(), "nope"),
		"outDir": site,
	})
	taskID, _ := out["taskId"].(string)
	if taskID == "" {
		t.Fatal("应当返回 taskId")
	}

	// 立刻发一条 note，趁任务还没收尾
	code, _ := postJSON(t, srv.baseURL()+"api/task/"+taskID+"/note", map[string]any{
		"line": "签名失败：交易报给节点后查不到它",
	})
	if code != http.StatusOK {
		t.Fatalf("note 应当被接受，实际 %d", code)
	}

	// 等任务结束
	var snap map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
		if snap["status"] != "running" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	logs, _ := snap["logs"].([]any)
	found := false
	for _, l := range logs {
		if s, _ := l.(string); strings.Contains(s, "签名失败：交易报给节点后查不到它") {
			found = true
		}
	}
	if !found {
		t.Fatalf("页面发来的消息应当出现在任务日志里，实际:\n%v", logs)
	}
	// 带上来源前缀，翻日志时能分清哪句是页面写的
	for _, l := range logs {
		if s, _ := l.(string); strings.Contains(s, "签名失败") && !strings.HasPrefix(s, "[页面]") {
			t.Fatalf("页面来的消息应当带 [页面] 前缀，实际 %q", s)
		}
	}
}

// 给不存在的任务发 note 要被拒，而不是悄悄吞掉。
func TestTaskNoteRejectsUnknownTask(t *testing.T) {
	srv := newTestServer(t, t.TempDir())

	code, _ := postJSON(t, srv.baseURL()+"api/task/nope-1/note", map[string]any{
		"line": "随便一句",
	})
	if code != http.StatusNotFound {
		t.Fatalf("不存在的任务应当返回 404，实际 %d", code)
	}
}

// 空消息是无害的空操作，不该报错。
//
// 前端可能在某些分支上传空串，为此回一个错会让界面显示无意义的红字。
func TestTaskNoteAcceptsEmptyLine(t *testing.T) {
	site := t.TempDir()
	srv := newTestServer(t, site)

	_, out := postJSON(t, srv.baseURL()+"api/pack", map[string]any{
		"source": filepath.Join(t.TempDir(), "nope"),
		"outDir": site,
	})
	taskID, _ := out["taskId"].(string)

	code, _ := postJSON(t, srv.baseURL()+"api/task/"+taskID+"/note", map[string]any{
		"line": "   ",
	})
	if code != http.StatusOK {
		t.Fatalf("空消息应当被接受，实际 %d", code)
	}
}
