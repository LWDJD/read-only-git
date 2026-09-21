package publish

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDigestStable(t *testing.T) {
	a := Digest([]byte("hello"))
	b := Digest([]byte("hello"))
	c := Digest([]byte("world"))
	if a != b {
		t.Fatal("digest should be deterministic")
	}
	if a == c {
		t.Fatal("different content should differ")
	}
	if len(a) != 64 {
		t.Fatalf("want 64 hex chars, got %d", len(a))
	}
}

func TestScanSkipsStateDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "hello")
	writeFile(t, filepath.Join(root, "sub", "b.txt"), "world")
	writeFile(t, filepath.Join(root, StateDir, "publish-local.json"), "{}")

	site, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(site.Files) != 2 {
		t.Fatalf("want 2 files, got %d: %+v", len(site.Files), site.Files)
	}
	// 清单应有序
	if site.Files[0].Path != "a.txt" || site.Files[1].Path != "sub/b.txt" {
		t.Fatalf("unexpected order: %+v", site.Files)
	}
}

// .rog 以文件形态出现时也不能当内容发布：否则会先拷过去、再在写记录时报错，
// 期间已经产生了副作用。
func TestScanSkipsStateFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.txt"), "hello")
	writeFile(t, filepath.Join(root, StateDir), "this is a plain file, not a dir")

	site, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(site.Files) != 1 || site.Files[0].Path != "a.txt" {
		t.Fatalf(".rog 文件形态不该被当成内容: %+v", site.Files)
	}
}

func TestScanUsesSlashPaths(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "b", "c.txt"), "x")

	site, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(site.Files) != 1 || site.Files[0].Path != "a/b/c.txt" {
		t.Fatalf("want a/b/c.txt, got %+v", site.Files)
	}
}

func TestLocalPublishFirstIsFullCopy(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), "hello")
	writeFile(t, filepath.Join(src, "b.txt"), "world")
	dst := t.TempDir()

	site, err := Scan(src)
	if err != nil {
		t.Fatal(err)
	}
	l := &Local{Dir: dst}
	rec, err := l.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Files) != 2 {
		t.Fatalf("want 2 record entries, got %d", len(rec.Files))
	}
	if rec.Target != "local" || rec.Root == "" {
		t.Fatalf("record metadata not set: %+v", rec)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Fatalf("%s not copied: %v", name, err)
		}
	}
}

func TestLocalPublishReusesUnchanged(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), "hello")
	writeFile(t, filepath.Join(src, "b.txt"), "world")
	dst := t.TempDir()

	site, _ := Scan(src)
	l := &Local{Dir: dst}
	rec1, err := l.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 改一个文件，重扫
	writeFile(t, filepath.Join(src, "b.txt"), "changed")
	site2, _ := Scan(src)

	changed := Changed(site2, rec1)
	if len(changed) != 1 || changed[0] != "b.txt" {
		t.Fatalf("want [b.txt], got %v", changed)
	}

	rec2, err := l.Publish(context.Background(), site2, rec1)
	if err != nil {
		t.Fatal(err)
	}

	// b.txt 已更新，a.txt 的凭据被复用
	data, err := os.ReadFile(filepath.Join(dst, "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "changed" {
		t.Fatalf("b.txt not updated: %q", data)
	}
	if rec2.Files["a.txt"] != rec1.Files["a.txt"] {
		t.Fatal("a.txt should be reused with same digest")
	}
	if rec2.Files["b.txt"] == rec1.Files["b.txt"] {
		t.Fatal("b.txt digest should have changed")
	}
}

func TestLocalPublishRecopiesWhenTargetMissing(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), "hello")
	dst := t.TempDir()

	site, _ := Scan(src)
	l := &Local{Dir: dst}
	rec, err := l.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 目标文件被删掉后，即使摘要没变也必须重新复制
	if err := os.Remove(filepath.Join(dst, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Publish(context.Background(), site, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a.txt")); err != nil {
		t.Fatalf("a.txt should be recopied: %v", err)
	}
}

func TestRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := StatePath(dir, "local", "/somewhere")

	rec := &Record{
		Target: "local",
		Root:   "/somewhere",
		Files:  map[string]string{"a.txt": "deadbeef"},
	}
	if err := SaveRecord(path, rec); err != nil {
		t.Fatal(err)
	}

	got, err := LoadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("record should load")
	}
	if got.Root != "/somewhere" || got.Files["a.txt"] != "deadbeef" {
		t.Fatalf("bad round trip: %+v", got)
	}
}

func TestLoadRecordMissingFile(t *testing.T) {
	got, err := LoadRecord(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if got != nil {
		t.Fatal("expected nil record")
	}
}

func TestChangedWithNilPrev(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), "x")
	site, _ := Scan(src)
	if n := len(Changed(site, nil)); n != 1 {
		t.Fatalf("nil prev should treat all as changed, got %d", n)
	}
}

// 记录损坏时返回错误，但不应该是文件不存在的情形。
func TestLoadRecordCorruptedReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := StatePath(dir, "local", "")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{ bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := LoadRecord(path)
	if err == nil {
		t.Fatal("损坏的记录应返回错误，而不是静默当成无记录")
	}
	if rec != nil {
		t.Fatal("损坏时不应返回记录")
	}
}

// 目标被外部篡改后，不能因为「文件存在」就跳过复制。
func TestLocalPublishRecopiesTamperedTarget(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), "hello")
	dst := t.TempDir()

	site, _ := Scan(src)
	l := &Local{Dir: dst}
	rec, err := l.Publish(context.Background(), site, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 篡改目标文件，站点侧没变
	writeFile(t, filepath.Join(dst, "a.txt"), "TAMPERED")

	if _, err := l.Publish(context.Background(), site, rec); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("篡改应被纠正，实际内容: %q", data)
	}
}

// 原子写入：临时文件不该残留在目标位置附近。
func TestSaveRecordLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := StatePath(dir, "local", "")
	rec := &Record{Target: "local", Root: "/x", Files: map[string]string{"a": "b"}}
	if err := SaveRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("临时文件不该残留")
	}
}
