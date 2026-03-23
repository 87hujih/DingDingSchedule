# 任务清单

## 当前任务
- [x] 明确“上一节正常打卡 + 上一节迟到都可顺延到本节”的精确业务边界。
- [x] 将实施步骤写入 `docs/superpowers/plans/2026-03-23-attendance-carry-forward-late-plan.md`。
- [ ] 先补打卡顺延回归测试，锁定上一节迟到用户在现有顺延条件下会进入本节 `on_time`。
- [ ] 以最小改动调整 `applyCarryForward`，保持其它统计口径与优先级不变。
- [ ] 运行定向测试并补充本次复盘。

## 当前任务复盘
- [x] 梳理当前项目中“打卡统计”的入口接口、定时任务和核心 service。
- [x] 追踪统计口径：应到、已到、迟到、未到、请假、休息日、有课的计算来源与优先级。
- [x] 结合实时视图、最终快照和分析统计代码，输出当前实现的计算流程说明。

## 当前任务复盘
- 已静态追踪打卡统计主链路：管理端路由入口在 `internal/app/routers_attendance.go`，核心计算集中在 `internal/service/attendance_record_service.go`，自动触发由 `internal/scheduler/attendance_scheduler.go` 调度，历史聚合分析在 `internal/service/attendance_analytics.go`。
- 当前实现是“两阶段”统计：上课后按 `TriggerDelayMinutes` 触发一次初步统计并落库/推送；固定在上课后 30 分钟再次 finalization，重新计算并 upsert 同一条 `attendance_records` 记录，把 `late` 与最终 `not_arrived` 拆开。
- 实时 `/attendance/record/detail` 在 finalization 前不会直接读库，而是按当前时间重新拉钉钉打卡记录计算；finalization 后才读 `attendance_records` 快照。相对地，周排行、Agent `QueryStats`、周出勤率排行都直接基于数据库快照聚合。
- 统计口径已确认：候选人 = 启用用户且命中启用部门；正常学期模式下 `should_attend = 候选人 - has_course - rest_day`，请假用户仍保留在应到口径里再单独归入 `leave`；假期/无学期/超学期范围则切到“全体应到”模式；分类优先级是 `rest_day > leave > has_course > should_attend`。
- 打卡判定已确认：查询窗口从“第 1 节当天 00:00 / 其余节次上一节下课时间”开始，到当前时刻/节次结束/finalize 时刻三者最小值结束；同一用户只取最早 `OnDuty` 打卡，`<= lateDeadline` 算 `on_time`，之后算 `late`；连续节次且间隔不超过 `MaxCarryForwardGapMinutes` 时会把上一节正常打卡顺延到本节。
- 实际验证命令：`go test ./internal/service -run 'Test(GetAttendanceDetailReturnsCurrentViewBeforeFinalize|GetAttendanceDetailReturnsFinalSnapshotAfterFinalize|FinalizeAttendanceRecordPersistsLateAndNotArrived|AttendanceDetailPrioritizesRestDayAndLeaveOverHasCourse|SlotAttendanceStatusPrioritizesRestDayAndLeaveOverHasCourse)' -v` 与 `go test ./internal/service -run 'Test(QueryStats|GetWeeklyRanking|GetWeeklyAttendanceRateRanking)' -v`，结果通过。
- [x] 追踪 `/api/admin/attendance/record/detail` 中 `avatar`、`dept_name` 的来源与赋值链路。
- [x] 确认字段为空是用户信息未加载、部门信息未聚合，还是 DTO 映射/过滤导致。
- [x] 如存在缺陷，补最小回归测试并以最小改动修复。
- [x] 运行定向验证并补充本次复盘。

## 当前任务复盘
- 已确认问题出在实时详情链路，而不是 handler 或 JSON 标签：`AttendanceRecordService` 的实时分支会构造 `AttendanceDetailResponse`，但 `getOnTimeUsers`、`getLeaveUsers`、`calculateNotArrivedWithLate`、`toRestDayBasicList` 和 `NewAttendanceDetailResponse` 原先只写了 `ID/Name`，导致 `avatar`、`dept_name` 为空。
- 在 `internal/service/attendance_record_service.go` 新增统一的 `enrichAttendanceDetailUsers`，在实时详情与手动触发统计的响应构造完成后，基于当前候选用户一次性回填 `should_attend`、`on_time`、`late`、`leave`、`not_arrived`、`rest_day`、`has_course` 的头像与部门名。
- 在 `internal/service/attendance_record_service_test.go` 新增 `TestGetAttendanceDetailRealtimePopulatesAvatarAndDeptName`，先复现实时 `/detail` 用户资料字段为空，再验证修复结果；同时补跑实时/最终视图、人工覆盖和去重相关定向测试。
- 验证命令：`$env:GOCACHE = (Resolve-Path .gocache).Path; go test ./internal/service -run TestGetAttendanceDetailRealtimePopulatesAvatarAndDeptName -v`；`$env:GOCACHE = (Resolve-Path .gocache).Path; go test ./internal/service -run 'Test(GetAttendanceDetailReturnsCurrentViewBeforeFinalize|GetAttendanceDetailReturnsFinalSnapshotAfterFinalize|GetAttendanceDetailRealtimePopulatesAvatarAndDeptName|FinalizeAttendanceRecordKeepsManualOverrideOverLatePunch|SignForUsersSupportsRealtimeDateSectionAndDetailShowsOverride|AttendanceDetailPrioritizesRestDayAndLeaveOverHasCourse|GetAttendanceDetailDeduplicatesMultiplePunchesFromSameUser)' -v`。
## 当前任务
- [x] 盘点项目入口、核心模块、配置文件、开发命令和部署链路，提炼 README 所需事实。
- [x] 设计适合 GitHub 首页展示的 README 结构，覆盖项目简介、能力、技术栈、快速开始和部署说明。
- [x] 生成根目录 README.md，并基于实际代码与脚本校对内容。

## 当前任务复盘
- 已新增根目录 `README.md`，将项目定位收敛为“多租户排课与考勤后端 + 钉钉 Stream + AI Agent”，避免继续沿用缺失或漂移的旧说明。
- README 当前覆盖了核心能力、技术栈、系统组成、目录结构、本地运行、开发命令、正式部署、应急部署和安全提示，内容均对照 `cmd/main.go`、`inits/*`、`internal/app/*`、`internal/agent/*`、`docker-compose*.yml`、`.github/workflows/*` 与部署脚本核验。
- 本次未修改业务代码；核验方式为回读 `README.md`、执行 `git diff -- README.md task_plan.md findings.md progress.md tasks/todo.md`、`git status --short README.md task_plan.md findings.md progress.md`、`rg -n "CONFIG_ENV|/health|one-click-deploy.sh|docker-compose.prod.yml|dingtalk.stream_mode|tenant_id|AutoMigrate" README.md`，确认 README 已落盘且关键表述可映射到当前实现。
## 当前任务
- [x] 审读 GitHub Actions 工作流、部署脚本和部署文档，提取 CI/CD 主链路。
- [x] 审读 Docker/配置文件，确认服务在服务器上的目录、配置注入和启动方式。
- [x] 审读仓库中与 GitHub Secrets、远程主机登录和回滚相关的约定，梳理 GitHub 与服务器如何配合。
- [x] 输出一份面向当前仓库实际实现的详细总结，并在任务清单中补充复盘。

