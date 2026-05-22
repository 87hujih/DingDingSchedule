package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	agenttools "schedule_server/internal/agent/tools"
	"schedule_server/pkg/dingtalk"

	"go.uber.org/zap"
)

type testKnowledgePort struct {
	mu       sync.Mutex
	calls    int
	tenantID uint
	query    string
	topK     int
	hits     []agenttools.KnowledgeHit
	err      error
}

// Search 返回测试预置的知识命中结果，并记录调用参数。
func (p *testKnowledgePort) Search(_ context.Context, tenantID uint, query string, topK int) ([]agenttools.KnowledgeHit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.tenantID = tenantID
	p.query = query
	p.topK = topK
	if p.err != nil {
		return nil, p.err
	}
	return p.hits, nil
}

type testCallLogPort struct {
	ch chan agenttools.CallLog
}

// newTestCallLogPort 创建测试用调用日志接收器。
func newTestCallLogPort() *testCallLogPort {
	return &testCallLogPort{ch: make(chan agenttools.CallLog, 1)}
}

// Write 接收一次 Agent 调用日志。
func (p *testCallLogPort) Write(_ context.Context, log agenttools.CallLog) {
	select {
	case p.ch <- log:
	default:
	}
}

// Wait 在超时前等待一条调用日志写入。
func (p *testCallLogPort) Wait(timeout time.Duration) (agenttools.CallLog, bool) {
	select {
	case log := <-p.ch:
		return log, true
	case <-time.After(timeout):
		return agenttools.CallLog{}, false
	}
}

type testGroupSubPort struct {
	mu                sync.Mutex
	subscribeCalls    int
	unsubscribeCalls  int
	lastConversation  string
	lastGroupName     string
	lastTenantID      uint
	lastEnabledByUID  uint
	lastSubscribedIDs []int64
}

func (p *testGroupSubPort) Subscribe(_ context.Context, tenantID uint, conversationID, groupName string, enabledByUID uint, deptIDs []int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subscribeCalls++
	p.lastTenantID = tenantID
	p.lastConversation = conversationID
	p.lastGroupName = groupName
	p.lastEnabledByUID = enabledByUID
	p.lastSubscribedIDs = append([]int64(nil), deptIDs...)
	return nil
}

func (p *testGroupSubPort) Unsubscribe(_ context.Context, _ uint, conversationID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unsubscribeCalls++
	p.lastConversation = conversationID
	return nil
}

func (p *testGroupSubPort) GetSubscription(context.Context, uint, string) (*agenttools.GroupSubInfo, error) {
	return &agenttools.GroupSubInfo{}, nil
}

type capturedChatRequest struct {
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Tools []json.RawMessage `json:"tools"`
}

type testClarifyDeptPort struct{}

func (testClarifyDeptPort) ListDepts(context.Context) ([]agenttools.DeptItem, error) {
	return []agenttools.DeptItem{
		{DeptID: 101, Name: "信工24级"},
		{DeptID: 102, Name: "信工23级"},
		{DeptID: 103, Name: "教务处"},
		{DeptID: 104, Name: "学工处"},
	}, nil
}

type testFamilyDeptPort struct{}

func (testFamilyDeptPort) ListDepts(context.Context) ([]agenttools.DeptItem, error) {
	return []agenttools.DeptItem{
		{DeptID: 201, Name: "家族7期"},
		{DeptID: 202, Name: "乐知全栈一期"},
	}, nil
}

type testClarifyGroupSubPort struct {
	info *agenttools.GroupSubInfo
}

func (p testClarifyGroupSubPort) Subscribe(context.Context, uint, string, string, uint, []int64) error {
	return nil
}

func (p testClarifyGroupSubPort) Unsubscribe(context.Context, uint, string) error {
	return nil
}

func (p testClarifyGroupSubPort) GetSubscription(context.Context, uint, string) (*agenttools.GroupSubInfo, error) {
	return p.info, nil
}

type testTaskAttendancePort struct {
	mu          sync.Mutex
	detailCalls int
	detailResp  *agenttools.AttendanceResult
	signCalls   int
	lastDate    string
	lastSection int
	lastUserIDs []uint
}

func (p *testTaskAttendancePort) GetAttendanceDetail(context.Context, agenttools.AttendanceQuery) (*agenttools.AttendanceResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.detailCalls++
	if p.detailResp == nil {
		return &agenttools.AttendanceResult{}, nil
	}
	resp := *p.detailResp
	return &resp, nil
}

func (p *testTaskAttendancePort) GetAttendanceText(context.Context, agenttools.AttendanceQuery) (string, error) {
	return "", nil
}

func (p *testTaskAttendancePort) GetWeeklyAbsenceRanking(context.Context) ([]agenttools.RankItem, error) {
	return nil, nil
}

func (p *testTaskAttendancePort) GetWeeklyAttendanceRateRanking(context.Context) ([]agenttools.RankItem, error) {
	return nil, nil
}

func (p *testTaskAttendancePort) FindRecordByDateSection(context.Context, string, int) (uint, error) {
	return 0, nil
}

func (p *testTaskAttendancePort) SignForUsers(context.Context, uint, []uint) error {
	return nil
}

func (p *testTaskAttendancePort) SignForUsersBySlot(_ context.Context, date string, section int, userIDs []uint) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.signCalls++
	p.lastDate = date
	p.lastSection = section
	p.lastUserIDs = append([]uint(nil), userIDs...)
	return nil
}

type testTaskUserPort struct{}

func (testTaskUserPort) FindByDingUserID(context.Context, string) (*agenttools.UserInfo, error) {
	return &agenttools.UserInfo{
		ID:         7,
		Name:       "Alice",
		DingUserID: "ding-user",
		Role:       1,
		TenantID:   42,
	}, nil
}

func (testTaskUserPort) SearchByName(_ context.Context, name string) ([]agenttools.UserInfo, error) {
	if strings.TrimSpace(name) == "张三" {
		return []agenttools.UserInfo{
			{
				ID:         99,
				Name:       "张三",
				DingUserID: "ding-zhangsan",
				Role:       0,
				TenantID:   42,
			},
		}, nil
	}
	return nil, nil
}

type testAmbiguousManualSignUserPort struct{}

func (testAmbiguousManualSignUserPort) FindByDingUserID(context.Context, string) (*agenttools.UserInfo, error) {
	return &agenttools.UserInfo{
		ID:         7,
		Name:       "Alice",
		DingUserID: "ding-user",
		Role:       1,
		TenantID:   42,
	}, nil
}

func (testAmbiguousManualSignUserPort) SearchByName(_ context.Context, name string) ([]agenttools.UserInfo, error) {
	if strings.TrimSpace(name) == "张" {
		return []agenttools.UserInfo{
			{ID: 99, Name: "张三", DingUserID: "ding-zhangsan", Role: 0, TenantID: 42},
			{ID: 100, Name: "张四", DingUserID: "ding-zhangsi", Role: 0, TenantID: 42},
		}, nil
	}
	return nil, nil
}

type testQueryUserSchedulePort struct {
	listUserCalls    int
	lastViewerID     uint
	lastViewerRole   int
	lastTargetUserID uint
	lastWeek         int
	courses          []agenttools.CourseItem
}

func (p *testQueryUserSchedulePort) ListMyScheduleByWeek(context.Context, uint, int) ([]agenttools.CourseItem, error) {
	return nil, nil
}

func (p *testQueryUserSchedulePort) ListUserScheduleByWeek(_ context.Context, viewerID uint, viewerRole int, targetUserID uint, week int) ([]agenttools.CourseItem, error) {
	p.listUserCalls++
	p.lastViewerID = viewerID
	p.lastViewerRole = viewerRole
	p.lastTargetUserID = targetUserID
	p.lastWeek = week
	return p.courses, nil
}

func (p *testQueryUserSchedulePort) GetFreeUsersBySlot(context.Context, int, int, int, int64) ([]agenttools.FreeSlotResult, error) {
	return nil, nil
}

type testScheduleQueryUserPort struct {
	searchResults []agenttools.UserInfo
}

func (p testScheduleQueryUserPort) FindByDingUserID(context.Context, string) (*agenttools.UserInfo, error) {
	return &agenttools.UserInfo{
		ID:         7,
		Name:       "Alice",
		DingUserID: "ding-user",
		Role:       0,
		TenantID:   42,
	}, nil
}

func (p testScheduleQueryUserPort) SearchByName(context.Context, string) ([]agenttools.UserInfo, error) {
	return p.searchResults, nil
}

