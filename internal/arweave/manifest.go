package arweave

import "encoding/json"

// ManifestSchemaVersion 是目前网关认的 manifest 版本。
const ManifestSchemaVersion = "0.2.0"

// Manifest 是 Arweave 的 path manifest：把可读路径映射到 data item id。
//
// 它是「入口」：网关收到 <manifest id>/<path> 时，按这里的映射取对应数据。
// 增量更新的关键也在于此：内容没变的文件沿用旧 id，只有 manifest 本身是新的。
type Manifest struct {
	Manifest string                   `json:"manifest"`
	Version  string                   `json:"version"`
	Index    *ManifestIndex           `json:"index,omitempty"`
	Paths    map[string]ManifestEntry `json:"paths"`
}

// ManifestIndex 指定默认入口，可以用路径或直接的 id。
type ManifestIndex struct {
	Path string `json:"path,omitempty"`
	ID   string `json:"id,omitempty"`
}

// ManifestEntry 是某个路径对应的 data item。
type ManifestEntry struct {
	ID string `json:"id"`
}

// NewManifest 用「路径 -> data item id」建一个 manifest。
// indexPath 指默认入口（通常是 index.html），留空则不设。
func NewManifest(paths map[string]string, indexPath string) *Manifest {
	m := &Manifest{
		Manifest: "arweave/paths",
		Version:  ManifestSchemaVersion,
		Paths:    make(map[string]ManifestEntry, len(paths)),
	}
	for p, id := range paths {
		m.Paths[p] = ManifestEntry{ID: id}
	}
	if indexPath != "" {
		m.Index = &ManifestIndex{Path: indexPath}
	}
	return m
}

// Bytes 序列化 manifest。
func (m *Manifest) Bytes() ([]byte, error) {
	return json.Marshal(m)
}

// ParseManifest 反序列化，用于校验或测试。
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