## 当前任务复盘
- 当前仓库实际存在两条部署链路：正式链路是 `.github/workflows/deploy.yml` + GHCR + `deploy.sh` + `docker-compose.prod.yml`，服务器目录统一约定为 `/opt/schedule_server`；应急链路则是 `one-click-deploy.sh` + `pack-for-deploy.sh` + `deploy-legacy.sh`，会上传源码包并在服务器本地 `docker build`。
- CI 与 CD 在 GitHub Actions 中是分离的：`.github/workflows/ci.yml` 负责 PR / 非 master push 的测试、lint、构建与 Docker 构建校验；`.github/workflows/deploy.yml` 负责 master push / 手动触发后的镜像构建、GHCR 推送、SSH 同步部署资产、远程执行部署脚本与服务器本地健康检查。
- 服务配置的关键注入点是 `.env.prod` 与 `configs/prod.yaml`：`docker-compose.prod.yml` 把宿主机 `${CONFIG_DIR}` 挂到容器 `/app/configs`，应用启动时通过 `CONFIG_ENV` + `CONFIG_PATH` 用 Viper 加载配置；HTTP 服务监听 `26665`，`/health` 返回 `{\"status\":\"ok\"}`。
- GitHub 侧在仓库中可见的要求主要是 Secrets 约定：`SERVER_HOST`、`SERVER_USER`、`SERVER_SSH_KEY`/`SERVER_PASSWORD`、可选 `SERVER_SSH_PASSPHRASE`，以及私有 GHCR 场景下的 `GHCR_USERNAME` / `GHCR_TOKEN`；镜像推送使用 GitHub 自带 `GITHUB_TOKEN`。
- 审读中确认存在文档漂移：`docs/deployment/production.md` 把健康检查写成外部地址，但 workflow 实际通过 SSH 在目标机上 `curl http://localhost:26665/health`；`docs/deployment/emergency.md` 把 `one-click-deploy.sh` 描述成镜像拉取式应急发布，但脚本实际已恢复为源码包上传后服务器本地构建。
- 额外风险点是 `configs/prod.yaml` 当前被纳入仓库并包含真实生产敏感配置；这说明当前生产配置并未完全做到“只在服务器注入”，后续如果继续规范化部署，优先级最高的改进应是把这类密钥迁出仓库。
- 本次核验方式为静态审读 `.github/workflows/*`、`deploy.sh`、`docker-compose.prod.yml`、`one-click-deploy.sh`、`pack-for-deploy.sh`、`deploy-legacy.sh`、`inits/config.go`、`internal/app/*`、`configs/prod.yaml`、`docs/deployment/*`，并执行 `go test ./internal/ci -v`，结果通过。

## 当前任务
- [x] 审读最新健康检查失败日志，确认失败发生在 workflow 最后一步从 GitHub Runner 直接访问服务地址，而不是部署脚本阶段。
- [ ] 对比当前 deploy workflow 与 `one-click-deploy.sh` 的健康检查路径，确认外部直连与服务器本地检查的差异。
- [ ] 先补 workflow 回归测试，锁定正式部署应通过 SSH 在目标机本地检查 `http://localhost:26665/health`。
- [ ] 以最小改动修正 deploy workflow 的健康检查链路，并完成验证与复盘。

## 当前任务
- [x] 审读最新部署日志，确认 GHCR 拉取已恢复，新的阻塞点变为 `compose up` 与已有 `schedule-server` 容器命名冲突。
- [x] 对比当前 compose/legacy 部署链路，确认旧容器为何不会被当前生产部署脚本接管或清理。
- [x] 给出 2-3 个修复方案并选定最小 blast radius 的落地方式。
- [x] 在用户确认方案后，通过测试约束部署脚本行为并实施修复。

## 当前任务复盘
- 最新部署日志显示，`2026-03-20 21:06:17 +08:00` 时镜像已经成功拉取完成，新的直接失败点是 `docker compose up -d` 创建容器时报 `Conflict. The container name "/schedule-server" is already in use`。
- 根因是旧源码部署链路和新 compose 链路都固定使用 `schedule-server` 作为容器名：旧链路会在 `docker run --name schedule-server` 前手动删旧容器，但当前 `deploy.sh` 在迁移到 compose 时只做 `compose pull + compose up -d`，没有补上这一步，所以第一次从旧链路切换到 compose 必然撞名。
- 在 `internal/ci/deploy_workflow_test.go` 新增回归测试，锁定部署脚本需要提供 `remove_conflicting_container`，并要求 `deploy_stack` 在 `compose up -d` 前先执行该清理逻辑。
- 更新 `deploy.sh`，在生产部署路径中通过 `docker inspect` 检测已有 `schedule-server` 容器；若存在则先 `docker stop` / `docker rm`，再交由 compose 重新创建并接管。
- 验证方式为 `go test ./internal/ci -run TestDeployScriptRemovesConflictingLegacyContainerBeforeComposeUp -v`（先红后绿）以及 `go test ./internal/ci -v`，均已通过。

## 当前任务
- [x] 审读本次 GitHub Actions 失败日志，确认错误发生在目标机执行 `docker compose pull` 访问 `ghcr.io/token` 阶段。
- [x] 先补部署脚本/工作流回归测试，锁定 GHCR 拉取失败时需要有显式重试与排障输出。
- [x] 以最小改动加固生产部署脚本，降低瞬时 GHCR 网络抖动导致整次发版失败的概率。
- [x] 运行定向验证并补充本次复盘记录。

## 当前任务复盘
- GitHub Actions 已成功 SSH 到目标机，真正失败点在远程 `./deploy.sh deploy` 的 `docker compose pull`；Docker daemon 访问 `https://ghcr.io/token?...` 时发生 `Client.Timeout exceeded while awaiting headers`，说明这次不是镜像构建失败，也不是 SSH 认证失败，而是目标机到 GHCR 的出站网络/注册表访问抖动。
- 现有 `deploy.sh` 在生产部署时只执行一次 `compose pull`，因此任何瞬时 GHCR 超时都会直接让整次发版失败，缺少针对外部仓库抖动的最小容错。
- 在 `internal/ci/deploy_workflow_test.go` 新增回归测试，锁定部署脚本需要暴露 `IMAGE_PULL_RETRIES`、`IMAGE_PULL_RETRY_DELAY_SECONDS`，并要求 `deploy_stack` 通过 `retry_compose_pull` 包装镜像拉取，而不是直接裸调 `compose pull`。
- 更新 `deploy.sh`，为生产镜像拉取新增默认 `3` 次重试和 `15` 秒退避；最终失败时输出明确排障方向，提示优先检查服务器到 `ghcr.io` 的网络连通性、GHCR 凭据和镜像 tag。
- 验证方式为 `go test ./internal/ci -run TestDeployScriptRetriesImagePullsBeforeFailing -v`（先红后绿）以及 `go test ./internal/ci -v`，均已通过。

## 当前任务
- [x] 确认一键部署失败的直接原因是新链路依赖 docker compose，与原源码部署方式不兼容。
- [x] 先用回归测试锁定 one-click-deploy.sh、pack-for-deploy.sh 和旧版部署脚本应恢复源码打包部署流程。
- [x] 以最小 blast radius 恢复一键部署的旧流程，并完成验证与复盘。