// TestAgentChatUsesKnowledgeOnlyForLeaveSyncFailureQuestion 验证真实口语化规则问法会走 knowledge-only。
func TestAgentChatUsesKnowledgeOnlyForLeaveSyncFailureQuestion(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:      "请假同步说明",
				SourcePath: "agent-knowledge/leave-sync-guide.md",
				DocType:    "rule",
				Audience:   "shared",
				Intent:     "leave",
				Heading:    "同步失败处理",
				Body:       "同步失败不会直接覆盖已经生成的考勤快照；排障后应重试同步，再由管理员复核结果。",
				SourceRef:  "请假同步说明#3",
				Score:      18,
			},
		},
	}

	var requests []capturedChatRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"同步失败不会直接覆盖已生成的考勤快照，排障后应重试同步并复核。"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:     server.URL,
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "如果请假信息没能同步到位，会出现什么情况",
		ConversationID:   "conv-knowledge",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply == "" {
		t.Fatalf("Chat() reply should not be empty")
	}

	knowledge.mu.Lock()
	if knowledge.calls != 1 {
		t.Fatalf("knowledge search calls = %d, want 1", knowledge.calls)
	}
	knowledge.mu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if len(requests[0].Tools) != 0 {
		t.Fatalf("knowledge-only request tools len = %d, want 0", len(requests[0].Tools))
	}
	if !requestContains(requests[0], "请假同步说明#3") {
		t.Fatalf("knowledge-only request missing source ref, messages = %+v", requests[0].Messages)
	}
	if !requestContains(requests[0], "同步失败不会直接覆盖已经生成的考勤快照") {
		t.Fatalf("knowledge-only request missing knowledge body, messages = %+v", requests[0].Messages)
	}
}

// TestAgentChatReturnsRealtimeResultOnlyForRealtimePlusRuleQuestion 验证实时+规则表达首轮只返回实时结果。
func TestAgentChatReturnsRealtimeResultOnlyForRealtimePlusRuleQuestion(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:      "考勤规则",
				SourcePath: "agent-knowledge/attendance-rules.md",
				DocType:    "rule",
				Audience:   "shared",
				Intent:     "attendance",
				Heading:    "迟到判定",
				Body:       "上课开始后超过 10 分钟打卡视为迟到。",
				SourceRef:  "考勤规则#1",
				Score:      18,
			},
		},
	}

	routerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"kind\":\"tool_query\",\"confidence\":0.98,\"reason_code\":\"live_attendance\"}"},"finish_reason":"stop"}]}`))
	}))
	defer routerServer.Close()

	var requests []capturedChatRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		requests = append(requests, req)
		current := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch current {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_current_time","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"今天第一节未到人员已查询。"},"finish_reason":"stop"}]}`))
		default:
			http.Error(w, `{"error":{"message":"unexpected extra request"}}`, http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       server.URL,
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		Knowledge:        knowledge,
		User:             testUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "今天第一节谁未到，并说明迟到规则",
		ConversationID:   "conv-mixed",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply != "今天第一节未到人员已查询。" {
		t.Fatalf("Chat() reply = %q, want only realtime result", reply)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if len(requests[0].Tools) == 0 {
		t.Fatalf("tool_query request should keep tools")
	}
	if requestContains(requests[0], "先回答实时查询结果") {
		t.Fatalf("tool_query request should not inject mixed answer-order instruction, messages = %+v", requests[0].Messages)
	}
	if requestContains(requests[0], "考勤规则#1") {
		t.Fatalf("tool_query request should not inject rule source ref, messages = %+v", requests[0].Messages)
	}
	if requestContains(requests[0], "上课开始后超过 10 分钟打卡视为迟到") {
		t.Fatalf("tool_query request should not inject rule body, messages = %+v", requests[0].Messages)
	}
}

// TestAgentChatRejectsOutOfDomainBeforeRetrieval 验证站外问题通过语义路由器被拒绝。
func TestAgentChatRejectsOutOfDomainBeforeRetrieval(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{}

	routerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"kind\":\"off_topic_reject\",\"confidence\":0.95,\"reason_code\":\"out_of_domain\"}"}}]}`)
	}))
	defer routerServer.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       "http://127.0.0.1:0",
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL + "/v1/chat/completions",
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "test-router",
		RouteMode:        string(RouteModeLive),
		Knowledge:        knowledge,
		User:             testUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "今天上海天气怎么样",
		ConversationID:   "conv-reject",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply != outOfDomainReply {
		t.Fatalf("Chat() reply = %q, want %q", reply, outOfDomainReply)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge search calls = %d, want 0", knowledge.calls)
	}
}

// TestAgentChatKeepsToolFirstForLiveQueryWithoutRuleSignal 验证纯实时查询即使命中知识也不应被抬成 mixed。
func TestAgentChatKeepsToolFirstForLiveQueryWithoutRuleSignal(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:      "系统总览",
				SourcePath: "agent-knowledge/system-overview.md",
				DocType:    "overview",
				Audience:   "shared",
				Intent:     "system",
				Heading:    "课表与考勤链路",
				Body:       "系统支持课表查询和考勤结果查看。",
				SourceRef:  "系统总览#17",
				Score:      18,
			},
		},
	}

	routerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"kind\":\"tool_query\",\"confidence\":0.97,\"reason_code\":\"live_attendance\"}"},"finish_reason":"stop"}]}`))
	}))
	defer routerServer.Close()

	var requests []capturedChatRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		requests = append(requests, req)
		current := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch current {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_current_time","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"今天第一节未到人员已查询。"},"finish_reason":"stop"}]}`))
		default:
			http.Error(w, `{"error":{"message":"unexpected extra request"}}`, http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       server.URL,
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		Knowledge:        knowledge,
		User:             testUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "今天第一节谁未到？",
		ConversationID:   "conv-tool-first",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply == "" {
		t.Fatalf("Chat() reply should not be empty")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if len(requests[0].Tools) == 0 {
		t.Fatalf("tool-first request should keep tools")
	}
	if requestContains(requests[0], "系统总览#17") {
		t.Fatalf("tool-first request should not inject knowledge summary, messages = %+v", requests[0].Messages)
	}
	names := requestToolNames(requests[0])
	if !toolPoolContains(names, "query_attendance_status") {
		t.Fatalf("tool-first request missing attendance tool: %v", names)
	}
	if !toolPoolContains(names, "subscribe_attendance_push") {
		t.Fatalf("tool-first request missing admin tool (expected full set for LLM selection): %v", names)
	}
}

// TestAgentChatUsesKnowledgeOnlyPathForRuleQuestions 验证纯规则问题会关闭工具并注入知识上下文。
func TestAgentChatUsesKnowledgeOnlyPathForRuleQuestions(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:     "考勤规则",
				Heading:   "迟到判定",
				Body:      "上课开始后超过 10 分钟打卡视为迟到。",
				SourceRef: "考勤规则#1",
				Score:     18,
			},
		},
	}

	var requests []capturedChatRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"根据规则，超过 10 分钟打卡算迟到。"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:     server.URL,
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "考勤迟到怎么判定？",
		ConversationID:   "conv-1",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply != "根据规则，超过 10 分钟打卡算迟到。" {
		t.Fatalf("Chat() reply = %q", reply)
	}

	knowledge.mu.Lock()
	if knowledge.calls != 1 {
		t.Fatalf("knowledge search calls = %d, want 1", knowledge.calls)
	}
	if knowledge.tenantID != 42 {
		t.Fatalf("knowledge tenantID = %d, want 42", knowledge.tenantID)
	}
	if knowledge.topK <= 0 {
		t.Fatalf("knowledge topK = %d, want positive", knowledge.topK)
	}
	knowledge.mu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if len(requests[0].Tools) != 0 {
		t.Fatalf("rag request tools len = %d, want 0", len(requests[0].Tools))
	}
	if !requestContains(requests[0], "考勤规则#1") {
		t.Fatalf("rag request missing source ref, messages = %+v", requests[0].Messages)
	}
	if !requestContains(requests[0], "上课开始后超过 10 分钟打卡视为迟到") {
		t.Fatalf("rag request missing knowledge body, messages = %+v", requests[0].Messages)
	}
}

// TestAgentChatWritesKnowledgeMetricsToCallLog 验证规则问答会把检索指标写入调用日志。
func TestAgentChatWritesKnowledgeMetricsToCallLog(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:     "考勤规则",
				Heading:   "迟到判定",
				Body:      "上课开始后超过 10 分钟打卡视为迟到。",
				SourceRef: "考勤规则#1",
				Score:     18,
			},
		},
	}
	callLog := newTestCallLogPort()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"根据规则，超过 10 分钟打卡算迟到。"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:     server.URL,
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		Knowledge:      knowledge,
		CallLog:        callLog,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "考勤迟到怎么判定？",
		ConversationID:   "conv-1",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	log, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected call log to be written")
	}
	if log.QueryType != "rag" {
		t.Fatalf("QueryType = %q, want rag", log.QueryType)
	}
	if log.DomainHint != "" {
		t.Fatalf("DomainHint = %q, want empty", log.DomainHint)
	}
	if log.PlanKind != "rag" {
		t.Fatalf("PlanKind = %q, want rag", log.PlanKind)
	}
	if log.KnowledgeStrength != "strong" {
		t.Fatalf("KnowledgeStrength = %q, want strong", log.KnowledgeStrength)
	}
	if log.ToolCallCount != 0 {
		t.Fatalf("ToolCallCount = %d, want 0", log.ToolCallCount)
	}
	if log.RetrievalHitCount != 1 {
		t.Fatalf("RetrievalHitCount = %d, want 1", log.RetrievalHitCount)
	}
	if len(log.SourceRefs) != 1 || log.SourceRefs[0] != "考勤规则#1" {
		t.Fatalf("SourceRefs = %v, want [考勤规则#1]", log.SourceRefs)
	}
	if log.LLMDurationMs <= 0 {
		t.Fatalf("LLMDurationMs = %d, want positive", log.LLMDurationMs)
	}
	if log.RetrievalDurationMs <= 0 {
		t.Fatalf("RetrievalDurationMs = %d, want positive", log.RetrievalDurationMs)
	}
}

func TestAgentChatQueriesOtherUsersScheduleViaTool(t *testing.T) {
	t.Parallel()

	routerServer := newRouteDecisionServer(t, RouteDecision{
		Kind:       RouteToolQuery,
		Confidence: 0.98,
		ReasonCode: "schedule_query",
	})
	defer routerServer.Close()

	schedule := &testQueryUserSchedulePort{
		courses: []agenttools.CourseItem{{CourseName: "高等数学", DayOfWeek: 1, Section: 1}},
	}

	var requests []capturedChatRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		requests = append(requests, req)
		current := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch current {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"query_user_schedule","arguments":"{\"user_name\":\"张三\",\"week\":6}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"张三第6周有1门课。"},"finish_reason":"stop"}]}`))
		default:
			http.Error(w, `{"error":{"message":"unexpected extra request"}}`, http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       server.URL,
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		Schedule:         schedule,
		User: testScheduleQueryUserPort{
			searchResults: []agenttools.UserInfo{{ID: 9, Name: "张三", DingUserID: "ding-zhangsan", TenantID: 42}},
		},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "帮我看张三第6周课表",
		ConversationID:   "conv-user-schedule",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "张三第6周有1门课") {
		t.Fatalf("Chat() reply = %q, want schedule answer", reply)
	}
	if schedule.listUserCalls != 1 {
		t.Fatalf("ListUserScheduleByWeek() call count = %d, want 1", schedule.listUserCalls)
	}
	if schedule.lastViewerID != 7 || schedule.lastTargetUserID != 9 || schedule.lastWeek != 6 {
		t.Fatalf("ListUserScheduleByWeek() args = viewer:%d target:%d week:%d", schedule.lastViewerID, schedule.lastTargetUserID, schedule.lastWeek)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	names := requestToolNames(requests[0])
	if !toolPoolContains(names, "query_user_schedule") {
		t.Fatalf("tool-first request missing query_user_schedule: %v", names)
	}
}

