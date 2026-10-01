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
// 什么都没剩下。日志放在用户目录下的 .rog/logs（见 DefaultLogDir），
// 不落进站点——它是本机运维产物，与站点的生命周期无关。
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

	logDir := srv.tasks.LogDir()
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

	logDir := srv.tasks.LogDir()
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

// 日志不落进站点：它是本机运维产物，与站点的生命周期无关。
//
// 这条是防回归：日志一旦落回 <站点>/.rog/logs，用户想拿那个目录
// 另作他用（比如当恢复目标）就会被永久挡住。
//
// 注意 .rog 里的**发布记录**是另一回事：那是有意上链的
// （见 publish.RecordRelPath 与 arweave 目标里的 RecordPath），
// 从链上恢复靠的正是它，不能跟日志一起移走。
func TestLogsAreNotInsideSite(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("取不到用户主目录")
	}
	want := filepath.Join(home, ".rog", "logs")

	got, err := DefaultLogDir()
	if err != nil {
		t.Fatalf("取默认日志目录失败: %v", err)
	}
	if got != want {
		t.Errorf("默认日志目录期望 %q，得到 %q", want, got)
	}

	site := t.TempDir()
	srv := New(site, 0)
	defer srv.Close()
	if dir := srv.tasks.LogDir(); dir == filepath.Join(site, publish.StateDir, "logs") {
		t.Error("日志不该落在站点目录里")
	}
}

// 发布记录必须留在站点里，而且要被 manifest 引用到——从链上恢复全靠它。
// 这条与上一条成对：日志移出去，记录留下来，别一起搬。
func TestPublishRecordStaysInSite(t *testing.T) {
	if got := publish.RecordRelPath("arweave", "demo"); !strings.HasPrefix(got, publish.StateDir+"/") {
		t.Errorf("记录路径应当在 %s 下，实际 %q", publish.StateDir, got)
	}
}
