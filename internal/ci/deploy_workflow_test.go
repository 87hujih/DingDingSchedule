package ci

import (
	"os"
	"path/filepath"
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