func TestAgentChatClarifiesAmbiguousUserScheduleQuery(t *testing.T) {
	t.Parallel()

	routerServer := newRouteDecisionServer(t, RouteDecision{
		Kind:       RouteToolQuery,
		Confidence: 0.98,
		ReasonCode: "schedule_query",
	})
	defer routerServer.Close()

	schedule := &testQueryUserSchedulePort{}
	var requests []capturedChatRequest
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		requests = append(requests, req)
		current := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch current {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"query_user_schedule","arguments":"{\"user_name\":\"张三\"}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"找到 2 个同名用户：张三、张三(教务处)。请直接回复更精确的姓名。"},"finish_reason":"stop"}]}`))
		default:
			http.Error(w, `{"error":{"message":"unexpected extra request"}}`, http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       server.URL,
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		Schedule:         schedule,
		User: testScheduleQueryUserPort{
			searchResults: []agenttools.UserInfo{
				{ID: 9, Name: "张三", DingUserID: "ding-zhangsan", TenantID: 42},
				{ID: 10, Name: "张三(教务处)", DingUserID: "ding-zhangsan-2", TenantID: 42},
			},
		},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "帮我看张三这周课表",
		ConversationID:   "conv-user-schedule-ambiguous",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "张三(教务处)") {
		t.Fatalf("Chat() reply = %q, want candidate clarification", reply)
	}
	if schedule.listUserCalls != 0 {
		t.Fatalf("ListUserScheduleByWeek() call count = %d, want 0", schedule.listUserCalls)
	}
}

// TestAgentChatWritesConversationTaskMetricsToCallLog 验证多轮任务状态会写入调用日志。
func TestAgentChatWritesConversationTaskMetricsToCallLog(t *testing.T) {
	t.Parallel()

	callLog := newTestCallLogPort()
	groupSub := &testGroupSubPort{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"unexpected llm request"}}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:     server.URL,
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		CallLog:        callLog,
		GroupSub:       groupSub,
		Dept:           testClarifyDeptPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "开启考勤订阅",
		ConversationID:    "conv-call-log",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}
	firstLog, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected first call log to be written")
	}
	if firstLog.ConversationEvent != "new_request" {
		t.Fatalf("first ConversationEvent = %q, want new_request", firstLog.ConversationEvent)
	}
	if firstLog.DomainHint != "" {
		t.Fatalf("first DomainHint = %q, want empty", firstLog.DomainHint)
	}
	if firstLog.PlanKind != "clarify" {
		t.Fatalf("first PlanKind = %q, want clarify", firstLog.PlanKind)
	}
	if firstLog.KnowledgeStrength != "none" {
		t.Fatalf("first KnowledgeStrength = %q, want none", firstLog.KnowledgeStrength)
	}
	if firstLog.PlannerReason != "missing_slots" {
		t.Fatalf("first PlannerReason = %q, want missing_slots", firstLog.PlannerReason)
	}
	if firstLog.ActiveTaskType != "subscribe_attendance_push" {
		t.Fatalf("first ActiveTaskType = %q, want subscribe_attendance_push", firstLog.ActiveTaskType)
	}
	if firstLog.TaskStatusBefore != "" {
		t.Fatalf("first TaskStatusBefore = %q, want empty", firstLog.TaskStatusBefore)
	}
	if firstLog.TaskStatusAfter != "waiting_slots" {
		t.Fatalf("first TaskStatusAfter = %q, want waiting_slots", firstLog.TaskStatusAfter)
	}
	if firstLog.PlannerAction != "start_task" {
		t.Fatalf("first PlannerAction = %q, want start_task", firstLog.PlannerAction)
	}
	if firstLog.PlannerConfidence <= 0 {
		t.Fatalf("first PlannerConfidence = %v, want positive", firstLog.PlannerConfidence)
	}
	if firstLog.ShadowPlannerAction != "start_task" {
		t.Fatalf("first ShadowPlannerAction = %q, want start_task", firstLog.ShadowPlannerAction)
	}
	if !firstLog.ShadowPlannerMatched {
		t.Fatalf("first ShadowPlannerMatched = false, want true")
	}

	_, err = a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "信工24级",
		ConversationID:    "conv-call-log",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	secondLog, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected second call log to be written")
	}
	if secondLog.ConversationEvent != "task_follow_up" {
		t.Fatalf("second ConversationEvent = %q, want task_follow_up", secondLog.ConversationEvent)
	}
	if secondLog.PlanKind != "continue_task" {
		t.Fatalf("second PlanKind = %q, want continue_task", secondLog.PlanKind)
	}
	if secondLog.KnowledgeStrength != "none" {
		t.Fatalf("second KnowledgeStrength = %q, want none", secondLog.KnowledgeStrength)
	}
	if secondLog.ActiveTaskType != "subscribe_attendance_push" {
		t.Fatalf("second ActiveTaskType = %q, want subscribe_attendance_push", secondLog.ActiveTaskType)
	}
	if secondLog.TaskStatusBefore != "waiting_slots" {
		t.Fatalf("second TaskStatusBefore = %q, want waiting_slots", secondLog.TaskStatusBefore)
	}
	if secondLog.TaskStatusAfter != "completed" {
		t.Fatalf("second TaskStatusAfter = %q, want completed", secondLog.TaskStatusAfter)
	}
	if strings.Join(secondLog.FollowUpMatchedSlots, ",") != "dept_names,scope" {
		t.Fatalf("second FollowUpMatchedSlots = %v, want [dept_names scope]", secondLog.FollowUpMatchedSlots)
	}
	if secondLog.ToolCallCount != 1 {
		t.Fatalf("second ToolCallCount = %d, want 1", secondLog.ToolCallCount)
	}
	if len(secondLog.ToolsCalled) != 1 || secondLog.ToolsCalled[0] != "subscribe_attendance_push" {
		t.Fatalf("second ToolsCalled = %v, want [subscribe_attendance_push]", secondLog.ToolsCalled)
	}
	if secondLog.PlannerAction != "continue_task" {
		t.Fatalf("second PlannerAction = %q, want continue_task", secondLog.PlannerAction)
	}
	if secondLog.PlannerConfidence <= 0 {
		t.Fatalf("second PlannerConfidence = %v, want positive", secondLog.PlannerConfidence)
	}
	if secondLog.TaskID == "" {
		t.Fatalf("second TaskID = empty, want non-empty")
	}
	if secondLog.ShadowPlannerAction != "continue_task" {
		t.Fatalf("second ShadowPlannerAction = %q, want continue_task", secondLog.ShadowPlannerAction)
	}
	if !secondLog.ShadowPlannerMatched {
		t.Fatalf("second ShadowPlannerMatched = false, want true")
	}
}