## 当前任务复盘
- 最新失败不是 SSH 或镜像问题，而是 one-click-deploy.sh 当前会把仓库根目录的新版 deploy.sh 上传到服务器执行；该脚本已改成依赖 docker compose 和镜像拉取，所以在未安装 compose 插件的服务器上直接失败。
- 新增 deploy-legacy.sh，恢复“服务器本地 docker build + docker run”的旧部署行为，同时补上当前代码所需的 configs 挂载，避免直接照抄历史脚本后因配置目录缺失再次失败。
- 更新 pack-for-deploy.sh，重新生成 schedule_server_deploy.tar.gz 源码部署包，并把 deploy-legacy.sh 作为包内 deploy.sh；更新 one-click-deploy.sh，恢复为“本地打包 -> 上传压缩包 -> 远程解压 -> 归一化换行 -> 执行包内 deploy.sh”的旧链路；同步调整 deploy-to-server.bat 不再传镜像 tag。
- 验证方式为 go test ./internal/ci -run "TestOneClickDeployUsesSourceBundleFlow|TestPackForDeployBuildsSourceBundle|TestLegacyDeployScriptBuildsAndRunsContainerWithoutCompose" -v 与 go test ./internal/ci -v，均已通过。
## 当前任务
- [x] 复现并定位 ./one-click-deploy.sh 远程执行 deploy.sh 失败的根因。
- [x] 先补一个失败回归测试，锁定 Windows CRLF 上传后不会再让远程部署命令直接炸掉。
- [x] 以最小改动修复 one-click-deploy.sh 的远程执行链路，并完成验证与复盘。

## 当前任务复盘
- 通过检查 deploy.sh 文件头十六进制内容，确认其首行是 #!/bin/bash\r\n；该文件经 scp 上传到 Linux 后直接执行，会因为 shebang 携带 \r 而报 cannot execute: required file not found。
- 在 internal/ci/one_click_deploy_test.go 新增了回归测试，锁定 one-click-deploy.sh 的远程部署命令必须先对 deploy.sh、docker-compose.prod.yml 和 .env.prod.example 做 CRLF -> LF 归一化。
- 更新 one-click-deploy.sh，在远程执行 chmod +x deploy.sh 之前增加 sed -i 's/\r$//' 归一化步骤，保持现有部署流程不变。
- 验证方式为 go test ./internal/ci -run TestOneClickDeployNormalizesUploadedScriptsBeforeExecution -v 与 go test ./internal/ci -v；尝试执行 ash -n one-click-deploy.sh 进一步做语法校验，但当前环境只有 C:\Windows\System32\bash.exe（WSL 桥接），调用时返回 Bash/Service/CreateInstance/E_ACCESSDENIED，无法在本机完成该项校验。

## 当前任务
- [x] 追溯 ONE_CLICK_DEPLOY_GUIDE.md 需要恢复的“原来的部署方法”，确认对应的旧脚本和 git 证据。
- [x] 根据确认后的旧流程改写 ONE_CLICK_DEPLOY_GUIDE.md，恢复为原部署方式说明。
- [x] 复查文档与当前仓库状态的关系，完成核验并补充本次复盘。

## 当前任务复盘
- 通过 git show d27712e^:pack-for-deploy.sh、git show d27712e^:deploy.sh、git show d27712e^:docker-compose.yml 以及仓库内现存的 schedule_server_deploy.tar.gz，确认原部署方法是“本地准备源码部署包，上传服务器后执行 ./deploy.sh deploy 在服务器本地构建并启动容器”。
- ONE_CLICK_DEPLOY_GUIDE.md 已从“GitHub Actions + GHCR 镜像部署”改回上述旧流程，并补充了源码包结构、上传/解压/部署/回滚步骤。
- 为避免误导，文档额外注明：本文恢复的是旧源码部署包里的 deploy.sh 行为，而当前仓库根目录的 deploy.sh 已切换为拉取镜像流程。
- 本次核验方式为审读文档正文、执行 git diff -- ONE_CLICK_DEPLOY_GUIDE.md，并用 
g -n "schedule_server_deploy|docker build|docker run|GitHub Actions|GHCR|./deploy.sh deploy" ONE_CLICK_DEPLOY_GUIDE.md 确认旧流程关键词已经恢复。

## 当前任务
- [x] 阅读 README.md（若不存在则定位等价说明文件）、PROJECT_FOR_RESUME.md、MY_EVIDENCE_MAP.md、RESUME_TARGET.md 和主要代码目录。
- [x] 提炼项目整体价值、个人独立完成的关键工作、Golang 后端能力、AI 应用落地能力和工程实现能力。
- [x] 输出适合直接写进实习简历的项目经历内容，并记录本次整理的证据来源与核验方式。

## 当前任务复盘
- 仓库根目录未发现 README.md，本次整理实际依据为 PROJECT_FOR_RESUME.md、MY_EVIDENCE_MAP.md、RESUME_TARGET.md，以及 cmd/main.go、internal/app、internal/service、internal/repository、internal/agent、internal/scheduler、pkg/dingtalk、pkg/scheduleparse、configs 和 .github/workflows。
- 代码证据显示该项目已形成“钉钉登录/同步 + 课表导入与查询 + 自动考勤快照 + 请假审批同步 + 群推送 + AI 自然语言查询”的完整闭环，适合按个人独立开发的完整后端工程项目来表述。
- 本次未修改业务代码；核验方式为静态审读上述文档与源码，并补看 git log --oneline -10，确认近期提交集中在 agent、考勤能力与 CI/CD 建设。
## 当前任务
- [x] 固化“考勤开始后 30 分钟内实时、30 分钟后最终结算入库”的业务规则。
- [x] 明确管理端、agent、周排行/统计分析和调度器的职责边界。
- [x] 将方案写入 `docs/superpowers/specs/2026-03-19-attendance-realtime-finalization-design.md`。
- [x] 用户已审阅并确认设计文档，允许继续推进。
- [x] 将实施步骤写入 `docs/superpowers/plans/2026-03-19-attendance-realtime-finalization-plan.md`。
- [ ] 等待开始按计划实施。

## 当前任务
- [x] 梳理考勤模块的状态判定、定时任务和消息生成链路。
- [x] 结合“固定考勤点”现状，分析迟到与未到当前为何被合并为“未到”。
- [x] 提出 2-3 种可落地的区分方案，给出推荐方案与落地注意点。
- [x] 输出面向当前代码结构的详细分析与后续建议。

## 当前任务复盘
- 审读了 `internal/service/attendance_record_service.go`、`internal/scheduler/attendance_scheduler.go`、`internal/dto/attendance_record.go`、`internal/service/attendance_analytics.go`、`internal/repository/attendance_record_repository.go`、`internal/handler/attendance_record_handler.go`、`internal/model/attendance_record.go` 和 `pkg/dingtalk/attendance.go`，确认考勤快照由定时任务在固定延迟点生成，并落库为单条 `attendance_records` 聚合记录。
- 代码显示系统在运行时其实已经能识别“迟到打卡”的原始事实：`getOnTimeUsers` 会把晚于 `lateDeadline` 的打卡收集到 `lateUsers`，但上层 `getAttendanceDetailWithLateUsers` 当前直接丢弃了这部分结果，只保留“未正常打卡”的聚合名单进入 `NotArrived`。
- 当前语义已经出现漂移：数据结构把 `NotArrived` 定义成“迟到和缺勤合并”，消息文本却把它展示成“迟到”，而统计分析又把它累计为 `absent_count`，说明这不是单点展示问题，而是整个考勤快照口径需要重新拆分。
- 结合 `configs/prod.yaml` 中 `late_grace_minutes=1`、`trigger_delay_minutes=2` 的现状，推荐采用“两阶段判定”思路：第一次推送区分 `late` 与 `pending_absent`，第二次在节次结束或确认窗口结束后把 `pending_absent` 结算成最终 `absent`，这样才能兼顾管理员及时跟进和最终统计口径准确。
- 本次未改业务代码；验证方式为静态审读关键服务、仓储、DTO、调度与钉钉接入实现，并交叉确认配置项和统计口径的实际使用位置。

