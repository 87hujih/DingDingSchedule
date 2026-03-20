package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readDeployWorkflow(t *testing.T) string {
	t.Helper()

	path := filepath.Join("..", "..", ".github", "workflows", "deploy.yml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read deploy workflow: %v", err)
	}
	return string(content)
}

func TestDeployWorkflowSupportsSSHKeyOrPassword(t *testing.T) {
	workflow := readDeployWorkflow(t)

	if !strings.Contains(workflow, "key: ${{ secrets.SERVER_SSH_KEY }}") {
		t.Fatalf("deploy workflow must keep SSH key support")
	}

	if !strings.Contains(workflow, "password: ${{ secrets.SERVER_PASSWORD }}") {
		t.Fatalf("deploy workflow must support SERVER_PASSWORD fallback")
	}
}

func TestDeployWorkflowValidatesSSHCredentialsBeforeRemoteSteps(t *testing.T) {
	workflow := readDeployWorkflow(t)

	requiredChecks := []string{
		"Missing SERVER_HOST secret",
		"Missing SERVER_USER secret",
		"Either SERVER_SSH_KEY or SERVER_PASSWORD must be configured",
	}

	for _, check := range requiredChecks {
		if !strings.Contains(workflow, check) {
			t.Fatalf("deploy workflow missing preflight check: %s", check)
		}
	}
}

func TestDeployWorkflowIncludesSSHKeyFingerprintDebugStep(t *testing.T) {
	workflow := readDeployWorkflow(t)

	requiredSnippets := []string{
		"- name: Debug SSH key fingerprint",
		"ssh-keygen -y -f",
		"ssh-keygen -lf",
	}

	for _, snippet := range requiredSnippets {
		if !strings.Contains(workflow, snippet) {
			t.Fatalf("deploy workflow missing SSH key fingerprint debug snippet: %s", snippet)
		}
	}

	if strings.Contains(workflow, "cat \"${tmp}\"") || strings.Contains(workflow, "echo \"${SERVER_SSH_KEY}\"") {
		t.Fatalf("deploy workflow must not print raw private key content in debug step")
	}

	if !strings.Contains(workflow, "if: ${{ env.SERVER_SSH_KEY != '' }}") {
		t.Fatalf("deploy workflow must use env.SERVER_SSH_KEY in debug step condition")
	}

	if strings.Contains(workflow, "if: ${{ secrets.SERVER_SSH_KEY != '' }}") {
		t.Fatalf("deploy workflow must not reference secrets directly in step if condition")
	}
}

func TestDeployScriptRetriesImagePullsBeforeFailing(t *testing.T) {
	script := readScript(t, "deploy.sh")

	requiredFragments := []string{
		"IMAGE_PULL_RETRIES=\"${IMAGE_PULL_RETRIES:-3}\"",
		"IMAGE_PULL_RETRY_DELAY_SECONDS=\"${IMAGE_PULL_RETRY_DELAY_SECONDS:-15}\"",
		"retry_compose_pull() {",
		"if compose pull; then",
		"sleep \"${IMAGE_PULL_RETRY_DELAY_SECONDS}\"",
		"请检查服务器到 ghcr.io 的网络连通性",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(script, fragment) {
			t.Fatalf("deploy script missing pull retry fragment: %s", fragment)
		}
	}

	deployStackPattern := regexp.MustCompile(`(?s)deploy_stack\(\) \{.*?\n\}`)
	deployStack := deployStackPattern.FindString(script)
	if deployStack == "" {
		t.Fatalf("deploy script missing deploy_stack function")
	}

	if !strings.Contains(deployStack, "retry_compose_pull") {
		t.Fatalf("deploy_stack must use retry_compose_pull")
	}

	if strings.Contains(deployStack, "compose pull") {
		t.Fatalf("deploy_stack must not call compose pull directly")
	}
}

func TestDeployScriptRemovesConflictingLegacyContainerBeforeComposeUp(t *testing.T) {
	script := readScript(t, "deploy.sh")

	requiredFragments := []string{
		"remove_conflicting_container() {",
		"docker inspect \"${CONTAINER_NAME}\" >/dev/null 2>&1",
		"docker stop \"${CONTAINER_NAME}\" || true",
		"docker rm \"${CONTAINER_NAME}\" || true",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(script, fragment) {
			t.Fatalf("deploy script missing legacy-container cleanup fragment: %s", fragment)
		}
	}

	deployStackPattern := regexp.MustCompile(`(?s)deploy_stack\(\) \{.*?\n\}`)
	deployStack := deployStackPattern.FindString(script)
	if deployStack == "" {
		t.Fatalf("deploy script missing deploy_stack function")
	}

	cleanupIndex := strings.Index(deployStack, "remove_conflicting_container")
	upIndex := strings.Index(deployStack, "compose up -d")
	if cleanupIndex == -1 {
		t.Fatalf("deploy_stack must call remove_conflicting_container")
	}
	if upIndex == -1 {
		t.Fatalf("deploy_stack must call compose up -d")
	}
	if cleanupIndex > upIndex {
		t.Fatalf("deploy_stack must remove conflicting container before compose up")
	}
}

func TestDeployWorkflowChecksHealthViaSSHOnTargetHost(t *testing.T) {
	workflow := readDeployWorkflow(t)

	requiredFragments := []string{
		"- name: Health check",
		"uses: appleboy/ssh-action@v1.2.0",
		"curl -fsS http://localhost:26665/health",
		"for attempt in $(seq 1 12)",
		"sleep 5",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(workflow, fragment) {
			t.Fatalf("deploy workflow missing remote health check fragment: %s", fragment)
		}
	}

	if strings.Contains(workflow, "curl -f http://${{ env.SERVER_HOST }}:26665/health") {
		t.Fatalf("deploy workflow must not rely on runner-side external health check")
	}
}
