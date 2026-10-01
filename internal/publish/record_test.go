package publish

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 记录要能被找到：精确名对不上时（换目录、从链上恢复后身份哈希漂移），
// 扫 .rog/ 里已有的那份——账本一直躺在那里，别重建它。
func TestFindStatePathFallsBackToExistingFile(t *testing.T) {
	root := t.TempDir()
	rog := filepath.Join(root, StateDir)
	if err := os.MkdirAll(rog, 0o755); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(rog, "publish-arweave-ffffffff.json")
	if err := os.WriteFile(restored, []byte(`{"target":"arweave","files":{},"refs":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := FindStatePath(root, "arweave", "totally-different-name"); got != restored {
		t.Fatalf("应当找到恢复来的记录，实际 %q", got)
	}

	// 精确名在时用精确名
	exact := StatePath(root, "arweave", "mine")
	if err := os.WriteFile(exact, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindStatePath(root, "arweave", "mine"); got != exact {
		t.Fatalf("精确名存在时应当用它，实际 %q", got)
	}

	// 都没有时给标准名（首次发布会往那里写）
	if got := FindStatePath(root, "arweave", "fresh"); got != StatePath(root, "arweave", "fresh") {
		t.Fatalf("无记录时应当给标准名，实际 %q", got)
	}
}

// 站点内相对路径与本地绝对路径必须指向同一个文件，
// 否则「上链的是这个、本地读的是那个」会悄悄错开。
func TestRecordRelPathMatchesStatePath(t *testing.T) {
	root := t.TempDir()
	rel := RecordRelPath("arweave", "demo")
	abs := StatePath(root, "arweave", "demo")

	if want := filepath.Join(root, filepath.FromSlash(rel)); abs != want {
		t.Fatalf("两个路径应指向同一个文件\n rel=%s\n abs=%s\n want=%s", rel, abs, want)
	}
	if strings.Contains(rel, `\`) {
		t.Fatalf("站点内路径要用 / 分隔，实际 %q", rel)
	}
	if !strings.HasPrefix(rel, StateDir+"/") {
		t.Fatalf("记录应落在 %s 下，实际 %q", StateDir, rel)
	}
}

// 不同身份要落到不同文件，同一身份必须稳定。
// 前者防止两个目的地互相覆盖，后者防止每次发布都写出一份新记录。
func TestRecordIdentitySeparatesTargets(t *testing.T) {
	a := RecordRelPath("arweave", "demo")
	b := RecordRelPath("arweave", "other")
	if a == b {
		t.Fatalf("不同仓库名要给不同路径，两个都是 %q", a)
	}
	if again := RecordRelPath("arweave", "demo"); again != a {
		t.Fatalf("同一身份应当稳定，得到 %q 与 %q", again, a)
	}
}

func TestMarshalRecordKeepsAllThreeMaps(t *testing.T) {
	rec := &Record{
		Target: "arweave",
		Root:   "entry",
		Files:  map[string]string{"a.txt": "d1"},
		Refs:   map[string]string{"a.txt": "id-1"},
		Labels: map[string]string{"repo": "demo"},
	}
	data, err := MarshalRecord(rec)
	if err != nil {
		t.Fatal(err)
	}

	var back Record
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Files["a.txt"] != "d1" || back.Refs["a.txt"] != "id-1" || back.Labels["repo"] != "demo" {
		t.Fatalf("三个映射都要完整往返: %+v", back)
	}
}

// SaveRecord 与 MarshalRecord 共用同一套字节，本地读写要自洽。
func TestSaveRecordRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := StatePath(root, "arweave", "demo")
	rec := &Record{
		Target: "arweave",
		Files:  map[string]string{"a.txt": "d1"},
		Refs:   map[string]string{"a.txt": "id-1"},
	}
	if err := SaveRecord(path, rec); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Files["a.txt"] != "d1" || got.Refs["a.txt"] != "id-1" {
		t.Fatalf("本地记录的读写要自洽: %+v", got)
	}
}
