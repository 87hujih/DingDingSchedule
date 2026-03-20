package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readOneClickDeployScript(t *testing.T) string {
	t.Helper()

	path := filepath.Join("..", "..", "one-click-deploy.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read one-click deploy script: %v", err)
	}
	return string(content)
}

func TestOneClickDeployNormalizesUploadedScriptsBeforeExecution(t *testing.T) {
	script := readOneClickDeployScript(t)

	requiredFragments := []string{
		"for asset in deploy.sh docker-compose.prod.yml .env.prod.example; do",
		"sed -i 's/\\r\\$//'",
		"\\\"\\${asset}\\\"",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(script, fragment) {
			t.Fatalf("one-click deploy script must normalize uploaded text assets before execution: missing %q", fragment)
		}
	}
}
