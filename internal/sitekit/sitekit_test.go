package sitekit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 骨架里必须有关键文件，否则部署出来是个打不开的站点。
func TestTemplateHasEntryFiles(t *testing.T) {
	files, err := Files("default")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("模板里一个文件都没有")
	}

	need := []string{"index.html", "src/main.js", "src/style.css", "src/git/repo.js"}
	have := map[string]bool{}
	for _, f := range files {
		have[f] = true
	}
	for _, n := range need {
		if !have[n] {
			t.Fatalf("模板里缺少 %s", n)
		}
	}
}

// 部署到空目录应当把全部文件写出来。
func TestMaterializeWritesEveryFile(t *testing.T) {
	dir := t.TempDir()

	written, err := Materialize("default", dir, false)
	if err != nil {
		t.Fatal(err)
	}

	all, _ := Files("default")
	if len(written) != len(all) {
		t.Fatalf("应当写入 %d 个文件，实际 %d", len(all), len(written))
	}
	for _, rel := range all {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s 没被写出来: %v", rel, err)
		}
	}

	// 写完就不该再缺东西
	missing, err := Missing("default", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("写完之后不该还缺文件: %v", missing)
	}
}

// 默认不覆盖：站点里的 index.html 可能被维护者改过，不能拿模板盖掉。
func TestMaterializeKeepsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.html")
	if err := os.WriteFile(target, []byte("我改过的首页"), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := Materialize("default", dir, false)
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "我改过的首页" {
		t.Fatal("默认不该覆盖已存在的文件")
	}
	for _, rel := range written {
		if rel == "index.html" {
			t.Fatal("已存在的文件不该出现在写入清单里")
		}
	}
}

// 强制覆盖时应当把内容刷新成模板里的。
func TestMaterializeOverwriteRefreshes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "index.html")
	if err := os.WriteFile(target, []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Materialize("default", dir, true); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "旧内容" {
		t.Fatal("强制覆盖应当刷新内容")
	}
	if !strings.Contains(string(got), "<!doctype html>") {
		t.Fatalf("覆盖后的内容不像模板首页: %q", got)
	}
}

// 空目录该被当成「全都缺」。
func TestMissingOnEmptyDir(t *testing.T) {
	dir := t.TempDir()

	missing, err := Missing("default", dir)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := Files("default")
	if len(missing) != len(all) {
		t.Fatalf("空目录应当全都缺，实际缺 %d / 共 %d", len(missing), len(all))
	}
}

// 不存在的模板要报错，而不是给一份空清单。
func TestUnknownTemplate(t *testing.T) {
	if _, err := Files("nope"); err == nil {
		t.Fatal("未知模板应当报错")
	}
	if _, err := Materialize("nope", t.TempDir(), false); err == nil {
		t.Fatal("未知模板应当报错")
	}
}

// 模板里的 repository.json 必须是合法 JSON，且是个空清单。
//
// 站点根上少了它，界面会报「读不到 repository.json」，
// pack 之后才会被覆盖成真正的清单。
func TestTemplateRegistryIsEmptyAndValid(t *testing.T) {
	dir := t.TempDir()
	if _, err := Materialize("default", dir, false); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "repository.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"repositories\"") {
		t.Fatalf("repository.json 内容不对: %q", data)
	}
}
