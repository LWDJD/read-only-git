package arweave

import "testing"

func TestManifestShape(t *testing.T) {
	m := NewManifest(map[string]string{
		"index.html": "id-index",
		"a.txt":      "id-a",
	}, "index.html")

	if m.Manifest != "arweave/paths" {
		t.Fatalf("schema = %q", m.Manifest)
	}
	if m.Version != ManifestSchemaVersion {
		t.Fatalf("version = %q", m.Version)
	}
	if m.Index == nil || m.Index.Path != "index.html" {
		t.Fatalf("index = %+v", m.Index)
	}
	if m.Paths["a.txt"].ID != "id-a" {
		t.Fatalf("paths = %+v", m.Paths)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	m := NewManifest(map[string]string{"x": "1"}, "")
	data, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	back, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.Paths["x"].ID != "1" {
		t.Fatalf("round trip 丢失路径: %+v", back)
	}
	if back.Index != nil {
		t.Fatal("未指定入口时 index 应为空")
	}
}

func TestManifestEmptyPaths(t *testing.T) {
	m := NewManifest(nil, "")
	data, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Paths) != 0 {
		t.Fatalf("空 manifest 不该有路径: %+v", back.Paths)
	}
}

func TestContentTags(t *testing.T) {
	tags := ContentTags("demo", "public/index.html", "text/html")

	find := func(name string) string {
		for _, tg := range tags {
			if tg.Name == name {
				return tg.Value
			}
		}
		return ""
	}

	if find(TagAppName) != AppName {
		t.Errorf("App-Name = %q", find(TagAppName))
	}
	if find(TagPath) != "public/index.html" {
		t.Errorf("Path = %q", find(TagPath))
	}
	if find(TagRepo) != "demo" {
		t.Errorf("Repo = %q", find(TagRepo))
	}
	if find(TagContentType) != "text/html" {
		t.Errorf("Content-Type = %q", find(TagContentType))
	}
}

func TestContentTagsOmitsEmptyOptional(t *testing.T) {
	tags := ContentTags("", "a.txt", "")
	for _, tg := range tags {
		if tg.Name == TagRepo || tg.Name == TagContentType {
			t.Fatalf("空的可选标签不该出现: %+v", tags)
		}
	}
}

func TestManifestTags(t *testing.T) {
	tags := ManifestTags("demo")
	var hasType bool
	for _, tg := range tags {
		if tg.Name == TagContentType && tg.Value == ManifestContentType {
			hasType = true
		}
	}
	if !hasType {
		t.Fatalf("manifest 缺少专用 Content-Type: %+v", tags)
	}
}
