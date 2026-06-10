package service

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultKnowledgeManifestName = "manifest.yaml"

type KnowledgeManifest map[string]KnowledgeDocumentMetadata

type knowledgeManifestEntry struct {
	DocType  string `yaml:"doc_type"`
	Audience string `yaml:"audience"`
	Intent   string `yaml:"intent"`
}

func LoadKnowledgeManifest(root string) (KnowledgeManifest, error) {
	manifestPath := filepath.Join(root, DefaultKnowledgeManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return KnowledgeManifest{}, nil
		}
		return nil, err
	}

	raw := make(map[string]knowledgeManifestEntry)
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	manifest := make(KnowledgeManifest, len(raw))
	for sourcePath, entry := range raw {
		manifest[filepath.ToSlash(strings.TrimSpace(sourcePath))] = NormalizeKnowledgeMetadata(KnowledgeDocumentMetadata{
			DocType:  entry.DocType,
			Audience: entry.Audience,
			Intent:   entry.Intent,
		})
	}
	return manifest, nil
}

func (m KnowledgeManifest) MetadataFor(sourcePath string) KnowledgeDocumentMetadata {
	for _, key := range KnowledgeManifestLookupKeys(sourcePath) {
		if meta, ok := m[key]; ok {
			return NormalizeKnowledgeMetadata(meta)
		}
	}
	return NormalizeKnowledgeMetadata(KnowledgeDocumentMetadata{})
}

func KnowledgeManifestLookupKeys(sourcePath string) []string {
	normalized := filepath.ToSlash(strings.TrimSpace(sourcePath))
	if normalized == "" {
		return nil
	}

	keys := []string{normalized}
	if slash := strings.Index(normalized, "/"); slash >= 0 && slash < len(normalized)-1 {
		keys = append(keys, normalized[slash+1:])
	}
	base := filepath.Base(normalized)
	if base != "" {
		keys = append(keys, base)
	}

	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}
