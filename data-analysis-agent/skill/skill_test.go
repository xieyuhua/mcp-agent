package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	content := `---
name: my_skill
description: |
  这是一段多行描述。
  第二行描述。
---

# 工作流

步骤一：做某事。
`
	s, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if s.Name != "my_skill" {
		t.Errorf("Name 解析错误: got %q", s.Name)
	}
	wantDesc := "这是一段多行描述。\n第二行描述。"
	if s.Description != wantDesc {
		t.Errorf("Description 解析错误:\n got=%q\nwant=%q", s.Description, wantDesc)
	}
	if s.Body == "" || !strings.HasPrefix(s.Body, "# 工作流") {
		t.Errorf("Body 解析错误: %q", s.Body)
	}
}

func TestParseSingleLineDescription(t *testing.T) {
	content := `---
name: single
description: 单行描述
---
正文内容
`
	s, err := Parse(content)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if s.Description != "单行描述" {
		t.Errorf("Description 应为单行: got %q", s.Description)
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	// 合法技能
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("---\nname: a\ndescription: 技能A\n---\n正文A\n"), 0o644)
	// readme 应被忽略
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("---\nname: readme\ndescription: x\n---\n"), 0o644)
	// 无 frontmatter 应被跳过
	os.WriteFile(filepath.Join(dir, "bad.md"), []byte("没有 frontmatter"), 0o644)

	skills, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir 失败: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("应只加载 1 个技能，实际 %d: %v", len(skills), Names(skills))
	}
	if _, ok := skills["a"]; !ok {
		t.Errorf("技能 a 未加载")
	}
}

func TestLoadDirMissing(t *testing.T) {
	skills, err := LoadDir("./not_exist_dir_xyz")
	if err != nil {
		t.Fatalf("缺失目录不应报错: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("缺失目录应返回空 map")
	}
}