func TestAgentChatClassifiesManualSignCapabilityWithoutExecutionInShadow(t *testing.T) {
	t.Parallel()

	callLog := newTestCallLogPort()

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeShadow),
		CallLog:        callLog,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "可以执行代签功能吗",
		ConversationID:   "conv-shadow-protocol",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	log, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected call log to be written")
	}
	if log.ProtocolMode != string(ProtocolModeShadow) {
		t.Fatalf("ProtocolMode = %q, want %q", log.ProtocolMode, ProtocolModeShadow)
	}
	if log.ProtocolAct != string(ActCapabilityQuestion) {
		t.Fatalf("ProtocolAct = %q, want %q", log.ProtocolAct, ActCapabilityQuestion)
	}
	if log.ProtocolDomain != string(DomainManualSign) {
		t.Fatalf("ProtocolDomain = %q, want %q", log.ProtocolDomain, DomainManualSign)
	}
	if log.ProtocolOperation != "manual_sign.describe_capability" {
		t.Fatalf("ProtocolOperation = %q, want manual_sign.describe_capability", log.ProtocolOperation)
	}
	if log.ProtocolValidationCode != "capability_non_executable" {
		t.Fatalf("ProtocolValidationCode = %q, want capability_non_executable", log.ProtocolValidationCode)
	}
	if log.ExecutionAllowed {
		t.Fatalf("ExecutionAllowed = true, want false")
	}
}

func TestAgentClarifiesUnknownBusinessLikeMessageViaLiveRoute(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		Knowledge:      &testKnowledgePort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "同步失败会怎样",
		ConversationID:   "conv-unknown-business-like",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply == outOfDomainReply {
		t.Fatalf("reply = %q, should not reject business-like message before retrieval", reply)
	}
}

func TestAgentUsesRetrievalPrepassForNonObviousOutRequest(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:     "系统说明",
				Heading:   "同步异常",
				Body:      "同步异常需要管理员进一步确认。",
				SourceRef: "系统说明#同步异常",
				Score:     3,
			},
		},
	}

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "同步失败会怎样",
		ConversationID:   "conv-retrieval-prepass",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 1 {
		t.Fatalf("knowledge calls = %d, want 1", knowledge.calls)
	}
}

func TestAgentClarifiesWeakKnowledgeMatchInsteadOfRejecting(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:     "系统说明",
				Heading:   "同步异常",
				Body:      "同步异常需要管理员进一步确认。",
				SourceRef: "系统说明#同步异常",
				Score:     3,
			},
		},
	}

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "考勤同步异常怎么处理",
		ConversationID:   "conv-weak-knowledge",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply == outOfDomainReply {
		t.Fatalf("reply = %q, should not be out-of-domain", reply)
	}
	if reply == noKnowledgeReply {
		t.Fatalf("reply = %q, should clarify instead of rejecting weak knowledge match", reply)
	}
}

// TestAgentChatInjectsKnowledgeContextBeforeToolCallsForMixedQuestions 验证 mixed 路径会在保留工具时注入知识上下文。
func TestAgentChatInjectsKnowledgeContextBeforeToolCallsForMixedQuestions(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:     "考勤规则",
				Heading:   "迟到判定",
				Body:      "上课开始后超过 10 分钟打卡视为迟到。",
				SourceRef: "考勤规则#1",
				Score:     18,
			},
		},
	}

	var requests []capturedChatRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		requests = append(requests, req)
		current := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch current {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_current_time","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"今天第一节还未到的人员已查询，同时迟到以开课后 10 分钟为界。"},"finish_reason":"stop"}]}`))
		default:
			http.Error(w, `{"error":{"message":"unexpected extra request"}}`, http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:     server.URL,
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "今天第一节谁未到，并说明迟到判定规则",
		ConversationID:   "conv-1",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply == "" {
		t.Fatalf("Chat() reply should not be empty")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	if len(requests[0].Tools) == 0 {
		t.Fatalf("mixed request should keep tools")
	}
	if !requestContains(requests[0], "考勤规则#1") {
		t.Fatalf("mixed request missing source ref, messages = %+v", requests[0].Messages)
	}
	if !requestContains(requests[0], "上课开始后超过 10 分钟打卡视为迟到") {
		t.Fatalf("mixed request missing knowledge body, messages = %+v", requests[0].Messages)
	}
}

func TestAgentChatPromptsForSubscriptionScopeBeforeExecuting(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{
		hits: []agenttools.KnowledgeHit{
			{
				Title:      "系统总览",
				SourcePath: "agent-knowledge/system-overview.md",
				DocType:    "overview",
				Audience:   "admin",
				Intent:     "subscription",
				Heading:    "群考勤自动推送",
				Body:       "管理员可在群聊中订阅考勤自动推送。",
				SourceRef:  "系统总览#群考勤自动推送",
				Score:      18,
			},
		},
	}
	groupSub := &testGroupSubPort{}

	var requests []capturedChatRequest
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req capturedChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		requests = append(requests, req)
		current := len(requests)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch current {
		case 1:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"subscribe_attendance_push","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"已为此群开启考勤推送。"},"finish_reason":"stop"}]}`))
		default:
			http.Error(w, `{"error":{"message":"unexpected extra request"}}`, http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	a := NewAgent(Deps{
		LLMBaseURL:     server.URL,
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		Knowledge:      knowledge,
		GroupSub:       groupSub,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "添加考勤订阅",
		ConversationID:    "conv-subscribe",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply == "" {
		t.Fatalf("Chat() reply should not be empty")
	}
	if !strings.Contains(reply, "需要先确认订阅范围") {
		t.Fatalf("reply = %q, want clarify scope prompt", reply)
	}
	if !strings.Contains(reply, "全部人员") {
		t.Fatalf("reply = %q, want clarify options", reply)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 0 {
		t.Fatalf("request count = %d, want 0", len(requests))
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge calls = %d, want 0", knowledge.calls)
	}

	groupSub.mu.Lock()
	defer groupSub.mu.Unlock()
	if groupSub.subscribeCalls != 0 {
		t.Fatalf("subscribe calls = %d, want 0", groupSub.subscribeCalls)
	}
}

func TestAgentChatAnswersCapabilityQuestionWithoutKnowledgeLookup(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "你有什么功能",
		ConversationID:   "conv-help",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "我可以帮助处理这些系统能力") {
		t.Fatalf("Chat() reply = %q, want capability overview", reply)
	}
	if !strings.Contains(reply, "你当前在这个会话里可直接使用") {
		t.Fatalf("Chat() reply = %q, want current availability section", reply)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge search calls = %d, want 0", knowledge.calls)
	}
}

func TestAgentChatProtocolLiveKeepsAttendanceQueryOutOfSubscriptionWorkflow(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{
		detailResp: &agenttools.AttendanceResult{
			Date:         "2026-04-07",
			Week:         8,
			Section:      2,
			ViewMode:     "current",
			ShouldAttend: 3,
			OnTimeCount:  1,
			LateCount:    1,
			LeaveCount:   0,
			AbsentCount:  1,
		},
	}

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		Attendance:     attendance,
		GroupSub:       testClarifyGroupSubPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	a.sessions.setActiveTask("42:conv-protocol-live-attendance:ding-user", &ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"dept_names"},
		FilledSlots:   map[string]string{"scope": "department"},
		ExpiresAt:     time.Now().Add(sessionTTL),
		LastPrompt:    "clarify_dept_names",
	})

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "查询一下今天第二节的考勤状态",
		ConversationID:    "conv-protocol-live-attendance",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if strings.Contains(reply, "订阅范围") {
		t.Fatalf("reply = %q, should not re-enter subscription workflow", reply)
	}
	if !strings.Contains(reply, "考勤状态") {
		t.Fatalf("reply = %q, want attendance result", reply)
	}

	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.detailCalls != 1 {
		t.Fatalf("GetAttendanceDetail() call count = %d, want 1", attendance.detailCalls)
	}
}