## 本次部署标准化实施任务
- [x] 将正式部署流程统一到 GitHub Actions + GHCR 镜像制品链路。
- [x] 收敛本地/服务器部署脚本，保留 `one-click-deploy.sh` 与 `deploy-to-server.bat` 作为应急入口。
- [x] 补充生产部署与应急回滚文档，并让旧指南明确退居辅助角色。
- [x] 在主工作区完成构建/测试验证，记录无法本地完成的校验项。

## 本次部署标准化实施复盘
- 新增 `.github/workflows/ci.yml`，让 PR / 普通 push 具备 `go test`、构建和 Docker 构建校验；同时把 `.github/workflows/deploy.yml` 改为 GHCR 镜像制品部署，不再上传源码包到服务器现场重建。
- 将 `scripts/migrate_holiday_periods.go`、`scripts/migrate_schedule_periods.go` 和 `scripts/reset_admin_password.go` 拆到各自目录下的 `main.go`，消除了 `go test ./...` 被多个 `main` 阻塞的问题。
- 引入 `docker-compose.prod.yml`、`.env.prod.example`、`CONFIG_PATH` 支持以及新的 `deploy.sh` 生产约定，统一生产目录为 `/opt/schedule_server`，并在部署前前置校验 `.env.prod` 与 `configs/prod.yaml`。
- 重写 `one-click-deploy.sh`、`deploy-to-server.bat`、`pack-for-deploy.sh`、`ONE_CLICK_DEPLOY_GUIDE.md`、`XSHELL_DEPLOY_COMPLETE_GUIDE.md`，将它们降级为应急/手工镜像部署入口，不再延续“源码打包上传 + 服务器重建”流程。
- 新增 `docs/deployment/production.md` 与 `docs/deployment/emergency.md`，补齐正式发布、手工回滚和常用排查命令；同时调整 `.gitignore`，允许这些正式部署文档和应急脚本作为仓库资产被跟踪。
- 已在主工作区通过 `go test ./...` 和 `go build -o bin/schedule_server ./cmd/main.go` 验证；`golangci-lint` 仍因仓库历史 lint 债务失败，所以 CI 中继续使用 `only-new-issues: true`；本机 Docker daemon 不可用，且 `bash` 指向异常的 WSL，无法在当前机器完成容器运行验证与 shell 语法校验。

## 本次 skill 安装任务
- [x] 确认 `find-skills` 的 GitHub 来源、安装脚本入口和目标路径。
- [x] 使用 `skill-installer` 脚本从 `vercel-labs/skills` 安装 `find-skills`。
- [x] 验证本地 skill 目录已生成对应文件，并补充复盘记录。

## 本次部署标准化规划任务
- [x] 基于当前仓库的 CI/CD、脚本和部署文档现状，明确标准化改造目标。
- [x] 将改造拆分为可独立落地的阶段，并为每个阶段指定文件边界与验收方式。
- [x] 将实施计划写入 `docs/superpowers/plans/2026-03-19-cicd-deployment-standardization-plan.md`。
- [x] 更新 `tasks/` 与项目规划文件，记录本次规划结论。

## 本次部署标准化规划复盘
- 输出了 `docs/superpowers/plans/2026-03-19-cicd-deployment-standardization-plan.md`，把本仓库从“本地一键脚本部署”升级到“标准 CI + 镜像制品 CD + 生产专用部署资产”的改造路径拆成 6 个任务。
- 计划明确了先修 `scripts` 目录结构以恢复 `go test ./...`，再新增 `ci.yml`、改造 `deploy.yml`、引入 `docker-compose.prod.yml`、统一 `/opt/schedule_server` 与 `CONFIG_ENV=prod` 约定，最后再收敛文档和应急流程。
- 计划同时保留 `one-click-deploy.sh` 作为应急路径，但将其从正式日常发布链路中降级，避免继续把“源码打包上传 + 服务器重建”当作标准部署方式。

## 本次 skill 安装任务复盘
- 通过 `C:\Users\mhn\.codex\skills\.system\skill-installer\scripts\install-skill-from-github.py` 从 `vercel-labs/skills` 的 `skills/find-skills` 路径安装了 `find-skills`。
- 安装结果落在 `C:\Users\mhn\.codex\skills\find-skills`，并已核对其中存在 `SKILL.md`。
- `SKILL.md` 已确认该 skill 用于帮助用户查找、筛选和安装其他可复用 skills。

## 当前任务
- [x] 确认“按部门查无课”应走方案 A：在现有 `query_free_users_by_slot` 上增加部门过滤。
- [x] 先通过 TDD 补工具层和 service 层失败回归测试。
- [x] 以最小改动让 `query_free_users_by_slot` 支持 `dept_name/dept_id` 并将 `dept_id` 透传到 `ScheduleService`。
- [x] 运行定向测试与包级验证，并记录结果。

## 当前任务复盘
- 在 `docs/superpowers/specs/2026-03-19-agent-free-slot-dept-filter-design.md` 与 `docs/superpowers/plans/2026-03-19-agent-free-slot-dept-filter-plan.md` 中记录了本次最小设计与实施计划。
- 更新了 `internal/agent/tools/schedule.go`、`internal/agent/tools/types.go`、`internal/agent/agent.go`、`internal/app/agent_wiring.go` 和 `internal/service/schedule_service.go`，让 `query_free_users_by_slot` 支持 `dept_name/dept_id`，并将单个 `dept_id` 透传到候选人筛选阶段。
- 在 `internal/agent/tools/schedule_test.go` 中新增了 schema 暴露、`dept_name` 解析、非法部门短路和 `dept_id` 兼容回归测试；在 `internal/service/schedule_service_test.go` 中新增了 service 层按部门过滤无课人员的回归测试。
- 通过 `go test ./internal/agent/tools -run TestQueryFreeUsersBySlot -v`、`go test ./internal/service -run TestGetFreeUsersBySlotFiltersByDeptID -v` 以及 `go test ./internal/agent/... ./internal/service/...` 完成验证。

## 本次最新失败调用排查任务
- [x] 查询 `agent_call_logs` 中最近一次失败调用的时间、状态和错误信息。
- [x] 拉取线上容器对应时间窗口日志，对齐消息接收、工具执行和 LLM/上游错误。
- [x] 判断失败发生阶段、直接原因和是否已恢复。
- [x] 输出结论并补充复盘记录。

