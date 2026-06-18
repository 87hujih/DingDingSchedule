package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readScript(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join("..", "..", name)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(content)
}

func readOptionalLocalScript(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join("..", "..", name)
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skipf("%s is a local-only deployment helper and is not tracked in CI", name)
	}
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(content)
}

func TestOneClickDeployUsesSourceBundleFlow(t *testing.T) {
	script := readOptionalLocalScript(t, "one-click-deploy.sh")

	requiredFragments := []string{
		"./pack-for-deploy.sh",
		"schedule_server_deploy.tar.gz",
		"tar -xzf 'schedule_server_deploy.tar.gz'",
		"cd 'schedule_server'",
		"./deploy.sh deploy",
		"sed -i 's/\\r\\$//'",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(script, fragment) {
			t.Fatalf("one-click deploy script must restore source bundle deployment flow: missing %q", fragment)
		}
	}

	forbiddenFragments := []string{
		"docker-compose.prod.yml",
		".env.prod.example",
		"IMAGE_REPO",
		"docker login ghcr.io",
	}

	for _, fragment := range forbiddenFragments {
		if strings.Contains(script, fragment) {
			t.Fatalf("one-click deploy script must not depend on new image-based deployment flow: found %q", fragment)
		}
	}
}

func TestPackForDeployBuildsSourceBundle(t *testing.T) {
	script := readScript(t, "pack-for-deploy.sh")

	requiredFragments := []string{
		"PACK_NAME=\"schedule_server_deploy.tar.gz\"",
		"cp -r cmd internal pkg inits global config \"$PROJECT_DIR/\"",
		"cp configs/prod.yaml \"$PROJECT_DIR/configs/\"",
		"cp go.mod go.sum \"$PROJECT_DIR/\"",
		"cp Dockerfile docker-compose.yml \"$PROJECT_DIR/\"",
		"cp deploy-legacy.sh \"$PROJECT_DIR/deploy.sh\"",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(script, fragment) {
			t.Fatalf("pack-for-deploy script must rebuild the legacy source bundle: missing %q", fragment)
		}
	}

	forbiddenFragments := []string{
		"schedule_server_ops_bundle.tar.gz",
		"docker-compose.prod.yml",
		".env.prod.example",
	}

	for _, fragment := range forbiddenFragments {
		if strings.Contains(script, fragment) {
			t.Fatalf("pack-for-deploy script must not use ops-only bundle flow: found %q", fragment)
		}
	}
}

func TestLegacyDeployScriptBuildsAndRunsContainerWithoutCompose(t *testing.T) {
	script := readScript(t, "deploy-legacy.sh")

	requiredFragments := []string{
		"docker build -t ${IMAGE_NAME}:${VERSION} -t ${IMAGE_NAME}:latest .",
		"docker run -d \\",
		"-v $(pwd)/configs:/app/configs:ro \\",
		"-v $(pwd)/logs:/app/logs \\",
		"-v $(pwd)/uploads:/app/uploads \\",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(script, fragment) {
			t.Fatalf("legacy deploy script must restore docker build/run deployment flow: missing %q", fragment)
		}
	}

	if strings.Contains(script, "docker compose") {
		t.Fatalf("legacy deploy script must not depend on docker compose")
	}
}