func TestAgentChatProtocolLiveAnswersManualSignCapabilityWithoutExecution(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		Attendance:     attendance,
		GroupSub:       testClarifyGroupSubPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	a.sessions.setActiveTask("42:conv-protocol-live-capability:ding-user", &ActiveTask{
		Type:          "subscribe_attendance_push",
		Status:        taskStatusWaiting,
		RequiredSlots: []string{"dept_names"},
		FilledSlots:   map[string]string{"scope": "department"},
		ExpiresAt:     time.Now().Add(sessionTTL),
		LastPrompt:    "clarify_dept_names",
	})

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "可以执行代签功能吗",
		ConversationID:    "conv-protocol-live-capability",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if strings.Contains(reply, "以下部门名称不存在") {
		t.Fatalf("reply = %q, should not treat capability question as department input", reply)
	}
	if !strings.Contains(reply, "代签") {
		t.Fatalf("reply = %q, want capability answer", reply)
	}

	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.signCalls != 0 {
		t.Fatalf("SignForUsersBySlot() call count = %d, want 0", attendance.signCalls)
	}
}

func TestProtocolLiveReadQueryBeatsActiveWorkflow(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{
		detailResp: &agenttools.AttendanceResult{
			Date:         "2026-04-07",
			Week:         8,
			Section:      2,
			ViewMode:     "current",
			ShouldAttend: 3,
			OnTimeCount:  2,
			LateCount:    0,
			LeaveCount:   0,
			AbsentCount:  1,
		},
	}

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		Attendance:     attendance,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	a.sessions.setWorkflowState("42:conv-protocol-live-read-wins:ding-user", &WorkflowSnapshot{
		ID:           "wf-subscription",
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectDepartments,
		MissingSlots: []string{"dept_names"},
		Trusted: trustedEntities{
			Scope: "department",
		},
	})

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "查询一下今天第二节的考勤状态",
		ConversationID:    "conv-protocol-live-read-wins",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "考勤状态") {
		t.Fatalf("reply = %q, want attendance result", reply)
	}

	_, workflow := a.sessions.getWorkflowState("42:conv-protocol-live-read-wins:ding-user")
	if workflow == nil || workflow.Type != WorkflowSubscriptionStart {
		t.Fatalf("workflow = %+v, want subscription workflow kept open", workflow)
	}

	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.detailCalls != 1 {
		t.Fatalf("GetAttendanceDetail() call count = %d, want 1", attendance.detailCalls)
	}
}

func TestProtocolLiveCapabilityQuestionNeverCallsBusinessTool(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{}
	groupSub := &testGroupSubPort{}
	callLog := newTestCallLogPort()
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		Attendance:     attendance,
		GroupSub:       groupSub,
		CallLog:        callLog,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	a.sessions.setWorkflowState("42:conv-protocol-live-capability-guard:ding-user", &WorkflowSnapshot{
		ID:           "wf-subscription",
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectDepartments,
		MissingSlots: []string{"dept_names"},
		Trusted: trustedEntities{
			Scope: "department",
		},
	})

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "可以执行代签功能吗",
		ConversationID:    "conv-protocol-live-capability-guard",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "代签") {
		t.Fatalf("reply = %q, want capability answer", reply)
	}

	log, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected call log to be written")
	}
	if log.ResponseKind != string(ResponseAnswer) {
		t.Fatalf("ResponseKind = %q, want %q", log.ResponseKind, ResponseAnswer)
	}

	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.signCalls != 0 {
		t.Fatalf("SignForUsersBySlot() call count = %d, want 0", attendance.signCalls)
	}

	groupSub.mu.Lock()
	defer groupSub.mu.Unlock()
	if groupSub.subscribeCalls != 0 || groupSub.unsubscribeCalls != 0 {
		t.Fatalf("group subscription calls = subscribe:%d unsubscribe:%d, want both 0", groupSub.subscribeCalls, groupSub.unsubscribeCalls)
	}
}

func TestProtocolLiveBlockedRequestsReturnSafeClarifyWithoutLegacyFallback(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{}
	callLog := newTestCallLogPort()
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		Knowledge:      knowledge,
		CallLog:        callLog,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "同步失败会怎样",
		ConversationID:   "conv-protocol-live-blocked",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "请再明确") {
		t.Fatalf("reply = %q, want safe clarify", reply)
	}
	if strings.Contains(reply, "AI 服务暂时不可用") {
		t.Fatalf("reply = %q, should not fall back to legacy llm path", reply)
	}

	log, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected call log to be written")
	}
	if log.ResponseKind != string(ResponseClarify) {
		t.Fatalf("ResponseKind = %q, want %q", log.ResponseKind, ResponseClarify)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge search calls = %d, want 0", knowledge.calls)
	}
}

func TestSubscriptionWorkflowStartsAndExecutesAllScopeInProtocolLive(t *testing.T) {
	t.Parallel()

	groupSub := &testGroupSubPort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		GroupSub:       groupSub,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "帮我开启本群考勤订阅",
		ConversationID:    "conv-protocol-subscribe-all",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}
	if !strings.Contains(reply, "订阅范围") {
		t.Fatalf("reply = %q, want scope clarify", reply)
	}
	_, activeTask := a.sessions.getSessionState("42:conv-protocol-subscribe-all:ding-user")
	if activeTask != nil {
		t.Fatalf("activeTask = %+v, want nil when protocol workflow owns the flow", activeTask)
	}
	_, workflow := a.sessions.getWorkflowState("42:conv-protocol-subscribe-all:ding-user")
	if workflow == nil || workflow.Type != WorkflowSubscriptionStart || workflow.State != WorkflowCollectScope {
		t.Fatalf("workflow = %+v, want subscription collect_scope", workflow)
	}

	reply, err = a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "全部人员",
		ConversationID:    "conv-protocol-subscribe-all",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if !strings.Contains(reply, "已为此群开启考勤推送") {
		t.Fatalf("reply = %q, want subscribe success", reply)
	}
	_, workflow = a.sessions.getWorkflowState("42:conv-protocol-subscribe-all:ding-user")
	if workflow != nil {
		t.Fatalf("workflow = %+v, want cleared after success", workflow)
	}

	groupSub.mu.Lock()
	defer groupSub.mu.Unlock()
	if groupSub.subscribeCalls != 1 {
		t.Fatalf("Subscribe() call count = %d, want 1", groupSub.subscribeCalls)
	}
	if len(groupSub.lastSubscribedIDs) != 0 {
		t.Fatalf("deptIDs = %v, want empty for all scope", groupSub.lastSubscribedIDs)
	}
}

func TestSubscriptionWorkflowListsDepartmentsAndExecutesDepartmentScopeInProtocolLive(t *testing.T) {
	t.Parallel()

	groupSub := &testGroupSubPort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		GroupSub:       groupSub,
		Dept:           testClarifyDeptPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "帮我开启本群考勤订阅",
		ConversationID:    "conv-protocol-subscribe-dept",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat(start) error = %v", err)
	}
	if !strings.Contains(reply, "订阅范围") {
		t.Fatalf("start reply = %q, want scope clarify", reply)
	}
	_, workflow := a.sessions.getWorkflowState("42:conv-protocol-subscribe-dept:ding-user")
	if workflow == nil || workflow.State != WorkflowCollectScope {
		t.Fatalf("workflow after start = %+v, want collect_scope", workflow)
	}

	reply, err = a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "指定部门",
		ConversationID:    "conv-protocol-subscribe-dept",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat(scope) error = %v", err)
	}
	_, workflow = a.sessions.getWorkflowState("42:conv-protocol-subscribe-dept:ding-user")
	if workflow == nil || workflow.State != WorkflowCollectDepartments {
		t.Fatalf("workflow after scope = %+v, want collect_departments", workflow)
	}

	reply, err = a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "现在都有哪些部门",
		ConversationID:    "conv-protocol-subscribe-dept",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat(list depts) error = %v", err)
	}
	if !strings.Contains(reply, "信工24级") {
		t.Fatalf("list departments reply = %q, want department options", reply)
	}

	reply, err = a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "信工24级",
		ConversationID:    "conv-protocol-subscribe-dept",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat(dept) error = %v", err)
	}
	if !strings.Contains(reply, "已为此群开启考勤推送") {
		t.Fatalf("final reply = %q, want subscribe success", reply)
	}
	_, workflow = a.sessions.getWorkflowState("42:conv-protocol-subscribe-dept:ding-user")
	if workflow != nil {
		t.Fatalf("workflow = %+v, want cleared after success", workflow)
	}

	groupSub.mu.Lock()
	defer groupSub.mu.Unlock()
	if groupSub.subscribeCalls != 1 {
		t.Fatalf("Subscribe() call count = %d, want 1", groupSub.subscribeCalls)
	}
	if len(groupSub.lastSubscribedIDs) != 1 || groupSub.lastSubscribedIDs[0] != 101 {
		t.Fatalf("deptIDs = %v, want [101]", groupSub.lastSubscribedIDs)
	}
}