## 本次最新失败调用排查复盘
- 截至 `2026-03-19 12:49:03`，数据库里最近一次失败的 agent 调用是 `id=194`，发生在 `2026-03-19 12:47:38`，用户问题为“你可以按照部门查询人员的无课情况吗”，状态为 `failed`，`rounds=2`，总耗时 `112303ms`，调用工具为 `list_departments,query_free_users_by_slot`。
- 线上容器日志对齐显示：`12:45:46` 收到群聊消息，`12:45:56` 调用 `list_departments`，`12:46:08` 调用 `query_free_users_by_slot`，`12:47:38` 在 `round=2` 发生 `LLM 调用失败`，错误为 `发送请求失败: Post "https://api.siliconflow.cn/v1/chat/completions": context deadline exceeded`。
- 这说明失败发生在第二次工具调用完成后的最终 LLM 总结阶段，而不是部门查询工具或无课查询工具自身执行失败。
- 与 `2026-03-18` 的失败相比，这次错误文案不再包含 `Client.Timeout exceeded while awaiting headers`，而是纯 `context deadline exceeded`；结合容器本次启动时间推断，线上已经跑到了“由外层 context 控制超时”的新版本。
- 从代码可见，`query_free_users_by_slot` 当前并不支持 `dept_name/dept_id` 过滤，只接受 `week/day_start/day_end`；本次 LLM 先查部门列表，再发起全周范围的无课查询，说明它无法把“按部门”约束直接下推到查询工具。由此推断，最终总结阶段需要处理的工具结果偏大，是本次超时的高概率放大因素，但直接报错点仍然是 SiliconFlow 接口在外层超时窗口内未完成响应。

## 本次 CI/CD 与部署排查任务
- [x] 盘点仓库内现有 CI 工作流、触发条件和执行内容。
- [x] 盘点现有部署脚本、Docker 资源、发布文档和发布入口。
- [x] 判断是否存在“可部署但未自动化”或“自动化缺失关键环节”的断点。
- [x] 输出结论、证据和建议，并补充复盘记录。

## 本次 CI/CD 与部署排查复盘
- 仓库内存在且仅存在一条 GitHub Actions 工作流 `.github/workflows/deploy.yml`，触发条件是 `push master` 和手动触发；该工作流会构建镜像并尝试远程部署，但没有 `pull_request` / 普通 `push` 级别的测试、lint、构建校验，因此当前更接近“自动部署”而不是完整 CI/CD。
- 部署入口并不缺失：仓库包含 `Dockerfile`、`docker-compose.yml`、`deploy.sh`、`one-click-deploy.sh`、`deploy-to-server.bat` 以及多份部署指南，说明已经具备容器化部署和人工/半自动发版能力。
- 现有 CD 链路存在不一致：`deploy.yml` 上传到 `/opt/schedule_server` 且携带 `configs/dev.yaml`，`one-click-deploy.sh` 使用 `/workspace/schedule_server`，`pack-for-deploy.sh` 则只打包 `configs/prod.yaml`；目录、配置和执行路径没有统一。
- `deploy.yml` 先在 GitHub Runner 构建并上传镜像 tar，再在服务器上执行 `./deploy.sh deploy`；而 `deploy.sh deploy` 会再次执行 `docker build .`，导致“预构建镜像”与“服务器本地重建”两套流程叠在一起，自动部署链路不够自洽。
- 为评估引入 CI 的现状，执行了 `go test ./...`；结果在 `scripts` 包失败，原因是多个脚本文件都声明了 `main` 函数，当前仓库并不满足直接把 `go test ./...` 挂进 CI 的条件。

## 当前修复任务
- [x] 明确本次修复范围：只修正 LLM 超时链路对齐，还是同时调整重试/日志策略。
- [x] 通过失败回归测试锁定“工具总结阶段应允许超过 50s 的 HTTP 等待时间”这一行为。
- [x] 以最小改动调整 LLM 客户端超时实现，避免底层 `http.Client.Timeout` 抢先截断外层 `context`。
- [x] 运行定向测试验证修复，并补充复盘记录。

## 当前修复任务复盘
- 在 `docs/superpowers/specs/2026-03-19-agent-llm-timeout-alignment-design.md` 与 `docs/superpowers/plans/2026-03-19-agent-llm-timeout-alignment-plan.md` 中记录了本次最小修复的设计和实施计划。
- 新增 `internal/agent/client_test.go`，先用失败回归测试锁定 `NewLLMClient` 不应再写死 `http.Client.Timeout = 50s`，避免总结阶段被底层 HTTP 客户端提前截断。
- 更新 `internal/agent/client.go`，移除了固定 `50s` 的 `http.Client.Timeout`，改为完全依赖外层 `context` 控制请求生命周期，使 `agent.go` 中的 `50s/90s` 分阶段超时真正生效。
- 通过 `go test ./internal/agent -run TestNewLLMClientUsesContextDrivenTimeout -v` 和 `go test ./internal/agent/...` 完成验证。

## 本次排查任务
- [x] 确认服务日志目录、日志文件命名和最近 24 小时覆盖范围。
- [x] 提取最近 24 小时内 agent 调用失败、超时和上游请求异常日志。
- [x] 对照 agent 调用链路代码，判断失败集中发生在哪个阶段。
- [x] 汇总根因、影响范围和后续建议，并补充复盘记录。

## 本次排查复盘
- 本地 `logs/app.log` 与 `logs/error.log` 不是线上最近一天日志，真实线上服务运行在 `106.52.42.194` 的 `schedule-server` Docker 容器中，需要通过 `ssh root@106.52.42.194 'docker logs ...'` 排查。
- 通过查询 `agent_call_logs` 表确认最近 24 小时共有 13 次 agent 对话，11 次成功、2 次失败，失败时间分别为 `2026-03-18 11:53:14` 和 `2026-03-18 21:16:38`。
- 两次失败的 `error_msg` 完全一致，都是请求 `https://api.siliconflow.cn/v1/chat/completions` 时等待响应头超时；失败记录的 `rounds=1` 且 `tools_called` 非空，说明工具已成功执行，失败发生在工具结果返回后的下一轮 LLM 总结阶段。
- 线上容器日志显示 `2026-03-18 21:15:35` 收到消息、`21:15:48` 成功调用工具、`21:16:38` 发生 `LLM 调用失败`，与数据库记录对齐；`2026-03-18 21:21:56` 到 `21:22:14` 的后续请求又恢复成功，说明更像上游 LLM/网络瞬时波动而非工具或业务逻辑持续性故障。
- 代码中 `internal/agent/agent.go` 试图让“工具总结阶段”使用 `90s` 超时，但 `internal/agent/client.go` 的底层 `http.Client.Timeout` 被硬编码为 `50s`，实际仍会在约 50-60 秒失败，放大了上游抖动的用户可见影响。
- 线上容器在 `2026-03-18 21:02:13` 还出现过 4 个租户同时连接钉钉 Stream 网关超时并进入重试，说明当时服务器对外部 API 的连通性存在抖动，但随后已恢复并能继续接收消息。

## 当前任务
- [x] 为 agent 部门过滤工具新增 `dept_name` 设计文档并完成审阅。
- [x] 为 `query_attendance_status`、`generate_attendance_text`、`query_attendance_stats`、`query_user_cross` 设计统一的 `dept_name` 解析方案。
- [x] 通过 TDD 为 `dept_name` 解析 helper 与 4 个工具补回归测试。
- [x] 以最小改动实现 `dept_name` 优先、`dept_id` 兼容的工具参数解析。
- [x] 运行 `go test ./internal/agent/tools -v` 和 `go test ./internal/agent/...` 完成验证。

