package main

// 项目模板：内嵌进二进制（离线可用）。QKM_TEMPLATES=<dir> 指向磁盘目录即可换成自定义/镜像模板。
//
//	qkm init                默认模板 app
//	qkm init -t lib         指定模板
//	qkm init -list          列出模板
//
// 模板里（文件名与内容）的 {{name}} 会替换成项目名；模板自带 cup.json 时以模板为准，否则生成默认的。

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed templates
var embeddedTemplates embed.FS

// templateDesc 是内嵌模板的一句话说明（磁盘模板没登记就用兜底文案）。
var templateDesc = map[string]string{
	"app":  "最小可运行项目（cup.json + src/main.qk）",
	"cli":  "命令行骨架（env/args 形参、参数解析）",
	"lib":  "库骨架（space + struct/impl + qkt 测试 + 依赖声明）",
	"gui":  "GUI 骨架（cleg + style 依赖已声明）",
	"test": "被测代码 + tests/ 测试",
}

// templateFS 返回模板根：QKM_TEMPLATES（磁盘）优先，否则用内嵌的。
func templateFS() (fs.FS, string, error) {
	if dir := strings.TrimSpace(os.Getenv("QKM_TEMPLATES")); dir != "" {
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			return nil, "", fmt.Errorf("QKM_TEMPLATES=%s 不是目录", dir)
		}
		return os.DirFS(dir), dir, nil
	}
	sub, err := fs.Sub(embeddedTemplates, "templates")
	return sub, "内嵌", err
}

// templateNames 列出可用模板名（排序）。
func templateNames() []string {
	root, _, err := templateFS()
	if err != nil {
		return nil
	}
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func templateDescription(name string) string {
	if d, ok := templateDesc[name]; ok {
		return d
	}
	return "自定义模板"
}

// materializeTemplate 把模板展开到 dir：文件名与内容里的 {{name}} 都替换成 projName，
// 返回写出的文件（相对 dir，斜杠分隔，便于展示与测试）。
func materializeTemplate(tpl, dir, projName string) ([]string, error) {
	root, _, err := templateFS()
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(root, tpl)
	if err != nil {
		return nil, fmt.Errorf("未知模板 %q（qkm init -list 查看可用模板）", tpl)
	}
	var written []string
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.ReplaceAll(p, "{{name}}", projName)
		out := filepath.Join(dir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		body := strings.ReplaceAll(string(b), "{{name}}", projName)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
			return err
		}
		if rel != "." {
			written = append(written, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(written)
	return written, err
}
