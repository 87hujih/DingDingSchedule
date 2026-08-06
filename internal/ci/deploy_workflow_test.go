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

	requiredFragments := []string{
		"SERVER_SSH_KEY: ${{ secrets.SERVER_SSH_KEY }}",
		"SERVER_PASSWORD: ${{ secrets.SERVER_PASSWORD }}",
		"if [ -z \"${SERVER_SSH_KEY}\" ] && [ -z \"${SERVER_PASSWORD}\" ]; then",
		"if [ -n \"${SERVER_SSH_KEY}\" ]; then",
		"Using SSH key authentication",
		"Using password authentication fallback",
		"printf '%s\\n' \"${SERVER_SSH_KEY}\" > \"${KEY_PATH}\"",
		"export SSHPASS=\"${SERVER_PASSWORD}\"",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(workflow, fragment) {
			t.Fatalf("deploy workflow missing SSH auth fragment: %s", fragment)
		}
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

func TestProductionComposeUsesPullableDingTalkWebhookImage(t *testing.T) {
	composePath := filepath.Join("..", "..", "docker-compose.prod.yml")
	content, err := os.ReadFile(composePath)
	if err != nil {
		t.Fatalf("read docker-compose.prod.yml: %v", err)
	}
	compose := string(content)

	requiredFragments := []string{
		"image: timonwong/prometheus-webhook-dingtalk:v2.1.0",
		"- '--config.file=/etc/webhook-dingtalk/config.yml'",
		"- ./deploy/webhook-dingtalk.yml:/etc/webhook-dingtalk/config.yml:ro",
		"- \"8065:8065\"",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(compose, fragment) {
			t.Fatalf("production compose missing pullable webhook fragment: %s", fragment)
		}
	}

	if strings.Contains(compose, "image: dingtalk-webhook:latest") {
		t.Fatalf("production compose must not depend on an unbuilt local dingtalk-webhook image")
	}
}

func TestDeployScriptStartsAPIBeforeOptionalMonitoringStack(t *testing.T) {
	script := readScript(t, "deploy.sh")

	requiredFragments := []string{
		"start_api_service() {",
		"compose up -d schedule-server",
		"start_monitoring_stack() {",
		"compose pull prometheus grafana alertmanager webhook-dingtalk --ignore-pull-failures",
		"compose up -d prometheus grafana alertmanager webhook-dingtalk",
		"监控栈启动失败，不影响 API 服务",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(script, fragment) {
			t.Fatalf("deploy script missing API-first deployment fragment: %s", fragment)
		}
	}

	deployStackPattern := regexp.MustCompile(`(?s)deploy_stack\(\) \{.*?\n\}`)
	deployStack := deployStackPattern.FindString(script)
	if deployStack == "" {
		t.Fatalf("deploy script missing deploy_stack function")
	}

	apiIndex := strings.Index(deployStack, "start_api_service")
	monitoringIndex := strings.Index(deployStack, "start_monitoring_stack")
	if apiIndex == -1 {
		t.Fatalf("deploy_stack must start API service")
	}
	if monitoringIndex == -1 {
		t.Fatalf("deploy_stack must attempt monitoring stack startup")
	}
	if apiIndex > monitoringIndex {
		t.Fatalf("deploy_stack must start API before optional monitoring stack")
	}
}

func TestDeployScriptRemovesConflictingLegacyContainerBeforeAPIStart(t *testing.T) {
	script := readScript(t, "deploy.sh")

	requiredFragments := []string{
		"remove_conflicting_container() {",
		"docker container inspect \"${CONTAINER_NAME}\" >/dev/null 2>&1",
		"docker stop \"${CONTAINER_NAME}\" || true",
		"docker rm \"${CONTAINER_NAME}\" || true",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(script, fragment) {
			t.Fatalf("deploy script missing legacy-container cleanup fragment: %s", fragment)
		}
	}

	startAPIPattern := regexp.MustCompile(`(?s)start_api_service\(\) \{.*?\n\}`)
	startAPI := startAPIPattern.FindString(script)
	if startAPI == "" {
		t.Fatalf("deploy script missing start_api_service function")
	}

	cleanupIndex := strings.Index(startAPI, "remove_conflicting_container")
	upIndex := strings.Index(startAPI, "compose up -d schedule-server")
	if cleanupIndex == -1 {
		t.Fatalf("start_api_service must call remove_conflicting_container")
	}
	if upIndex == -1 {
		t.Fatalf("start_api_service must call compose up -d schedule-server")
	}
	if cleanupIndex > upIndex {
		t.Fatalf("start_api_service must remove conflicting container before API compose up")
	}
}

func TestDeployWorkflowChecksHealthViaSSHOnTargetHost(t *testing.T) {
	workflow := readDeployWorkflow(t)

	requiredFragments := []string{
		"- name: Deploy through SSH master connection",
		"docker save \"${IMAGE_REPO}:${IMAGE_TAG}\" | gzip -1 |",
		"\"${SSH_CMD[@]}\" \"${SSH_AUTH_OPTS[@]}\" \"${SSH_OPTS[@]}\" \"${REMOTE}\" \"docker load\"",
		"curl -fsS http://localhost:26665/health",
		"sleep 5",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(workflow, fragment) {
			t.Fatalf("deploy workflow missing remote health check fragment: %s", fragment)
		}
	}

	healthRetryPattern := regexp.MustCompile(`for attempt in \\\$\(seq 1 12\)`)
	if !healthRetryPattern.MatchString(workflow) {
		t.Fatalf("deploy workflow missing remote health check retry loop")
	}

	if strings.Contains(workflow, "curl -f http://${{ env.SERVER_HOST }}:26665/health") {
		t.Fatalf("deploy workflow must not rely on runner-side external health check")
	}
}

func TestDeployWorkflowValidatesProductionAgentReleaseBeforeReplacingContainer(t *testing.T) {
	workflow := readDeployWorkflow(t)

	requiredFragments := []string{
		"Running Agent production config and database preflight",
		"--network container:schedule-server",
		"-e CONFIG_ENV=prod",
		"-e CONFIG_PATH=/app/configs",
		"agent-release-check",
	}
	for _, fragment := range requiredFragments {
		if !strings.Contains(workflow, fragment) {
			t.Fatalf("deploy workflow missing Agent config preflight fragment: %s", fragment)
		}
	}

	preflightIndex := strings.Index(workflow, "agent-release-check")
	deployIndex := strings.Index(workflow, "SKIP_IMAGE_PULL=1 ./deploy.sh deploy")
	if preflightIndex == -1 || deployIndex == -1 || preflightIndex > deployIndex {
		t.Fatal("Agent config preflight must run before replacing the production container")
	}
}
