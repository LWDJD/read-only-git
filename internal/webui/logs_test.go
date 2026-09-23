package webui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LWDJD/read-only-git/internal/publish"
)

// 任务日志要落盘。
//
// 这是实际踩到的：发布失败之后想回头查，界面上的日志早被下一次操作冲掉了，
// 什么都没剩下。日志文件放在站点的 .rog/logs 下——与发布记录同一个地方，
// 整体不进版本库、也不参与发布。
func TestTaskLogsArePersisted(t *testing.T) {
	site := t.TempDir()
	srv := newTestServer(t, site)

	// 起一个必然失败的任务
	code, out := postJSON(t, srv.baseURL()+"api/pack", map[string]any{
		"source": filepath.Join(t.TempDir(), "does-not-exist"),
		"outDir": site,
	})
	if code != 200 {
		t.Fatalf("建任务应当返回 200，实际 %d", code)
	}
	taskID, _ := out["taskId"].(string)

	var snap map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
		if snap["status"] != "running" {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	logDir := filepath.Join(site, publish.StateDir, "logs")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("应当有日志目录 %s: %v", logDir, err)
	}
	var logs []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			logs = append(logs, e.Name())
		}
	}
	if len(logs) == 0 {
		t.Fatal("应当至少落下一个日志文件")
	}

	raw, err := os.ReadFile(filepath.Join(logDir, logs[0]))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "pack") {
		t.Fatalf("日志里应当看得出这是哪个任务，实际:\n%s", body)
	}
	if !strings.Contains(body, "失败") && !strings.Contains(body, "error") {
		t.Fatalf("失败原因应当落在日志里，实际:\n%s", body)
	}
}

// 旧日志要能被清掉，不能无限堆积。
//
// 保留份数由 Store.retain 定；这里把跑一轮之后的总数盯住。
func TestTaskLogsArePruned(t *testing.T) {
	site := t.TempDir()
	srv := newTestServer(t, site)
	srv.tasks.retain = 3

	for i := 0; i < 6; i++ {
		_, out := postJSON(t, srv.baseURL()+"api/pack", map[string]any{
			"source": filepath.Join(t.TempDir(), "nope"),
			"outDir": site,
		})
		taskID, _ := out["taskId"].(string)
		deadline := time.Now().Add(10 * time.Second)
		var snap map[string]any
		for time.Now().Before(deadline) {
			getJSON(t, srv.baseURL()+"api/task/"+taskID, &snap)
			if snap["status"] != "running" {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	logDir := filepath.Join(site, publish.StateDir, "logs")
	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			n++
		}
	}
	// retain 是 3，跑之前先清一次，所以最多是 retain 份
	if n > srv.tasks.retain+1 {
		t.Fatalf("日志文件应当被清到 %d 份以内，实际 %d 份", srv.tasks.retain+1, n)
	}
}
