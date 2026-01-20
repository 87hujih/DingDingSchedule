# 钉钉 Stream 模式事件订阅说明

## 概述

Stream 模式是钉钉提供的一种事件推送方式，与传统 HTTP 回调模式不同，它由**服务端主动连接钉钉**，无需公网 IP 和域名。

## 验证连接的逻辑

### 工作原理

```
┌─────────────────┐                    ┌─────────────────┐
│   你的服务器     │ ──── WebSocket ───→ │   钉钉服务器     │
│  (Stream客户端)  │ ←─── 事件推送 ────  │                 │
└─────────────────┘                    └─────────────────┘
```

1. **服务端主动连接**：你的服务启动后，Stream 客户端使用 AppKey/AppSecret 向钉钉发起 WebSocket 长连接
2. **钉钉验证凭证**：钉钉服务器验证 AppKey/AppSecret 是否有效
3. **连接建立**：验证通过后，WebSocket 连接建立成功
4. **平台检测**：当你在开放平台点击「已完成接入」时，平台检查是否存在该应用的活跃 Stream 连接
5. **事件推送**：连接建立后，钉钉通过此连接推送订阅的事件

### 验证流程时序图

```
服务器                        钉钉Stream服务                    开放平台
  │                              │                              │
  │ ─── 1. WebSocket连接请求 ───→ │                              │
  │     (携带AppKey/AppSecret)   │                              │
  │                              │                              │
  │ ←── 2. 连接建立成功 ─────────  │                              │
  │                              │                              │
  │                              │ ←── 3. 用户点击"已完成接入" ──  │
  │                              │                              │
  │                              │ ─── 4. 检查活跃连接 ─────────→ │
  │                              │                              │
  │                              │ ←── 5. 返回验证结果 ─────────  │
  │                              │                              │
  │ ←── 6. 推送事件数据 ─────────  │                              │
  │                              │                              │
  │ ─── 7. 返回处理结果 ─────────→ │                              │
  │                              │                              │
```

## 代码实现

### 1. Stream 客户端 (`pkg/dingtalk/stream.go`)

```go
// Start 启动 Stream 客户端（阻塞）
func (s *StreamClient) Start(ctx context.Context) error {
    cli := client.NewStreamClient(
        // 使用 AppKey/AppSecret 进行身份认证
        client.WithAppCredential(client.NewAppCredentialConfig(s.appKey, s.appSecret)),
        // 订阅事件类型：EVENT 表示事件订阅，"*" 表示所有事件
        client.WithSubscription("EVENT", "*", s.handleEvent),
    )

    s.client = cli
    s.logger.Infow("钉钉 Stream 客户端启动中...", "appKey", s.appKey)

    // 阻塞运行，内部维护 WebSocket 长连接
    return cli.Start(ctx)
}
```

**关键点：**
- `client.NewStreamClient()` 创建 SDK 客户端
- `client.WithAppCredential()` 配置认证凭证，这是验证连接的核心
- `client.WithSubscription("EVENT", "*", handler)` 订阅所有事件
- `cli.Start(ctx)` 建立 WebSocket 连接并保持（阻塞）

### 2. 事件处理 (`pkg/dingtalk/stream.go:52-87`)

```go
// handleEvent 处理钉钉事件
func (s *StreamClient) handleEvent(ctx context.Context, df *payload.DataFrame) (*payload.DataFrameResponse, error) {
    // 解析事件数据
    var evt bpmsEvent
    if err := json.Unmarshal([]byte(df.Data), &evt); err != nil {
        s.logger.Warnw("解析事件数据失败", "data", df.Data, "err", err)
        return payload.NewSuccessDataFrameResponse(), nil
    }

    // 提取事件信息（兼容不同字段命名）
    eventType := firstNonEmpty(evt.EventType, evt.EventType2)
    corpID := firstNonEmpty(evt.CorpID, evt.CorpID2)
    processInstanceID := firstNonEmpty(evt.ProcessInstanceID, evt.ProcessInstanceID2)

    // 处理审批事件
    if s.onBpmsEvent != nil && processInstanceID != "" && corpID != "" {
        if eventType == "bpms_instance_change" || eventType == "bpms_task_change" {
            go func() {
                if err := s.onBpmsEvent(context.Background(), corpID, processInstanceID, eventType); err != nil {
                    s.logger.Errorw("处理审批事件失败", ...)
                }
            }()
        }
    }

    // 返回成功响应，告知钉钉已收到
    return payload.NewSuccessDataFrameResponse(), nil
}
```

