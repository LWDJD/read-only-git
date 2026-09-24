// Package sitekit 把站点骨架（前端文件）嵌进二进制。
//
// 为什么需要：维护器是一个单独的二进制，但站点要跑起来还差前端文件。
// 没有它们，pack 出来的是一个能 clone、但界面打不开的目录。
// 把骨架嵌进来之后，一个 exe 就能从零把站点立起来。
//
// 骨架的真身在 internal/sitekit/site/，它是唯一来源。
// 早先它在 public/ 下，而 go:embed 够不到包目录之外，只能复制一份，
// 两份代码迟早漂移。现在 public/ 回归纯产物目录。
package sitekit

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed site
var assets embed.FS

// templateRoot 是骨架在 embed 文件系统里的根目录。
const templateRoot = "site"

// Template 描述一套可部署的站点骨架。
type Template struct {
	// ID 是稳定标识，多套模板时用它区分。
	ID string `json:"id"`
	// Name 是给人看的中文名。
	Name string `json:"name"`
	// Description 一句话说明它是什么。
	Description string `json:"description"`
	// Files 是它包含的文件数，仅用于展示。
	Files int `json:"files"`
}

// Templates 列出内置的模板。
//
// 现在只有一套，但接口按「多套」设计：
// 将来要加换肤或别的风格时，不必再动调用方。
func Templates() []Template {
	list, _ := listTemplateFiles()
	return []Template{
		{
			ID:          "default",
			Name:        "默认主题",
			Description: "对齐 GitHub 风格的只读仓库浏览器，跟随系统深浅色",
			Files:       len(list),
		},
	}
}

// Files 返回某个模板包含的所有相对路径（用 / 分隔），已排序。
func Files(templateID string) ([]string, error) {
	if _, err := lookup(templateID); err != nil {
		return nil, err
	}
	return listTemplateFiles()
}

// listTemplateFiles 不校验 id，避开
// Templates → Files → lookup → Templates 这条环。
func listTemplateFiles() ([]string, error) {
	var out []string
	err := fs.WalkDir(assets, templateRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(templateRoot, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(out)
	return out, nil
}

// Materialize 把模板部署到 dir，返回本次写入了哪些文件。
//
// overwrite 为 false 时不碰已经存在的文件：站点里的 index.html 可能被
// 维护者改过，默认不该拿模板盖掉它。要强制刷新就传 true。
func Materialize(templateID, dir string, overwrite bool) ([]string, error) {
	if _, err := lookup(templateID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("目标目录不能为空")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	var written []string
	err := fs.WalkDir(assets, templateRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel, relErr := filepath.Rel(templateRoot, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dir, rel)

		// repository.json 归 pack 管，不归骨架管：它是仓库清单，
		// --force 也不能盖掉，那会把已有仓库的记录清成空清单。
		// 骨架里带一份只是给新站开箱用的，存在就永远不碰。
		if rel == "repository.json" {
			if _, statErr := os.Stat(target); statErr == nil {
				return nil
			}
		}

		if !overwrite {
			if _, statErr := os.Stat(target); statErr == nil {
				// 已经在了，跳过
				return nil
			}
		}

		data, readErr := assets.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		if writeErr := os.WriteFile(target, data, 0o644); writeErr != nil {
			return writeErr
		}
		written = append(written, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return written, err
	}

	sort.Strings(written)
	return written, nil
}

// Missing 返回 dir 里还缺哪些模板文件。
//
// 用来回答「这个目录是不是一个能用的站点」：缺 index.html 就打不开界面，
// 缺 src/ 下的东西界面会白屏，两种都算不完整。
func Missing(templateID, dir string) ([]string, error) {
	all, err := Files(templateID)
	if err != nil {
		return nil, err
	}

	var out []string
	for _, rel := range all {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			out = append(out, rel)
		}
	}
	return out, nil
}

func lookup(id string) (Template, error) {
	for _, t := range Templates() {
		if t.ID == id {
			return t, nil
		}
	}
	return Template{}, fmt.Errorf("没有这个模板: %q", id)
}