## 当前任务复盘
- 在 `internal/agent/tools/dept_resolver.go` 中新增了统一的部门解析 helper，收口 `dept_name` 优先、`dept_id` 兼容、未知/重名部门返回用户可见错误 JSON 的规则。
- 在 `internal/agent/tools/attendance.go` 与 `internal/agent/tools/analytics.go` 中为 4 个查询工具新增 `dept_name` 参数，并保留 `dept_id` 兼容行为。
- 在 `internal/agent/agent.go` 中将 `deps.Dept` 注入考勤工具与统计工具注册，避免重复走管理员专属 `list_departments`。
- 新增了 `internal/agent/tools/dept_resolver_test.go`、`internal/agent/tools/attendance_test.go`、`internal/agent/tools/analytics_test.go`，同时覆盖 schema 暴露和 handler 行为。
- 通过 `go test ./internal/agent/tools -run TestResolveDeptFilter -v`、`go test ./internal/agent/tools -run TestAttendanceTool -v`、`go test ./internal/agent/tools -run TestAnalyticsTool -v`、`go test ./internal/agent/tools -v` 和 `go test ./internal/agent/...` 完成验证。

## 上一个已完成任务
- [x] 梳理所有“考勤候选人”加载入口，明确哪些路径需要同时满足用户状态和部门状态。
- [x] 先编写失败回归测试，锁定“`user.Status == 1` 且至少归属一个 `department.Status == 1` 的部门”这一规则。
- [x] 以最小改动新增考勤专用候选人查询，并让考勤相关入口统一复用。
- [x] 运行定向测试和相关包验证，并记录结果。

## 上一个已完成任务复盘
- 在 `internal/service/attendance_record_service_test.go` 中新增了回归测试，先复现“停用部门用户仍进入候选名单”的问题，再验证修复结果。
- 在 `internal/repository/user_repository.go` 中新增 `ListAttendanceCandidates`，将候选人规则收敛为“用户启用且命中至少一个启用部门”。
- 更新了 `internal/service/attendance_record_service.go`、`internal/service/attendance_service.go`、`internal/service/attendance_analytics.go` 和 `internal/service/schedule_service.go`，统一复用新的考勤候选人查询。
- 通过 `go test ./internal/service -run TestGetShouldAttendUsersExcludesUsersFromDisabledDepartments -v`、`go test ./internal/service/... ./internal/repository/...` 和 `go test ./cmd ./config ./global ./inits ./internal/... ./pkg/...` 完成验证。
- `go test ./...` 仍会失败，但原因是仓库现有 `scripts` 包内多个脚本文件同时定义 `main`，与本次改动无关。

## 上一个已完成任务
- [x] 明确问题 3 在钉钉超时且 LLM 尚未完成时的目标行为。
- [x] 比较 2-3 种上下文与取消策略设计，并选出推荐方案。
- [x] 在保持外部行为不变的前提下，落地群聊异步回推编排重构。
- [x] 运行定向测试和包级验证，并输出结果总结。

## 上一个已完成任务复盘
- 新增了群聊异步回推正式设计文档和实现计划，明确采用“编排器版”方案收束群聊异步处理逻辑。
- 在 `pkg/dingtalk/group_chat_reply_orchestrator_test.go` 中新增了编排器测试，覆盖 Webhook 成功、Webhook 失效 fallback 和并发已满三条关键行为。
- 在 `pkg/dingtalk/group_chat_reply_orchestrator.go` 中提取了群聊回复编排器，并让 `pkg/dingtalk/stream.go` 的 `handleGroupChatAsync` 只负责委派。
- 通过 `go test ./pkg/dingtalk -run TestGroupChatReplyOrchestrator -v`、`go test ./pkg/dingtalk` 和 `go test ./internal/agent/... ./pkg/dingtalk/...` 完成验证。

## 上一个已完成任务
- [x] 为问题 4（`weekday_num` 协议不一致）编写最小设计与测试方案。
- [x] 为课表工具层补充“周日归一化”的失败回归测试。
- [x] 以最小改动将 `weekday_num` 规范到现有 `1-7` 协议。
- [x] 运行定向验证和包级验证，并输出修复总结。

## 上一个已完成任务复盘
- 在 `docs/superpowers/specs/` 和 `docs/superpowers/plans/` 下补充了针对星期编号修复的最小设计文档和实现计划。
- 新增了 `internal/agent/tools/schedule_test.go`，用于锁定星期编号协议，特别覆盖“周日 -> 7”。
- 更新了 `internal/agent/tools/schedule.go`，通过专门的辅助函数规范化 `weekday_num`，同时保持响应结构不变。
- 通过 `go test ./internal/agent/tools -run TestWeekdayNumberForTool -v`、`go test ./internal/agent/tools` 和 `go test ./internal/agent/...` 完成验证。

## 更早前已完成任务
- [x] 为修复 agent 问题 1 和 2 编写最小设计与实现计划。
- [x] 为后续工具调用链和错误订阅参数补充失败回归测试。
- [x] 以最小改动实现修复并让测试通过。
- [x] 运行定向验证和包级验证，并输出修复总结。

## 更早前已完成任务复盘
- 在 `docs/superpowers/specs/` 和 `docs/superpowers/plans/` 下补充了当前范围的最小设计文档和实现计划。
- 为 ReAct 后续工具调用链和 `subscribe_attendance_push` 错误参数各新增了一个回归测试；两者都在修复前失败、修复后通过。
- 更新了 `internal/agent/agent.go`，保证后续 LLM 轮次继续携带工具定义；同时更新了 `internal/agent/tools/admin.go`，在产生副作用前拒绝错误的订阅参数。
- 通过 `go test ./internal/agent -run TestAgentChatAllowsFollowUpToolCalls -v`、`go test ./internal/agent/tools -run TestSubscribeAttendancePushRejectsMalformedParams -v` 和 `go test ./internal/agent/...` 完成验证。

## 更早前已完成任务
- [x] 审查 `internal/agent` 目录的结构与职责划分。
- [x] 检查每个实现的正确性、错误处理和 API 协议问题。
- [x] 记录带文件和行号的具体发现。
- [x] 向用户交付审查结论。

## 更早前已完成任务复盘
- 审读了 `internal/agent` 和 `internal/agent/tools` 下的全部文件，并对照 `internal/app/agent_wiring.go` 核对了关键适配层行为。
- 验证了多步工具调用、管理员订阅参数错误、请求上下文丢失以及星期编号等几个主要问题。
- 运行 `go test ./internal/agent/...` 确认被审查的包可以正常编译；两个包均无测试文件但已通过。

## 再更早前已完成任务
- [x] 审查现有仓库规范，并检查 `tasks/` 下现有的流程文件。
- [x] 更新 `AGENTS.md`，补充流程编排、任务跟踪、验证、子代理使用和工程原则。
- [x] 审查更新后的文档，确认其与用户要求一致且表述清晰。

## 再更早前已完成任务复盘
- 更新了 `AGENTS.md`，加入流程编排、子代理使用、任务跟踪、验证和工程原则，同时保留了仓库原有的 Go 开发规范。
- 新增了 `tasks/todo.md` 和 `tasks/lessons.md`，使用户要求的计划与经验沉淀流程可以真正执行。
- 通过重新检查这三个文件，并运行 `git status --short -- AGENTS.md tasks` 验证文档已生效。













