package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestDefaultKnowledgeRootPointsToAgentKnowledgeDir 锁定默认知识目录。
func TestDefaultKnowledgeRootPointsToAgentKnowledgeDir(t *testing.T) {
	want := filepath.ToSlash(filepath.Clean(filepath.Join(".", "docs", "agent-knowledge")))
	if filepath.ToSlash(filepath.Clean(defaultKnowledgeRoot)) != want {
		t.Fatalf("defaultKnowledgeRoot = %q, want %q", defaultKnowledgeRoot, want)
	}
}

// TestCollectMarkdownPathsUsesAllMarkdownFilesWhenIncludeEmpty 验证空 include 时自动扫描全部 Markdown。
func TestCollectMarkdownPathsUsesAllMarkdownFilesWhenIncludeEmpty(t *testing.T) {
	root := t.TempDir()

	mustWriteTestFile(t, filepath.Join(root, "leave-sync-guide.md"))
	mustWriteTestFile(t, filepath.Join(root, "attendance-rules.md"))
	mustWriteTestFile(t, filepath.Join(root, "nested", "system-overview.md"))
	mustWriteTestFile(t, filepath.Join(root, "ignore.txt"))

	got, err := collectMarkdownPaths(root, nil)
	if err != nil {
		t.Fatalf("collectMarkdownPaths returned error: %v", err)
	}

	want := []string{
		"attendance-rules.md",
		"leave-sync-guide.md",
		filepath.ToSlash(filepath.Join("nested", "system-overview.md")),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectMarkdownPaths = %#v, want %#v", got, want)
	}
}

// TestCollectMarkdownPathsPrefersExplicitIncludeList 验证显式白名单优先于目录扫描。
func TestCollectMarkdownPathsPrefersExplicitIncludeList(t *testing.T) {
	root := t.TempDir()

	mustWriteTestFile(t, filepath.Join(root, "attendance-rules.md"))
	mustWriteTestFile(t, filepath.Join(root, "leave-sync-guide.md"))

	got, err := collectMarkdownPaths(root, []string{"leave-sync-guide.md"})
	if err != nil {
		t.Fatalf("collectMarkdownPaths returned error: %v", err)
	}

	want := []string{"leave-sync-guide.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collectMarkdownPaths = %#v, want %#v", got, want)
	}
}

// TestLoadKnowledgeManifestReturnsMetadataBySourcePath 验证 manifest 能按 source path 返回文档元数据。
func TestLoadKnowledgeManifestReturnsMetadataBySourcePath(t *testing.T) {
	root := t.TempDir()

	manifestPath := filepath.Join(root, defaultKnowledgeManifestName)
	if err := os.WriteFile(manifestPath, []byte(`
agent-knowledge/attendance-rules.md:
  doc_type: rule
  audience: shared
  intent: attendance
agent-knowledge/system-overview.md:
  doc_type: overview
  audience: shared
  intent: system
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error: %v", manifestPath, err)
	}

	manifest, err := loadKnowledgeManifest(root)
	if err != nil {
		t.Fatalf("loadKnowledgeManifest() error = %v", err)
	}

	attendance := manifest.MetadataFor("agent-knowledge/attendance-rules.md")
	if attendance.DocType != "rule" || attendance.Audience != "shared" || attendance.Intent != "attendance" {
		t.Fatalf("attendance metadata = %+v, want rule/shared/attendance", attendance)
	}

	overview := manifest.MetadataFor("agent-knowledge/system-overview.md")
	if overview.DocType != "overview" || overview.Audience != "shared" || overview.Intent != "system" {
		t.Fatalf("overview metadata = %+v, want overview/shared/system", overview)
	}
}

// TestLoadKnowledgeManifestFallsBackToDefaultsWhenMissing 验证 manifest 缺失条目时会回退到默认元数据。
func TestLoadKnowledgeManifestFallsBackToDefaultsWhenMissing(t *testing.T) {
	root := t.TempDir()

	manifestPath := filepath.Join(root, defaultKnowledgeManifestName)
	if err := os.WriteFile(manifestPath, []byte(`
agent-knowledge/attendance-rules.md:
  doc_type: rule
  audience: shared
  intent: attendance
`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error: %v", manifestPath, err)
	}

	manifest, err := loadKnowledgeManifest(root)
	if err != nil {
		t.Fatalf("loadKnowledgeManifest() error = %v", err)
	}

	meta := manifest.MetadataFor("agent-knowledge/custom-faq.md")
	if meta.DocType != "unknown" || meta.Audience != "shared" || meta.Intent != "unknown" {
		t.Fatalf("fallback metadata = %+v, want unknown/shared/unknown", meta)
	}
}

// mustWriteTestFile 在临时目录中创建测试文档。
func mustWriteTestFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("# title\ncontent\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error: %v", path, err)
	}
}
