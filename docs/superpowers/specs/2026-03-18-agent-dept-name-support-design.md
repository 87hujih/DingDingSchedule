# Agent 查询工具支持 `dept_name` 设计

## 背景

当前 agent 已经暴露了支持按部门过滤的考勤查询和统计查询工具，但公开查询工具只接受 `dept_id`。
实际对话中，用户通常直接输入自然语言部门名称，而 LLM 又无法稳定地将部门名称转换为部门 ID，因为 `list_departments` 目前仅对管理员开放。
这会导致 agent 回退到不带部门过滤的查询，最终返回整个租户范围的考勤结果。

## 目标

让当前所有支持部门过滤的 agent 查询工具都可以直接接受 `dept_name`，同时保持对 `dept_id` 的兼容。

本次改动涉及以下工具：

- `query_attendance_status`
- `generate_attendance_text`
- `query_attendance_stats`
- `query_user_cross`

## 非目标

- 不修改 service 或 repository 现有的部门过滤行为。
- 不将 `list_departments` 改为公开工具。
- 不引入模糊匹配、语义匹配或近似匹配。
- 不修改管理员工具中多部门订阅的现有行为。

## 推荐方案

保持现有下游接口仍然基于单个 `dept_id` 工作，只在工具层增加一个解析步骤，将 `dept_name` 转换为最终的单个部门 ID，再继续走原有服务链路。

每个受影响工具同时接受：

- `dept_name`：推荐字段
- `dept_id`：兼容保留字段

解析规则如下：

1. 如果 `dept_name` 在去除首尾空白后非空，则按精确部门名称匹配。
2. 如果 `dept_name` 解析成功，则使用对应部门 ID，并忽略 `dept_id`。
3. 如果 `dept_name` 为空但传入了 `dept_id`，则保持现有行为不变。
4. 如果两者都为空，则保持现有“不按部门过滤”的行为。

## 工具层设计

### 共享解析器

在 `internal/agent/tools` 下新增一个共享 helper，职责仅为将工具层输入解析为最终部门 ID。

该 helper 依赖 `DeptPort`，并返回：

- 最终解析出的部门 ID
- 是否启用部门过滤
- 面向用户的 JSON 错误结果，用于处理可预期的参数错误

helper 需要处理的可预期校验失败包括：

- 部门名称不存在
- 当前租户内存在重名部门

这些情况应返回业务风格的 JSON 结果，而不是直接抛出 Go error。这样 agent 可以向用户返回明确原因，而不是笼统的“工具执行失败”。

### 需要调整的注册函数

以下注册函数需要补充 `DeptPort` 依赖：

- `RegisterAttendanceTools`
- `RegisterAnalyticsTools`

`agent.NewAgent` 中继续复用现有的 `deps.Dept`，并将其传入以上两个注册函数。

## 参数 Schema 变更

对上述 4 个工具统一做以下调整：

- 在 JSON Schema 中新增 `dept_name`
- 保留 `dept_id`
- 更新参数描述，明确 `dept_name` 为首选，`dept_id` 为兼容保留字段

当 `dept_name` 和 `dept_id` 同时传入时，工具行为以 `dept_name` 为准。

## 错误处理

部门名称匹配采用 `strings.TrimSpace` 后的精确匹配。

具体规则：

- 空字符串 `dept_name` 视为未传
- 不做模糊匹配
- `dept_name` 不存在时，返回“未找到部门”
- `dept_name` 命中多个部门时，返回“部门名称不唯一，请改用 `dept_id`”

这样可以保证第一版行为稳定可预测，避免出现例如 `学生` 错误匹配到 `学生会` 的情况。

## 测试策略

采用 TDD。

### 解析器测试

增加针对 helper 的单元测试，覆盖：

- 通过 `dept_name` 正确解析
- `dept_name` 优先于 `dept_id`
- 回退到 `dept_id`
- 两者都为空时不启用过滤
- 部门名称不存在
- 部门名称重名

### 工具回归测试

为 4 个受影响工具补充测试，验证：

- 传入 `dept_name` 时，下游实际收到正确的 `DeptID`
- `dept_name` 无效时，不会调用下游考勤或统计 port
- 旧的 `dept_id` 调用方式继续保持兼容

## 风险

- 现有租户下可能存在重名的叶子部门。
  在这种情况下，新行为应当失败关闭，并提示用户改用 `dept_id`。
- 如果工具描述没有明确优先推荐 `dept_name`，LLM 仍可能继续使用不带过滤的调用方式。

## 验证计划

至少执行以下命令：

- `go test ./internal/agent/tools -run Dept -v`
- `go test ./internal/agent/tools -v`
- `go test ./internal/agent/...`

如果测试名称或包范围与最终实现不完全一致，则执行等价的定向命令，确保覆盖新的解析 helper 以及上述 4 个工具。