## 当前任务复盘
- 已静态追踪 `/api/admin/attendance/record/detail` 的完整链路：`internal/app/routers_attendance.go` -> `internal/handler/attendance_record_handler.go` -> `internal/service/attendance_record_service.go` -> `internal/repository/attendance_record_repository.go` / `internal/model/attendance_record.go` -> `internal/dto/attendance_record.go`。
- `AttendanceDetailResponse.RecordID` 的 JSON 字段名由 `internal/dto/attendance_record.go` 中 `json:"record_id"` 决定，`response.OK` 只是把该 DTO 放进统一响应体的 `data` 字段。
- `record_id` 仅在“从数据库快照构造响应”时被赋值为 `attendance_records.id`：`dto.NewAttendanceDetailResponseFromRecord` 直接执行 `RecordID: record.ID`；实时计算路径 `dto.NewAttendanceDetailResponse` 不设置该字段，因此会返回 Go 零值 `0`。
- 这意味着 `/detail` 接口在未 finalize 的实时视图下返回 `record_id=0` 是当前实现的必然结果；只有 finalize 后走 `GetAttendanceRecordFromDB`，且数据库已存在对应 `attendance_records` 记录时，`record_id` 才会是主表真实 ID。
- 额外风险是 `SaveAttendanceRecord` / `FinalizeAttendanceRecord` / `TriggerAttendanceStatistics` 在保存成功后也没有把新建或更新记录的主键回填到响应 DTO，因此同一轮请求返回值里 `record_id` 仍可能是 `0`。
- 验证方式为静态审读上述文件，并执行 `go test ./internal/service -run "TestGetAttendanceDetailReturnsCurrentViewBeforeFinalize|TestGetAttendanceDetailReturnsFinalSnapshotAfterFinalize" -v`，结果通过。

## 当前任务
- [x] 定位 `record.GET("/detail", h.AttendanceRecordHdl.GetAttendanceDetail)` 的 handler、service、repository 与响应 DTO。
- [x] 追踪 `record_id` 的来源、类型、赋值时机和 JSON 返回字段，确认是否存在错位或空值风险。
- [x] 通过现有测试与静态链路核对验证接口当前返回的 `record_id` 行为。
- [x] 输出排查结论，并在任务清单中补充复盘。

## 当前任务复盘
- `/api/admin/attendance/record/detail` 的 `record_id` 不是始终都有值：实时分支调用 `NewAttendanceDetailResponse`，该构造函数没有给 `RecordID` 赋值，因此返回 Go 零值 `0`；最终快照分支调用 `NewAttendanceDetailResponseFromRecord`，才会把 `attendance_records.id` 填到 `record_id`。
- 具体分叉发生在 `internal/service/attendance_record_service.go`：`GetAttendanceDetail` 在 finalize 前走实时计算，finalize 后走 `GetAttendanceRecordFromDB`；返回字段名由 `internal/dto/attendance_record.go` 中的 `json:"record_id"` 决定，handler 与 `response.OK` 都不会改写该字段。
- 因为代签接口 `internal/dto/sign_attendance.go` 明确要求 `record_id`，且服务端按主键 `FindByID` 查询记录，所以如果调用方拿实时 `/detail` 返回的 `record_id=0` 去补签，会直接失效；当前仓库里的 agent 补签链路也没有依赖 `/detail`，而是单独按日期+节次查记录 ID。
- 验证方式为静态审读 `internal/app/routers_attendance.go`、`internal/handler/attendance_record_handler.go`、`internal/service/attendance_record_service.go`、`internal/repository/attendance_record_repository.go`、`internal/model/attendance_record.go`、`internal/dto/attendance_record.go`、`internal/dto/sign_attendance.go`、`internal/agent/tools/admin.go`，并运行 `go test ./internal/service -run "TestGetAttendanceDetailReturnsCurrentViewBeforeFinalize|TestGetAttendanceDetailReturnsFinalSnapshotAfterFinalize" -v` 与 `go test ./internal/service -run "TestFinalizeAttendanceRecordPersistsLateAndNotArrived" -v`，结果通过。

## 当前任务
- [x] 复核实时考勤查询与现有代签链路，明确当前阻塞点与约束。
- [x] 基于“实时可代签、立即显示到 on_time、代签优先于真实迟到打卡”给出可落地方案对比。
- [ ] 等待用户确认设计方向后，再补设计文档与实施计划。

## 当前任务复盘
- 当前 `SignForUsers` 只能操作已落库的 `attendance_records`，按 `record_id -> FindByID -> 改写 NotArrivedIDs/OnTimeIDs` 工作，因此不能直接服务于未 finalize 的实时 `/detail`。
- 用户确认的新目标是：实时阶段允许代签；代签后 `/detail` 立即显示进 `on_time`；最终结算时若后续出现真实迟到打卡，仍以代签结果为准。
- 这意味着系统需要引入一层独立于最终快照的“实时人工覆盖状态”，且实时视图与最终结算都必须读取这层覆盖状态；仅仅让 `/detail` 提前生成 `record_id` 还不够，因为最终还需要保留“人工覆盖优先于真实迟到”的规则。

## 当前任务
- [x] 基于已确认的实时代签设计，输出可执行的实施计划。
- [x] 将计划写入 `docs/superpowers/plans/2026-03-21-attendance-realtime-manual-sign-override-plan.md`。
- [x] 单独提交 spec 与 plan 文档，避免与当前工作区业务改动混杂。
- [ ] 等待进入按计划实施阶段。

## 当前任务复盘
- 设计文档已落在 `docs/superpowers/specs/2026-03-21-attendance-realtime-manual-sign-override-design.md`，实施计划已落在 `docs/superpowers/plans/2026-03-21-attendance-realtime-manual-sign-override-plan.md`。
- spec 关键约束已经收口为：实时补签请求走 `date + section`，`week` 由服务端推导；最终视图必须继续合并人工覆盖；首版不引入撤销接口。
- 实施计划将工作拆为请求契约、service 失败回归测试、人工覆盖持久层与依赖注入、统一覆盖合并逻辑、范围验证五个任务，覆盖实时详情、最终结算、历史快照和旧 `record_id` 兼容路径。
- 文档已分别以 `64ad04a`（spec）和 `b86bac4`（plan）提交；提交后 Git for Windows 额外打印了 `sh.exe ... couldn't create signal pipe, Win32 error 5`，但提交本身已经成功写入本地历史。
## 当前任务
- [x] 完成 Task 1，请求契约扩展为兼容 `record_id` 或 `date + section`，并通过 DTO 定向测试。
- [x] 完成 Task 2，补齐实时代签/最终结算/历史快照一致性的 service 失败回归测试，并确认先红。
- [x] 完成 Task 3，接入 `attendance_manual_overrides` 模型、仓储、自动迁移和 service 依赖注入。
- [ ] 执行 Task 4：实现统一的代签入口解析、人工覆盖写入与详情/快照/结算合并逻辑。
- [ ] 执行 Task 5：完成 DTO / handler / service 范围验证，更新复盘记录。
## 当前任务
- [x] 完成 Task 1，请求契约扩展为兼容 `record_id` 或 `date + section`，并通过 DTO 定向测试。
- [x] 完成 Task 2，补齐实时代签/最终结算/历史快照一致性的 service 失败回归测试，并确认先红。
- [x] 完成 Task 3，接入 `attendance_manual_overrides` 模型、仓储、自动迁移和 service 依赖注入。
- [x] 完成 Task 4，实现统一的代签入口解析、人工覆盖写入与详情/快照/结算合并逻辑。
- [x] 完成 Task 5，完成 DTO / handler / service 范围验证并整理验证记录。

