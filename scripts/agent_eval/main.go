package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"schedule_server/global"
	"schedule_server/inits"
	"schedule_server/internal/agent"
	agenttool "schedule_server/internal/agent/tools"
	"schedule_server/internal/app"
	"schedule_server/internal/repository"
	"schedule_server/internal/service"
	"schedule_server/pkg/dingtalk"
)

// main 执行离线 Agent 评测，并按需串起知识同步和端到端问答。
func main() {
	tenantID := flag.Uint("tenant-id", 0, "租户 ID")
	casesPath := flag.String("cases", "./internal/agent/testdata/eval_cases.json", "评测样本 JSON 路径")
	syncKnowledgeRoot := flag.String("sync-knowledge-root", "", "评测前可选同步的知识目录，例如 ./docs/agent-knowledge")
	syncKnowledgeInclude := flag.String("sync-knowledge-include", "", "逗号分隔的 Markdown 相对路径白名单；为空时同步 root 下全部 .md")
	withAgent := flag.Bool("with-agent", false, "是否启用真实 Agent 问答，统计工具命中与回复关键词")
	corpID := flag.String("corp-id", "", "端到端评测所需的企业 corpID")
	senderID := flag.String("sender-id", "", "端到端评测所需的钉钉用户 ID")
	senderName := flag.String("sender-name", "EvalUser", "端到端评测展示用用户名")
	flag.Parse()

	if *tenantID == 0 {
		fmt.Println("tenant-id 必须大于 0")
		os.Exit(1)
	}

	inits.Init()

	repo := repository.NewRepository(global.DB)
	knowledgeSrv := service.NewAgentKnowledgeService(repo.AgentKnowledgeRepo, global.Log)

	if root := strings.TrimSpace(*syncKnowledgeRoot); root != "" {
		docs, err := loadMarkdownDocuments(root, splitIncludeList(*syncKnowledgeInclude))
		if err != nil {
			fmt.Printf("加载知识文档失败: %v\n", err)
			os.Exit(1)
		}
		result, err := knowledgeSrv.SyncMarkdownDocuments(context.Background(), uint(*tenantID), docs)
		if err != nil {
			fmt.Printf("同步知识失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("知识同步完成：文档 %d，切片 %d，跳过 %d\n", result.DocumentsSynced, result.ChunksCreated, result.Skipped)
	}

	cases, err := agent.LoadEvalCases(*casesPath)
	if err != nil {
		fmt.Printf("加载评测样本失败: %v\n", err)
		os.Exit(1)
	}

	knowledge := &knowledgePortAdapter{srv: knowledgeSrv}
	var observer agent.EvalObserver

	if *withAgent {
		if err := validateWithAgentPrerequisites(global.AppConfig.LLM.APIKey, *corpID, *senderID); err != nil {
			fmt.Printf("with-agent 预检查失败: %v\n", err)
			os.Exit(1)
		}

		dingMgr := service.NewDingTalkClientManager(repo.TenantRepo)
		schedulePeriodSrv := service.NewSchedulePeriodService(
			repo.SchedulePeriodRepo,
			repo.ScheduleSettingRepo,
			&global.AppConfig.Schedule,
		)
		semesterSrv := service.NewSemesterService(repo.SemesterRepo)
		scheduleSrv := service.NewScheduleService(
			repo.CourseRepo,
			repo.UserRepo,
			repo.SemesterRepo,
			repo.ScheduleSettingRepo,
			dingMgr,
			global.Log,
		)
		attendanceSrv := service.NewAttendanceRecordService(
			repo.UserRepo,
			repo.CourseRepo,
			repo.LeaveRepo,
			repo.AttendanceRecordRepo,
			repo.AttendanceManualOverrideRepo,
			repo.ScheduleSettingRepo,
			repo.UserRestDayRepo,
			dingMgr,
			schedulePeriodSrv,
			semesterSrv,
			global.AppConfig.Schedule,
			global.Log,
		)
		restDaySrv := service.NewRestDayService(
			repo.UserRestDayRepo,
			repo.ScheduleSettingRepo,
			repo.UserRepo,
			global.Log,
		)
		leaveSyncSrv := service.NewLeaveSyncService(repo.LeaveRepo, repo.UserRepo, dingMgr, global.Log)

		// observer 将每条评测样本委托给独立 Agent，避免共享限流器和历史会话。
		observer = newCaseScopedObserver(func() (evalChatRunner, *recordingCallLog, error) {
			recorder := newRecordingCallLog()
			runner := app.BuildAgent(
				repo,
				scheduleSrv,
				attendanceSrv,
				semesterSrv,
				schedulePeriodSrv,
				restDaySrv,
				leaveSyncSrv,
				knowledgeSrv,
				recorder,
			)
			return runner, recorder, nil
		}, *corpID, *senderID, *senderName)
	}

	summary, results, err := agent.EvaluateCases(context.Background(), knowledge, uint(*tenantID), cases, observer)
	if err != nil {
		fmt.Printf("执行评测失败: %v\n", err)
		os.Exit(1)
	}

	printSummary(summary, *withAgent)
	printFailures(results)

	if hasFailures(results) {
		os.Exit(1)
	}
}

type evalChatRunner interface {
	Chat(ctx context.Context, msg *dingtalk.ChatMessage) (string, error)
	Stop()
}

type evalRunnerFactory func() (evalChatRunner, *recordingCallLog, error)

// validateWithAgentPrerequisites 校验端到端评测的最小输入与 LLM 凭据前提。
func validateWithAgentPrerequisites(apiKey, corpID, senderID string) error {
	if strings.TrimSpace(corpID) == "" || strings.TrimSpace(senderID) == "" {
		return fmt.Errorf("with-agent 模式必须提供 corp-id 和 sender-id")
	}

	key := strings.TrimSpace(apiKey)
	if key == "" || strings.EqualFold(key, "sk-placeholder") || strings.Contains(strings.ToLower(key), "placeholder") {
		return fmt.Errorf("当前 LLM API Key 看起来是占位值，请先配置可用的 LLM API Key 再运行 with-agent 评测")
	}

	return nil
}

// newCaseScopedObserver 为每条样本创建独立的 Agent runner，避免 session 和限流串样本。
func newCaseScopedObserver(factory evalRunnerFactory, corpID, senderID, senderName string) agent.EvalObserver {
	return func(ctx context.Context, question string) (agent.EvalObservation, error) {
		runner, recorder, err := factory()
		if err != nil {
			return agent.EvalObservation{}, err
		}
		if runner == nil {
			return agent.EvalObservation{}, fmt.Errorf("eval runner is nil")
		}
		defer runner.Stop()

		if recorder != nil {
			recorder.Reset()
		}

		reply, err := runner.Chat(ctx, &dingtalk.ChatMessage{
			CorpID:           corpID,
			SenderID:         senderID,
			SenderNick:       senderName,
			Content:          question,
			ConversationID:   fmt.Sprintf("agent-eval-%d", time.Now().UnixNano()),
			ConversationType: "1",
		})
		if err != nil {
			return agent.EvalObservation{}, err
		}
		if recorder == nil {
			return agent.EvalObservation{Reply: reply}, nil
		}

		callLog, ok := recorder.Wait(2 * time.Second)
		if !ok {
			return agent.EvalObservation{Reply: reply}, nil
		}
		return agent.EvalObservation{
			Reply: reply,
			Tools: append([]string(nil), callLog.ToolsCalled...),
		}, nil
	}
}

type knowledgePortAdapter struct {
	srv *service.AgentKnowledgeService
}

// Search 将 service 层知识命中结果转换为 agent 评测结构。
func (a *knowledgePortAdapter) Search(ctx context.Context, tenantID uint, query string, topK int) ([]agent.KnowledgeHit, error) {
	if a.srv == nil {
		return nil, nil
	}

	hits, err := a.srv.Search(ctx, tenantID, query, topK)
	if err != nil {
		return nil, err
	}

	result := make([]agent.KnowledgeHit, 0, len(hits))
	for _, hit := range hits {
		result = append(result, agent.KnowledgeHit{
			Title:      hit.Title,
			SourcePath: hit.SourcePath,
			DocType:    hit.DocType,
			Audience:   hit.Audience,
			Intent:     hit.Intent,
			ChunkIndex: hit.ChunkIndex,
			Heading:    hit.Heading,
			Body:       hit.Body,
			SourceRef:  hit.SourceRef,
			Score:      hit.Score,
		})
	}
	return result, nil
}

type recordingCallLog struct {
	mu   sync.Mutex
	last agenttool.CallLog
	ch   chan agenttool.CallLog
}

// newRecordingCallLog 创建评测脚本内部使用的日志录制器。
func newRecordingCallLog() *recordingCallLog {
	return &recordingCallLog{
		ch: make(chan agenttool.CallLog, 1),
	}
}

// Write 记录一次异步写入的 Agent 调用日志。
func (r *recordingCallLog) Write(_ context.Context, log agenttool.CallLog) {
	r.mu.Lock()
	r.last = log
	r.mu.Unlock()

	select {
	case r.ch <- log:
	default:
	}
}

// Reset 清空最近一次日志和等待中的通道数据。
func (r *recordingCallLog) Reset() {
	r.mu.Lock()
	r.last = agenttool.CallLog{}
	r.mu.Unlock()

	for {
		select {
		case <-r.ch:
		default:
			return
		}
	}
}

// Wait 在超时前等待一条调用日志，超时后回退到最近一次已写入记录。
func (r *recordingCallLog) Wait(timeout time.Duration) (agenttool.CallLog, bool) {
	select {
	case log := <-r.ch:
		return log, true
	case <-time.After(timeout):
		r.mu.Lock()
		defer r.mu.Unlock()
		if len(r.last.ToolsCalled) == 0 && r.last.Reply == "" && r.last.Question == "" {
			return agenttool.CallLog{}, false
		}
		return r.last, true
	}
}

// loadMarkdownDocuments 按目录和白名单加载待同步的 Markdown 文档。
func loadMarkdownDocuments(root string, include []string) ([]service.MarkdownKnowledgeDocument, error) {
	relPaths := include
	if len(relPaths) == 0 {
		var discovered []string
		// WalkDir 在未提供白名单时递归收集全部 Markdown 文档。
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.EqualFold(filepath.Ext(path), ".md") {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				discovered = append(discovered, rel)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		relPaths = discovered
	}

	docs := make([]service.MarkdownKnowledgeDocument, 0, len(relPaths))
	for _, rel := range relPaths {
		fullPath := filepath.Join(root, rel)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, err
		}
		docs = append(docs, service.MarkdownKnowledgeDocument{
			Title:      deriveTitle(rel, string(data)),
			SourcePath: filepath.ToSlash(filepath.Join(filepath.Base(root), rel)),
			Content:    string(data),
		})
	}
	return docs, nil
}

// splitIncludeList 解析逗号分隔的知识文档相对路径白名单。
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

// deriveTitle 优先从 Markdown 标题推导展示用文档名。
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

// printSummary 输出本次评测的核心指标摘要。
func printSummary(summary agent.EvalSummary, withAgent bool) {
	fmt.Printf("总样本: %d\n", summary.TotalCases)
	fmt.Printf("领域准确率: %.1f%% (%d/%d)\n", summary.DomainAccuracy, summary.DomainPassed, summary.TotalCases)
	fmt.Printf("模式准确率: %.1f%% (%d/%d)\n", summary.ModeAccuracy, summary.ModePassed, summary.TotalCases)
	fmt.Printf("路由准确率: %.1f%% (%d/%d)\n", summary.RouteAccuracy, summary.RoutePassed, summary.TotalCases)
	if summary.RetrievalCases > 0 {
		fmt.Printf("知识命中率: %.1f%% (%d/%d)\n", summary.RetrievalAccuracy, summary.RetrievalPassed, summary.RetrievalCases)
	}
	if withAgent && summary.ToolCases > 0 {
		fmt.Printf("工具命中率: %.1f%% (%d/%d)\n", summary.ToolAccuracy, summary.ToolPassed, summary.ToolCases)
	}
	if withAgent && summary.KeywordCases > 0 {
		fmt.Printf("关键词命中率: %.1f%% (%d/%d)\n", summary.KeywordAccuracy, summary.KeywordPassed, summary.KeywordCases)
	}
	fmt.Printf("平均耗时: %d ms\n", summary.AverageLatencyMs)
}

// printFailures 输出未通过样本的关键信息。
func printFailures(results []agent.EvalCaseResult) {
	var failed []agent.EvalCaseResult
	for _, result := range results {
		if caseFailed(result) {
			failed = append(failed, result)
		}
	}
	if len(failed) == 0 {
		fmt.Println("失败样本: 无")
		return
	}

	fmt.Println("失败样本:")
	for _, result := range failed {
		fmt.Printf("- [%s] %s | domain=%s matched=%t | mode=%s matched=%t | route=%s matched=%t",
			result.Category,
			result.Question,
			result.DomainResult,
			result.DomainMatched,
			result.AnswerMode,
			result.ModeMatched,
			result.Route,
			result.RouteMatched,
		)
		if result.RetrievalChecked {
			fmt.Printf(" | retrieval=%t actual=%v", result.RetrievalMatched, result.RetrievedSources)
		}
		if result.ToolsChecked {
			fmt.Printf(" | tools=%t actual=%v", result.ToolsMatched, result.ActualTools)
		}
		if result.KeywordsChecked {
			fmt.Printf(" | keywords=%t", result.KeywordsMatched)
		}
		if result.Error != "" {
			fmt.Printf(" | error=%s", result.Error)
		}
		fmt.Println()
	}
}

// hasFailures 判断整批评测中是否存在未通过样本。
func hasFailures(results []agent.EvalCaseResult) bool {
	for _, result := range results {
		if caseFailed(result) {
			return true
		}
	}
	return false
}

// caseFailed 判断单条样本是否存在任一维度未通过。
func caseFailed(result agent.EvalCaseResult) bool {
	if !result.DomainMatched || !result.ModeMatched || !result.RouteMatched || result.Error != "" {
		return true
	}
	if result.RetrievalChecked && !result.RetrievalMatched {
		return true
	}
	if result.ToolsChecked && !result.ToolsMatched {
		return true
	}
	if result.KeywordsChecked && !result.KeywordsMatched {
		return true
	}
	return false
}
