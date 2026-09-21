package publish

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

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