func TestSubscriptionWorkflowRejectsCapabilityQuestionDuringDeptCollection(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		Dept:           testClarifyDeptPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	a.sessions.setWorkflowState("42:conv-protocol-subscription-capability:ding-user", &WorkflowSnapshot{
		Type:         WorkflowSubscriptionStart,
		State:        WorkflowCollectDepartments,
		MissingSlots: []string{"dept_names"},
		Trusted: trustedEntities{
			Scope: "department",
		},
	})

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "可以执行代签功能吗",
		ConversationID:    "conv-protocol-subscription-capability",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if strings.Contains(reply, "以下部门名称不存在") {
		t.Fatalf("reply = %q, should not treat capability question as department input", reply)
	}
	if !strings.Contains(reply, "代签") {
		t.Fatalf("reply = %q, want capability answer", reply)
	}
}

func TestSubscriptionWorkflowCancelsOnExplicitWriteRequestInProtocolLive(t *testing.T) {
	t.Parallel()

	callLog := newTestCallLogPort()
	groupSub := &testGroupSubPort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		CallLog:        callLog,
		GroupSub:       groupSub,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "取消本群考勤订阅",
		ConversationID:    "conv-protocol-unsubscribe",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "已取消此群的考勤自动推送") {
		t.Fatalf("reply = %q, want unsubscribe success", reply)
	}

	log, ok := callLog.Wait(time.Second)
	if !ok {
		t.Fatalf("expected call log to be written")
	}
	if log.ProtocolOperation != "subscription.cancel" {
		t.Fatalf("ProtocolOperation = %q, want subscription.cancel", log.ProtocolOperation)
	}
	if log.ResponseKind != string(ResponseResult) {
		t.Fatalf("ResponseKind = %q, want %q", log.ResponseKind, ResponseResult)
	}

	groupSub.mu.Lock()
	defer groupSub.mu.Unlock()
	if groupSub.unsubscribeCalls != 1 {
		t.Fatalf("Unsubscribe() call count = %d, want 1", groupSub.unsubscribeCalls)
	}
}

func TestManualSignWorkflowExecutesExplicitRequestInProtocolLive(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		Attendance:     attendance,
		User:           testTaskUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "帮我给张三补签今天第二节考勤",
		ConversationID:   "conv-protocol-manual-sign",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "已为张三") {
		t.Fatalf("reply = %q, want sign success", reply)
	}

	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.signCalls != 1 {
		t.Fatalf("SignForUsersBySlot() call count = %d, want 1", attendance.signCalls)
	}
}

func TestManualSignWorkflowReturnsCandidateSelectionForAmbiguousUserInProtocolLive(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		Attendance:     attendance,
		User:           testAmbiguousManualSignUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "帮我给张补签今天第二节考勤",
		ConversationID:   "conv-protocol-manual-sign-ambiguous",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "张三") || !strings.Contains(reply, "张四") {
		t.Fatalf("reply = %q, want candidate selection", reply)
	}
	_, workflow := a.sessions.getWorkflowState("42:ding-user")
	if workflow == nil || workflow.Type != WorkflowManualSignCreate {
		t.Fatalf("workflow = %+v, want manual sign workflow kept open", workflow)
	}

	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.signCalls != 0 {
		t.Fatalf("SignForUsersBySlot() call count = %d, want 0", attendance.signCalls)
	}
}

func TestManualSignWorkflowDoesNotTreatSentenceAsUserName(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		ProtocolMode:   string(ProtocolModeLive),
		Attendance:     attendance,
		User:           testTaskUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	a.sessions.setWorkflowState("42:ding-user", &WorkflowSnapshot{
		Type:         WorkflowManualSignCreate,
		State:        WorkflowCollectUser,
		MissingSlots: []string{"user_id"},
		Trusted: trustedEntities{
			Date:    "2026-04-07",
			Section: 2,
		},
	})

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "今天第二节",
		ConversationID:   "conv-protocol-manual-sign-shape",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if strings.Contains(reply, "已为") {
		t.Fatalf("reply = %q, should not execute with invalid user shape", reply)
	}

	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.signCalls != 0 {
		t.Fatalf("SignForUsersBySlot() call count = %d, want 0", attendance.signCalls)
	}
}

func TestAgentChatClarifiesDepartmentScopedSubscriptionByListingDepartmentsFirst(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		GroupSub:       testClarifyGroupSubPort{},
		Dept:           testClarifyDeptPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "订阅指定部门考勤",
		ConversationID:    "conv-clarify",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "教务处") || !strings.Contains(reply, "学工处") {
		t.Fatalf("Chat() reply = %q, want listed departments", reply)
	}
	if !strings.Contains(reply, "需要订阅哪些部门") {
		t.Fatalf("Chat() reply = %q, want follow-up prompt", reply)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge search calls = %d, want 0", knowledge.calls)
	}
}

func TestAgentChatChecksSubscriptionStatusDirectlyInGroup(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		Knowledge:      knowledge,
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		GroupSub: testClarifyGroupSubPort{
			info: &agenttools.GroupSubInfo{
				Subscribed: true,
				GroupName:  "测试群",
			},
		},
		Dept:   testClarifyDeptPort{},
		Tenant: testTenantPort{},
		Logger: zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "查这个群有没有订阅考勤推送",
		ConversationID:    "conv-status",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if !strings.Contains(reply, "已订阅") {
		t.Fatalf("Chat() reply = %q, want subscribed status", reply)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge search calls = %d, want 0", knowledge.calls)
	}
}

func TestAgentChatAcceptsChineseNumeralDepartmentAliasDuringSubscriptionFollowUp(t *testing.T) {
	t.Parallel()

	groupSub := &testGroupSubPort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		GroupSub:       groupSub,
		Dept:           testFamilyDeptPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "开启考勤订阅",
		ConversationID:    "conv-family-alias",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}

	secondReply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "家族七期",
		ConversationID:    "conv-family-alias",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if !strings.Contains(secondReply, "家族七期") {
		t.Fatalf("second reply = %q, want subscription success for alias", secondReply)
	}

	groupSub.mu.Lock()
	defer groupSub.mu.Unlock()
	if groupSub.subscribeCalls != 1 {
		t.Fatalf("subscribe calls = %d, want 1", groupSub.subscribeCalls)
	}
	if len(groupSub.lastSubscribedIDs) != 1 || groupSub.lastSubscribedIDs[0] != 201 {
		t.Fatalf("subscribed dept ids = %v, want [201]", groupSub.lastSubscribedIDs)
	}
}

func TestAgentChatKeepsSubscriptionTaskAfterInvalidDepartmentAndListsDepartmentsOnFollowUp(t *testing.T) {
	t.Parallel()

	groupSub := &testGroupSubPort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		GroupSub:       groupSub,
		Dept:           testFamilyDeptPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "开启考勤订阅",
		ConversationID:    "conv-family-retry",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}

	secondReply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "家族九期",
		ConversationID:    "conv-family-retry",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if !strings.Contains(secondReply, "以下部门名称不存在") {
		t.Fatalf("second reply = %q, want invalid department guidance", secondReply)
	}

	thirdReply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "现在都有哪些部门",
		ConversationID:    "conv-family-retry",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("third Chat() error = %v", err)
	}
	if !strings.Contains(thirdReply, "家族7期") || !strings.Contains(thirdReply, "乐知全栈一期") {
		t.Fatalf("third reply = %q, want department list while staying in task", thirdReply)
	}

	fourthReply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "家族7期",
		ConversationID:    "conv-family-retry",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("fourth Chat() error = %v", err)
	}
	if !strings.Contains(fourthReply, "家族7期") {
		t.Fatalf("fourth reply = %q, want corrected department to complete subscription", fourthReply)
	}

	groupSub.mu.Lock()
	defer groupSub.mu.Unlock()
	if groupSub.subscribeCalls != 1 {
		t.Fatalf("subscribe calls = %d, want 1", groupSub.subscribeCalls)
	}
	if len(groupSub.lastSubscribedIDs) != 1 || groupSub.lastSubscribedIDs[0] != 201 {
		t.Fatalf("subscribed dept ids = %v, want [201]", groupSub.lastSubscribedIDs)
	}
}