## 当前任务复盘
- `SignForUserRequest` 新增仅供服务端使用的 `OperatorID` 注入位，handler 在 `ShouldBindJSON` 后统一执行 `req.Validate()`，避免非法实时补签请求继续下沉到 service。
- `AttendanceRecordService` 新增统一的实时代签槽位解析与人工覆盖合并 helper：`record_id` 路径按快照反查 `date/week/section`，实时路径按 `date + section` 解析并由服务端推导 `week`；实时详情、最终视图、最终结算和历史快照代签都复用同一套 `force_on_time` 合并规则。
- 历史 `record_id` 代签现在除了修正快照，还会同步 upsert `attendance_manual_overrides`；实时 `date + section` 代签则不依赖 `record_id`，写入覆盖后 `/detail` 会立刻把目标用户并入 `on_time`。
- 修正了一处回归测试预期：实时补签只应移动目标用户，不应把未代签的 `MissingUser` 一并从 `not_arrived` 清空；这和已确认 spec 保持一致。
- 实际验证命令：
  - `go test ./internal/service -run "Test(SignForUsersSupportsRealtimeDateSectionAndDetailShowsOverride|SignForUsersRejectsRealtimeOverridesForNonAttendTargets|FinalizeAttendanceRecordKeepsManualOverrideOverLatePunch|SignForUsersWithRecordIDKeepsSnapshotAndDetailConsistent)" -v`
  - `go test ./internal/dto -v`
  - `go test ./internal/handler ./internal/service -v`
  - `go test ./internal/service -run "Test(GetAttendanceDetailReturnsCurrentViewBeforeFinalize|GetAttendanceDetailReturnsFinalSnapshotAfterFinalize|FinalizeAttendanceRecordPersistsLateAndNotArrived|SignForUsersSupportsRealtimeDateSectionAndDetailShowsOverride|FinalizeAttendanceRecordKeepsManualOverrideOverLatePunch|SignForUsersWithRecordIDKeepsSnapshotAndDetailConsistent)" -v`

## 当前任务
- [x] 追踪 `/api/admin/attendance/record/detail` 的 `on_time` 计算与去重逻辑，确认多次打卡是否会重复进入返回列表。
- [x] 结合实时分支、快照分支和人工覆盖逻辑，找出可能导致重复展示的具体路径。
- [x] 用现有测试或新增最小复现验证当前行为，并把结论写回复盘。

## 当前任务复盘
- 已静态追踪 `GetAttendanceDetail -> buildAttendanceDetail/getOnTimeUsers -> dto.NewAttendanceDetailResponse` 与 `GetAttendanceRecordFromDB -> dto.NewAttendanceDetailResponseFromRecord` 两条 `/detail` 返回链路，确认实时分支与历史 `SignForUsers` 写入路径都带有按用户 ID 去重逻辑。
- `getOnTimeUsers` 会先把钉钉打卡记录收敛为 `map[dingUserID]earliestCheckTime`，因此同一用户在同一节次内多次真实打卡时，只会保留最早一条，再决定进入 `on_time` 或 `late`；`applyCarryForward` 和 `SignForUsers` 也分别用 `currentOnTimeSet` / `onTimeMap` 防止重复追加。
- 为了验证不是静态分析误判，已在 `internal/service/attendance_record_service_test.go` 新增 `TestGetAttendanceDetailDeduplicatesMultiplePunchesFromSameUser`，用同一用户两条 `OnDuty` 打卡记录复现 `/detail`，断言 `on_time` 只返回 1 人且保留最早打卡时间；测试通过。
- 需要注意的边界是：最终快照读取 `dto.NewAttendanceDetailResponseFromRecord` 时不会再次去重，它会原样展开数据库里的 `on_time_ids`。因此“多次真实打卡导致重复显示”当前不存在，但如果数据库已经存进重复用户 ID，快照接口仍会原样显示，这属于脏数据容忍问题，不是当前实时计算链路的问题。
- 实际验证命令：`go test ./internal/service -run "TestGetAttendanceDetail(ReturnsCurrentViewBeforeFinalize|ReturnsFinalSnapshotAfterFinalize|DeduplicatesMultiplePunchesFromSameUser)" -v`




## 当前任务
- [x] 补充 `/attendance/record/detail` 状态优先级回归测试，锁定 `rest_day > leave > has_course`。
- [x] 以最小改动调整实时详情与最终结算的分类链路，让休息日和请假都能覆盖 `has_course`。
- [x] 运行定向测试并补充本次复盘，记录新优先级对接口返回的影响。

## 当前任务复盘
- 在 `internal/service/attendance_record_service_test.go` 新增 `TestAttendanceDetailPrioritizesRestDayAndLeaveOverHasCourse`，先复现“有课用户同时命中休息日/请假时仍被吞进 `has_course`”的旧行为，再锁定新规则下实时详情、finalize 返回和落库快照都必须一致。
- `internal/service/attendance_record_service.go` 新增了“候选人 + 原始有课名单”查询 helper，并把详情分类顺序调整为：先从候选人中识别 `rest_day`，再在非休息日集合中识别 `leave`，最后仅把未被这两类覆盖的用户保留在 `has_course`；`should_attend` 也同步改为只排除最终 `rest_day` 和 `has_course`。
- 现有 `getShouldAttendUsers` 仍保持旧语义，避免影响其他调用方；本次 blast radius 收敛在 `AttendanceRecordService` 的详情/结算路径内。
- 实际验证命令：
  - `go test ./internal/service -run TestAttendanceDetailPrioritizesRestDayAndLeaveOverHasCourse -v`
  - `go test ./internal/service -run "Test(GetShouldAttendUsersExcludesUsersFromDisabledDepartments|GetAttendanceDetailReturnsCurrentViewBeforeFinalize|GetAttendanceDetailReturnsFinalSnapshotAfterFinalize|FinalizeAttendanceRecordPersistsLateAndNotArrived|FinalizeAttendanceRecordKeepsManualOverrideOverLatePunch|SignForUsersWithRecordIDKeepsSnapshotAndDetailConsistent|AttendanceDetailPrioritizesRestDayAndLeaveOverHasCourse)" -v`

## 当前任务
- [x] 补充 `SlotAttendanceStatus` 回归测试，锁定 `rest_day > leave > has_course > should_arrive`。
- [x] 以最小改动调整 `AttendanceService.GetSlotAttendanceStatus` 的分类顺序，使其与 `record/detail` 一致。
- [x] 运行定向测试并补充本次复盘，记录接口返回口径变化。

## 当前任务复盘
- 在 `internal/service/attendance_service_test.go` 新增 `TestSlotAttendanceStatusPrioritizesRestDayAndLeaveOverHasCourse`，先复现 `slots/status` 把“既有课又请假/休息日”的用户仍归进 `has_course` 的旧行为，再锁定新优先级。
- `internal/service/attendance_service.go` 中 `GetSlotAttendanceStatus` 现已改为按 `rest_day > leave > has_course > should_arrive` 归类：先从全部候选人识别休息日，再在非休息日集合里识别请假，最后才保留未被覆盖的 `has_course` 用户。
- 同次修改把 `on_leave` 用户补进了部门名称查询，避免请假名单此前因为未参与 `GetUserDepartmentNames` 聚合而丢失 `dept_name`。
- 验证命令：`go test ./internal/service -run TestSlotAttendanceStatusPrioritizesRestDayAndLeaveOverHasCourse -v`、`go test ./internal/service -run "Test(SlotAttendanceStatusPrioritizesRestDayAndLeaveOverHasCourse|AttendanceDetailPrioritizesRestDayAndLeaveOverHasCourse)" -v`、`go test ./internal/service -v`。

