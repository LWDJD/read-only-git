package arweave

// Tag 是挂在 data item 上的一对元数据。
type Tag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Item 是交给钱包签名的载荷：站点路径 + tags（内容走单独的字节通道，不进 JSON）。
type Item struct {
	Path string `json:"path"`
	Tags []Tag  `json:"tags"`
}

// 站点内容统一使用的标签名。
const (
	AppName = "read-only-git"

	TagAppName     = "App-Name"
	TagRepo        = "Repo"
	TagPath        = "Path"
	TagContentType = "Content-Type"

	// ManifestContentType 是 Arweave path manifest 的专用 Content-Type，
	// 网关靠它识别 manifest。
	ManifestContentType = "application/x.arweave-manifest+json"

	// TagRecord 标记「这是一份发布记录」，便于按 tag 检索与辨认。
	TagRecord = "Rog-Record"
)

// ContentTags 为站点里的一个文件生成 tags。
func ContentTags(repo, path, contentType string) []Tag {
	tags := []Tag{
		{Name: TagAppName, Value: AppName},
		{Name: TagPath, Value: path},
	}
	if repo != "" {
		tags = append(tags, Tag{Name: TagRepo, Value: repo})
	}
	if contentType != "" {
		tags = append(tags, Tag{Name: TagContentType, Value: contentType})
	}
	return tags
}

// ManifestTags 为 path manifest 生成 tags。
func ManifestTags(repo string) []Tag {
	tags := []Tag{
		{Name: TagAppName, Value: AppName},
		{Name: TagContentType, Value: ManifestContentType},
	}
	if repo != "" {
		tags = append(tags, Tag{Name: TagRepo, Value: repo})
	}
	return tags
}

// RecordTags 为发布记录生成 tags。
func RecordTags(repo string) []Tag {
	tags := []Tag{
		{Name: TagAppName, Value: AppName},
		{Name: TagContentType, Value: "application/json"},
		{Name: TagRecord, Value: "1"},
	}
	if repo != "" {
		tags = append(tags, Tag{Name: TagRepo, Value: repo})
	}
	return tags
}