func TestAgentChatExplainsRetryableSubscriptionFailureAndKeepsTaskOpen(t *testing.T) {
	t.Parallel()

	groupSub := &testGroupSubPort{}
	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		GroupSub:       groupSub,
		Dept:           testFamilyDeptPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "开启考勤订阅",
		ConversationID:    "conv-family-explain",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}

	_, err = a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "家族九期",
		ConversationID:    "conv-family-explain",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}

	thirdReply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "为什么失败",
		ConversationID:    "conv-family-explain",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("third Chat() error = %v", err)
	}
	if !strings.Contains(thirdReply, "部门") || !strings.Contains(thirdReply, "可选部门") {
		t.Fatalf("third reply = %q, want retry explanation with correction guidance", thirdReply)
	}

	fourthReply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "家族7期",
		ConversationID:    "conv-family-explain",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("fourth Chat() error = %v", err)
	}
	if !strings.Contains(fourthReply, "家族7期") {
		t.Fatalf("fourth reply = %q, want corrected department to complete subscription", fourthReply)
	}
}

func TestAgentChatResumesManualSignTaskAcrossMultipleReplies(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{}
	routerServer := newRouteDecisionServer(t,
		RouteDecision{Kind: RouteTaskStart, TargetTaskType: "sign_for_user"},
		RouteDecision{Kind: RouteTaskContinue},
		RouteDecision{Kind: RouteTaskContinue},
	)
	defer routerServer.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       "http://127.0.0.1:0",
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		Attendance:       attendance,
		User:             testTaskUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	firstReply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "帮我补签",
		ConversationID:   "conv-sign",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}
	if !strings.Contains(firstReply, "姓名") {
		t.Fatalf("first reply = %q, want user guidance", firstReply)
	}

	secondReply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "张三",
		ConversationID:   "conv-sign",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if !strings.Contains(secondReply, "日期") || !strings.Contains(secondReply, "节次") {
		t.Fatalf("second reply = %q, want date and section guidance", secondReply)
	}

	thirdReply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "今天第一节",
		ConversationID:   "conv-sign",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("third Chat() error = %v", err)
	}
	if !strings.Contains(thirdReply, "张三") {
		t.Fatalf("third reply = %q, want sign success", thirdReply)
	}

	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.signCalls != 1 {
		t.Fatalf("sign calls = %d, want 1", attendance.signCalls)
	}
	if attendance.lastSection != 1 {
		t.Fatalf("last section = %d, want 1", attendance.lastSection)
	}
	if len(attendance.lastUserIDs) != 1 || attendance.lastUserIDs[0] != 99 {
		t.Fatalf("last user ids = %v, want [99]", attendance.lastUserIDs)
	}
}

func TestAgentChatUsesPlannerPrimaryForLongManualSignFollowUp(t *testing.T) {
	t.Parallel()

	attendance := &testTaskAttendancePort{}
	routerServer := newRouteDecisionServer(t,
		RouteDecision{Kind: RouteTaskStart, TargetTaskType: "sign_for_user"},
		RouteDecision{Kind: RouteTaskContinue},
	)
	defer routerServer.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       "http://127.0.0.1:0",
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		Attendance:       attendance,
		User:             testTaskUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "帮我补签",
		ConversationID:   "conv-sign-long-follow-up",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}

	secondReply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "请帮我给张三补签今天第一节考勤",
		ConversationID:   "conv-sign-long-follow-up",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if !strings.Contains(secondReply, "张三") {
		t.Fatalf("second reply = %q, want sign success for long follow-up", secondReply)
	}

	attendance.mu.Lock()
	defer attendance.mu.Unlock()
	if attendance.signCalls != 1 {
		t.Fatalf("sign calls = %d, want 1", attendance.signCalls)
	}
	if attendance.lastSection != 1 {
		t.Fatalf("last section = %d, want 1", attendance.lastSection)
	}
	if len(attendance.lastUserIDs) != 1 || attendance.lastUserIDs[0] != 99 {
		t.Fatalf("last user ids = %v, want [99]", attendance.lastUserIDs)
	}
}

func TestAgentChatCancelsActiveTaskWhenUserSaysCancel(t *testing.T) {
	t.Parallel()

	routerServer := newRouteDecisionServer(t,
		RouteDecision{Kind: RouteTaskStart, TargetTaskType: "subscribe_attendance_push"},
		RouteDecision{Kind: RouteTaskCancel},
	)
	defer routerServer.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       "http://127.0.0.1:0",
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		GroupSub:         &testGroupSubPort{},
		Dept:             testClarifyDeptPort{},
		User:             testUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "开启考勤订阅",
		ConversationID:    "conv-cancel",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "取消",
		ConversationID:    "conv-cancel",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("cancel Chat() error = %v", err)
	}
	if !strings.Contains(reply, "取消") {
		t.Fatalf("reply = %q, want cancel acknowledgement", reply)
	}

	_, task := a.sessions.getSessionState("42:conv-cancel:ding-user")
	if task != nil {
		t.Fatalf("active task = %#v, want nil", task)
	}
}

func TestAgentChatSwitchesToNewRequestWhenNewBusinessQuestionArrives(t *testing.T) {
	t.Parallel()

	routerServer := newRouteDecisionServer(t,
		RouteDecision{Kind: RouteTaskStart, TargetTaskType: "subscribe_attendance_push"},
		RouteDecision{
			Kind:           RouteTaskStart,
			TargetTaskType: "query_subscription_status",
			SwitchTask:     true,
			SoftNoticeCode: "switched_task",
		},
	)
	defer routerServer.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       "http://127.0.0.1:0",
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		GroupSub: testClarifyGroupSubPort{
			info: &agenttools.GroupSubInfo{},
		},
		Dept:           testClarifyDeptPort{},
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	_, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "开启考勤订阅",
		ConversationID:    "conv-switch",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:            "corp-1",
		SenderID:          "ding-user",
		SenderNick:        "Alice",
		Content:           "查这个群有没有订阅考勤推送",
		ConversationID:    "conv-switch",
		ConversationType:  "2",
		ConversationTitle: "测试群",
	})
	if err != nil {
		t.Fatalf("switch Chat() error = %v", err)
	}
	if !strings.Contains(reply, "先切到") {
		t.Fatalf("reply = %q, want soft switch notice", reply)
	}
	if !strings.Contains(reply, "还没有订阅") {
		t.Fatalf("reply = %q, want subscription status response", reply)
	}
}

func TestAgentChatUnsubscribesForExplicitClosePhrasesInLiveRoute(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		content        string
		routerDecision RouteDecision
	}{
		{
			name:           "close subscription",
			content:        "关闭考勤订阅",
			routerDecision: RouteDecision{Kind: RouteTaskStart, TargetTaskType: "subscribe_attendance_push"},
		},
		{
			name:           "close current group subscription",
			content:        "关闭本群考勤订阅",
			routerDecision: RouteDecision{Kind: RouteTaskStart, TargetTaskType: "subscribe_attendance_push"},
		},
		{
			name:           "cancel current group subscription",
			content:        "取消本群考勤订阅",
			routerDecision: RouteDecision{Kind: RouteTaskCancel},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			routerServer := newRouteDecisionServer(t, tc.routerDecision)
			defer routerServer.Close()

			groupSub := &testGroupSubPort{}
			a := NewAgent(Deps{
				LLMBaseURL:       "http://127.0.0.1:0",
				LLMAPIKey:        "test-key",
				LLMModel:         "test-model",
				RouterLLMBaseURL: routerServer.URL,
				RouterLLMAPIKey:  "test-key",
				RouterLLMModel:   "router-model",
				RouteMode:        string(RouteModeLive),
				GroupSub:         groupSub,
				User:             testUserPort{},
				Semester:         testSemesterPort{},
				SchedulePeriod:   testSchedulePeriodPort{},
				Tenant:           testTenantPort{},
				Logger:           zap.NewNop().Sugar(),
			})
			defer a.Stop()

			reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
				CorpID:            "corp-1",
				SenderID:          "ding-user",
				SenderNick:        "Alice",
				Content:           tc.content,
				ConversationID:    "conv-unsubscribe-live",
				ConversationType:  "2",
				ConversationTitle: "测试群",
			})
			if err != nil {
				t.Fatalf("Chat() error = %v", err)
			}
			if !strings.Contains(reply, "已取消此群的考勤自动推送") {
				t.Fatalf("reply = %q, want unsubscribe success", reply)
			}

			groupSub.mu.Lock()
			defer groupSub.mu.Unlock()
			if groupSub.unsubscribeCalls != 1 {
				t.Fatalf("Unsubscribe() call count = %d, want 1", groupSub.unsubscribeCalls)
			}
			if groupSub.subscribeCalls != 0 {
				t.Fatalf("Subscribe() call count = %d, want 0", groupSub.subscribeCalls)
			}
		})
	}
}