**关键点：**
- `payload.DataFrame` 是钉钉推送的数据帧
- `payload.NewSuccessDataFrameResponse()` 返回成功响应，钉钉收到后不会重试
- 使用 goroutine 异步处理业务逻辑，避免阻塞事件接收

### 3. 服务启动入口 (`internal/app/app.go:27-29`)

```go
// 启动钉钉 Stream 客户端（如果启用）
if global.AppConfig.DingTalk.StreamMode {
    go startDingTalkStream()
}
```

### 4. Stream 启动函数 (`internal/app/app.go:84-114`)

```go
func startDingTalkStream() {
    cfg := global.AppConfig.DingTalk
    if cfg.AppKey == "" || cfg.AppSecret == "" {
        global.Log.Warn("钉钉 Stream 模式未配置 AppKey/AppSecret，跳过启动")
        return
    }

    // 创建依赖
    repo := repository.NewRepository(global.DB)
    dingMgr := service.NewDingTalkClientManager(repo.TenantRepo)
    leaveSyncSrv := service.NewLeaveSyncService(repo.LeaveRepo, repo.UserRepo, dingMgr, global.Log)

    // 创建 Stream 客户端
    streamClient := dingtalk.NewStreamClient(cfg.AppKey, cfg.AppSecret, global.Log)

    // 设置审批事件处理器
    streamClient.SetBpmsEventHandler(func(ctx context.Context, corpID, processInstanceID, eventType string) error {
        return leaveSyncSrv.SyncProcessInstance(ctx, corpID, processInstanceID)
    })

    // 启动（阻塞）
    if err := streamClient.Start(context.Background()); err != nil {
        global.Log.Errorw("钉钉 Stream 客户端启动失败", "err", err)
    }
}
```

## 配置说明

### configs/dev.yaml

```yaml
dingtalk:
  app_key: "your_app_key"
  app_secret: "your_app_secret"
  agent_id: "your_agent_id"
  stream_mode: true  # 启用 Stream 模式
```

### config/config.go

```go
type DingTalk struct {
    AppKey     string `mapstructure:"app_key" yaml:"app_key"`
    AppSecret  string `mapstructure:"app_secret" yaml:"app_secret"`
    AgentID    string `mapstructure:"agent_id" yaml:"agent_id"`
    StreamMode bool   `mapstructure:"stream_mode" yaml:"stream_mode"`
    // ...
}
```

## 开放平台配置步骤

1. 登录 [钉钉开发者后台](https://open-dev.dingtalk.com)
2. 进入应用 → 开发配置 → 事件订阅
3. 选择「Stream 模式推送」
4. **先启动你的服务**（确保日志出现 "钉钉 Stream 客户端启动中..."）
5. 点击「已完成接入」验证连接
6. 验证通过后点击「保存」
7. 添加需要订阅的事件（如 `bpms_instance_change`）

## 日志示例

### 启动成功

```json
{"level":"info","msg":"钉钉 Stream 客户端启动中...","appKey":"dingxxxxx"}
```

### 收到事件

```json
{"level":"info","msg":"收到钉钉事件","eventType":"bpms_instance_change","corpId":"dingxxxxx","processInstanceId":"xxxxx"}
```

### 处理事件

```json
{"level":"info","msg":"处理审批事件","corpId":"dingxxxxx","processInstanceId":"xxxxx","eventType":"bpms_instance_change"}
```

## 与 HTTP 回调模式对比

| 特性 | Stream 模式 | HTTP 回调模式 |
|------|------------|--------------|
| 公网 IP | 不需要 | 需要 |
| 域名 | 不需要 | 需要 |
| 连接方向 | 服务端主动连接钉钉 | 钉钉主动请求服务端 |
| 防火墙 | 只需出站规则 | 需要入站规则 |
| 适用场景 | 内网/本地开发 | 生产环境 |
| 实现复杂度 | 简单 | 需要处理签名验证 |

## 常见问题

### Q: 验证连接失败？

1. 确保服务已启动且日志显示 "钉钉 Stream 客户端启动中..."
2. 检查 AppKey/AppSecret 是否正确
3. 确保应用已发布上线
4. 检查网络是否能访问钉钉服务器

### Q: 收不到事件？

1. 确认已在开放平台添加事件订阅
2. 检查事件类型是否正确（如 `bpms_instance_change`）
3. 查看日志是否有连接错误

### Q: 连接断开后会自动重连吗？

SDK 内部实现了自动重连机制，断开后会自动尝试重新建立连接。
