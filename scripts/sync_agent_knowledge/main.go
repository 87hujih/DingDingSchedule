package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"schedule_server/global"
	"schedule_server/inits"
	"schedule_server/internal/repository"
	"schedule_server/internal/service"
)

const defaultKnowledgeRoot = "./docs/agent-knowledge"

// 使用方法:
// go run ./scripts/sync_agent_knowledge -tenant-id 1
// main 读取知识目录中的 Markdown 文档并同步到租户级知识库。
func main() {
	tenantID := flag.Uint("tenant-id", 0, "租户 ID")
	root := flag.String("root", defaultKnowledgeRoot, "知识文档根目录")
	include := flag.String("include", "", "逗号分隔的 Markdown 文件名白名单；为空时同步根目录下全部 Markdown")
	flag.Parse()

	if *tenantID == 0 {
		fmt.Println("tenant-id 必须大于 0")
		os.Exit(1)
	}

	inits.Init()

	repo := repository.NewRepository(global.DB)
	svc := service.NewAgentKnowledgeService(repo.AgentKnowledgeRepo, global.Log)

	paths, err := collectMarkdownPaths(*root, splitIncludeList(*include))
	if err != nil {
		fmt.Printf("收集知识文档失败: %v\n", err)
		os.Exit(1)
	}

	docs := make([]service.MarkdownKnowledgeDocument, 0, len(paths))
	for _, rel := range paths {
		fullPath := filepath.Join(*root, rel)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			fmt.Printf("读取文件失败 %s: %v\n", fullPath, err)
			os.Exit(1)
		}
		docs = append(docs, service.MarkdownKnowledgeDocument{
			Title:      deriveTitle(rel, string(data)),
			SourcePath: filepath.ToSlash(filepath.Join(filepath.Base(*root), rel)),
			Content:    string(data),
		})
	}

	result, err := svc.SyncMarkdownDocuments(context.Background(), uint(*tenantID), docs)
	if err != nil {
		fmt.Printf("同步知识失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("知识同步完成：文档 %d，切片 %d，跳过 %d\n", result.DocumentsSynced, result.ChunksCreated, result.Skipped)
}

// splitIncludeList 解析逗号分隔的相对路径白名单。
func splitIncludeList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

// collectMarkdownPaths 返回需要同步的相对 Markdown 路径列表。
func collectMarkdownPaths(root string, include []string) ([]string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("root 不能为空")
	}
	if len(include) > 0 {
		return include, nil
	}

	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, fmt.Errorf("目录 %s 下没有可同步的 Markdown 文档", root)
	}
	return paths, nil
}

// deriveTitle 优先从 Markdown 一级标题推导文档标题。
func deriveTitle(relPath, content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		title := strings.TrimSpace(strings.TrimLeft(line, "#"))
		if title != "" {
			return title
		}
	}
	base := filepath.Base(relPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