func TestAgentChatUnsubscribesForExplicitClosePhrasesInLegacyMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
	}{
		{name: "close subscription", content: "关闭考勤订阅"},
		{name: "close current group subscription", content: "关闭本群考勤订阅"},
		{name: "cancel current group subscription", content: "取消本群考勤订阅"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			groupSub := &testGroupSubPort{}
			a := NewAgent(Deps{
				LLMBaseURL:     "http://127.0.0.1:0",
				LLMAPIKey:      "test-key",
				LLMModel:       "test-model",
				RouteMode:      string(RouteModeOff),
				GroupSub:       groupSub,
				User:           testUserPort{},
				Semester:       testSemesterPort{},
				SchedulePeriod: testSchedulePeriodPort{},
				Tenant:         testTenantPort{},
				Logger:         zap.NewNop().Sugar(),
			})
			defer a.Stop()

			reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
				CorpID:            "corp-1",
				SenderID:          "ding-user",
				SenderNick:        "Alice",
				Content:           tc.content,
				ConversationID:    "conv-unsubscribe-legacy",
				ConversationType:  "2",
				ConversationTitle: "测试群",
			})
			if err != nil {
				t.Fatalf("Chat() error = %v", err)
			}
			if !strings.Contains(reply, "已取消此群的考勤自动推送") {
				t.Fatalf("reply = %q, want unsubscribe success", reply)
			}

			groupSub.mu.Lock()
			defer groupSub.mu.Unlock()
			if groupSub.unsubscribeCalls != 1 {
				t.Fatalf("Unsubscribe() call count = %d, want 1", groupSub.unsubscribeCalls)
			}
			if groupSub.subscribeCalls != 0 {
				t.Fatalf("Subscribe() call count = %d, want 0", groupSub.subscribeCalls)
			}
		})
	}
}

func TestAgentChatClarifiesTaskCancelWithoutActiveTaskInLiveRoute(t *testing.T) {
	t.Parallel()

	routerServer := newRouteDecisionServer(t, RouteDecision{Kind: RouteTaskCancel})
	defer routerServer.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       "http://127.0.0.1:0",
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		User:             testUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "取消",
		ConversationID:   "conv-empty-cancel",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if strings.Contains(reply, "已取消当前任务") {
		t.Fatalf("reply = %q, should not acknowledge cancel without active task", reply)
	}
	if !strings.Contains(reply, "没完全理解") {
		t.Fatalf("reply = %q, want clarify fallback", reply)
	}
}

func TestAgentDoesNotRejectUnknownBusinessLikeMessageBeforeRetrieval(t *testing.T) {
	t.Parallel()

	knowledge := &testKnowledgePort{}
	routerServer := newRouteDecisionServer(t, RouteDecision{
		Kind:        RouteClarify,
		ClarifyCode: "ambiguous_intent",
	})
	defer routerServer.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       "http://127.0.0.1:0",
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		Knowledge:        knowledge,
		User:             testUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "这个怎么算",
		ConversationID:   "conv-ambiguous-business",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply == outOfDomainReply {
		t.Fatalf("reply = %q, should clarify instead of reject", reply)
	}
	if !strings.Contains(reply, "没完全理解") {
		t.Fatalf("reply = %q, want clarify guidance", reply)
	}

	knowledge.mu.Lock()
	defer knowledge.mu.Unlock()
	if knowledge.calls != 0 {
		t.Fatalf("knowledge search calls = %d, want 0", knowledge.calls)
	}
}

func TestAgentChatRepliesPolitelyToGreetingWithoutDomainReject(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		RouteMode:      string(RouteModeOff),
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "你好",
		ConversationID:   "conv-greeting",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if reply == outOfDomainReply {
		t.Fatalf("reply = %q, should not be out-of-domain", reply)
	}
	if !strings.Contains(reply, "你好") {
		t.Fatalf("reply = %q, want greeting", reply)
	}
}

func TestAgentChatPolitelyRefusesGenericSocialChatWithoutLLMFallback(t *testing.T) {
	t.Parallel()

	a := NewAgent(Deps{
		LLMBaseURL:     "http://127.0.0.1:0",
		LLMAPIKey:      "test-key",
		LLMModel:       "test-model",
		User:           testUserPort{},
		Semester:       testSemesterPort{},
		SchedulePeriod: testSchedulePeriodPort{},
		Tenant:         testTenantPort{},
		Logger:         zap.NewNop().Sugar(),
	})
	defer a.Stop()

	reply, err := a.Chat(context.Background(), &dingtalk.ChatMessage{
		CorpID:           "corp-1",
		SenderID:         "ding-user",
		SenderNick:       "Alice",
		Content:          "最近怎么样",
		ConversationID:   "conv-social",
		ConversationType: "1",
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if strings.Contains(reply, "AI 服务暂时不可用") {
		t.Fatalf("reply = %q, want polite refusal instead of llm failure", reply)
	}
	if !strings.Contains(reply, "课表") || !strings.Contains(reply, "考勤") {
		t.Fatalf("reply = %q, want polite domain-scoped refusal", reply)
	}
}

// requestContains 判断发给模型的请求里是否包含指定片段。
func requestContains(req capturedChatRequest, needle string) bool {
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}

func requestToolNames(req capturedChatRequest) []string {
	names := make([]string, 0, len(req.Tools))
	for _, raw := range req.Tools {
		var toolDef struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if err := json.Unmarshal(raw, &toolDef); err != nil {
			continue
		}
		if strings.TrimSpace(toolDef.Function.Name) == "" {
			continue
		}
		names = append(names, toolDef.Function.Name)
	}
	return names
}

func TestAgentSemanticRouterHandlesTaskMetaQuestion(t *testing.T) {
	t.Parallel()

	routerServer := newRouteDecisionServer(t,
		RouteDecision{Kind: RouteTaskStart, TargetTaskType: "subscribe_attendance_push"},
		RouteDecision{Kind: RouteTaskMeta},
		RouteDecision{Kind: RouteTaskContinue},
	)
	defer routerServer.Close()

	a := NewAgent(Deps{
		LLMBaseURL:       "http://127.0.0.1:0",
		LLMAPIKey:        "test-key",
		LLMModel:         "test-model",
		RouterLLMBaseURL: routerServer.URL,
		RouterLLMAPIKey:  "test-key",
		RouterLLMModel:   "router-model",
		RouteMode:        string(RouteModeLive),
		GroupSub:         &testGroupSubPort{},
		Dept:             testClarifyDeptPort{},
		User:             testUserPort{},
		Semester:         testSemesterPort{},
		SchedulePeriod:   testSchedulePeriodPort{},
		Tenant:           testTenantPort{},
		Logger:           zap.NewNop().Sugar(),
	})
	defer a.Stop()

	chatMsg := func(content string) *dingtalk.ChatMessage {
		return &dingtalk.ChatMessage{
			CorpID:            "corp-1",
			SenderID:          "ding-user",
			SenderNick:        "Alice",
			Content:           content,
			ConversationID:    "conv-sub-meta",
			ConversationType:  "2",
			ConversationTitle: "测试群",
		}
	}

	// Step 1: Start subscription task — proactively shows department list
	firstReply, err := a.Chat(context.Background(), chatMsg("在这个群聊添加考勤订阅"))
	if err != nil {
		t.Fatalf("first Chat() error = %v", err)
	}
	if !strings.Contains(firstReply, "信工24级") {
		t.Fatalf("first reply = %q, want department list containing 信工24级", firstReply)
	}

	// Step 2: Ask for department list (meta question) — still shows list, not a dead loop
	secondReply, err := a.Chat(context.Background(), chatMsg("都有哪些部门"))
	if err != nil {
		t.Fatalf("second Chat() error = %v", err)
	}
	if !strings.Contains(secondReply, "信工24级") {
		t.Fatalf("second reply = %q, want department list containing 信工24级", secondReply)
	}
	if !strings.Contains(secondReply, "教务处") {
		t.Fatalf("second reply = %q, want department list containing 教务处", secondReply)
	}

	// Step 3: Provide department name (task continue)
	thirdReply, err := a.Chat(context.Background(), chatMsg("信工24级"))
	if err != nil {
		t.Fatalf("third Chat() error = %v", err)
	}
	if !strings.Contains(thirdReply, "开启") && !strings.Contains(thirdReply, "订阅") {
		t.Fatalf("third reply = %q, want subscription confirmation", thirdReply)
	}
}

func newRouteDecisionServer(t *testing.T, decisions ...RouteDecision) *httptest.Server {
	t.Helper()

	var mu sync.Mutex
	index := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		if index >= len(decisions) {
			http.Error(w, `{"error":{"message":"unexpected router request"}}`, http.StatusInternalServerError)
			return
		}

		decision := decisions[index]
		if isTaskActionRoute(decision.Kind) && decision.Confidence == 0 {
			decision.Confidence = 0.95
		}
		payload, err := json.Marshal(decision)
		if err != nil {
			t.Fatalf("marshal route decision: %v", err)
		}
		index++

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` + strconv.Quote(string(payload)) + `},"finish_reason":"stop"}]}`))
	}))
}
