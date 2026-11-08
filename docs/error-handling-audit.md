# Authova 错误处理只读审计

日期：2026-11-08。状态：审计建议，等待组织统一规范冻结，不是已实施规范。

## 范围与现场

- 工作区 Authova，main，HEAD `8f4f01e17477deeb1e982eb8202e8988162e4804`；审计开始工作树干净、ahead origin/main 2。保留已有本地提交，不提交、不推送。
- 根目录、仓库内及向上到文件系统根的已检查位置没有适用 AGENTS.md；适用 `CONTRIBUTING.md`：领域不依赖 SQL/驱动错误、App scope、禁止秘密日志、提交未知明确失败、公共契约需兼容。
- 仅读取公开源码、依赖缓存和测试定义；仅新增本文。未读取私密配置/测试凭据，未操作实例、数据库或其他仓库。未执行测试/重现注入；下述“已覆盖”均指现有测试代码，不能当成本轮新通过记录。
- 固定依赖：Kratos v3.0.0；grpc v1.83.2 为 indirect。没有发现装配中的 gRPC listener；Proto HTTP 与标准 OAuth/OIDC handler 是实际边界。生成 gRPC 类型不等于已部署 gRPC 服务。

## 核心判断：报错能否追到原始原因

只能部分追到。Core HTTP 确实生成 X-Request-ID；管理 Problem 带 request_id，Console/SDK/界面也有传递能力。存储错误对象通常仍能解包到原始驱动 cause，但普通管理 5xx 没有统一诊断日志出口，安全事件只留下分类，trace 通常只有 operation_failed/http_error。对象中的 cause 随请求结束即可能丢失。

没有发现 Authova 自己采集 `runtime.Callers`/`debug.Stack`、实现 StackTrace 或给普通错误附栈。`%w`、Unwrap、Kratos WithCause、OTel span 都不自动提供业务错误栈。Core recover 直接丢弃 panic 值与栈，只记录 request_id。Console 没有同等自定义 recovery，依赖 net/http 的最终兜底时可能打印 panic 值与 goroutine 栈，但没有统一 request/trace 关联和脱敏保证；这与“全项目已有安全错误栈”不同。

| 边界 | 当前能关联到什么 | 仍缺什么 |
| --- | --- | --- |
| 管理/查询 HTTP | Problem code/request_id、HTTP 状态、部分带 request_id 的安全事件 | 原始失败 operation/driver code 的统一日志、内部栈、稳定 trace 日志关联 |
| OAuth/OIDC | 标准 error 响应、外层 X-Request-ID；事务失败有按秒限频日志 | 日志没有 request_id/trace_id；并发失败被聚合，不能凭用户 ID 精确定位那次 cause |
| Console BFF | 本地 console_ ID；成功取得完整上游响应时保留 Core ID | 两个 ID 没有一条链接日志；请求/读取响应失败会丢上游状态/ID与原始 cause |
| Worker | message_id/attempt、独立 span、outcome 指标 | lease/send/ack 的安全原因分类；没有持久失败分类；未携带原始 HTTP request/trace 关联 |
| 查询 SDK→CLI/MCP | 非 200 时安全 status/code/request_id | 网络/取消错误被新字符串替代，Is/As 丢失；无本地 stack 或诊断关联 |

## 代表调用链与可复用实现

### 1. 管理写入与存储分类

`internal/transport/proto_query.go: protoQueryRouter` 注册生成 HTTP bindings，错误交 `transport/http.go: problem`，不是默认 Kratos ErrorEncoder。管理方法例如 `proto_management.go: UpdateApp` 经 `appOutput` → `application/service.go: AppManage` → `sqlstore.Store.App/transaction` → 绑定同一事务的 Ent 查询/写入（`ent_driver.go: boundDriver/entities`、`ent_upsert.go: expectOne/typedUpsert`）→ `errors.go: classify/storageError`。

- `domain/model.go` 九个稳定 sentinel；`domain/rate_limit.go: RateLimitError` 用 Unwrap 携带稳定 rate-limit 语义和有界 Retry-After。
- `sqlstore.Error` 同时实现 `Unwrap`（原始 cause）、`Is`（领域 kind）、`slog.LogValuer`（operation/driver/code/transient/kind 白名单）。Error 文本不拼原始驱动信息。PG unique/FK/check/deadlock 与 MySQL 相应分类保留，Transient 仅诊断，不授予写重试。
- `Store.transaction` 写事务 commit 失败单独归 `ErrCommitUnknown`；cause 仍可包含 context.Canceled。只读 commit 失败仍为普通存储错误。HTTP 管理返回 `503 outcome_unknown`，必须保留。
- `errors_test.go: TestClassifiedErrorsPreserveSafeDiagnostics` 检查 Is/As、双驱动、安全 Error/%+v/slog 和 unknown commit 不退化为 unavailable。是组织可以直接借鉴的“小安全投影＋可解包原因”模式，不需要先抽公共库。
- 缺口：operation 常只叫 scan/query/execute，上层资源/动作语义不足；原始错误在内存保留不代表已写日志。`store.go: seal/open` 把序列化、解密/解码失败直接改成 ErrUnavailable，cause 确实丢失。

### 2. 认证拒绝不是数据库失败

`transport/login.go: loginUI` → `application/authentication.go: VerifyChallenge` → App 写事务。错误 OTP 先设置 outcome=ErrUnauthenticated，回调返回 PutChallenge 结果；事务提交成功后才返回 outcome。这样失败次数、一次性消费不会因认证拒绝被 rollback。数据库或 commit 失败优先返回，不能用 defer 覆盖。

`protocol/storage.go: TokenRequestByRefreshToken` 检测 refresh reuse 后 RevokeFamily+Audit，返回认证拒绝；`protocol/types.go: dependency/TransactionFailure` 把真正基础设施失败旁路保存；`provider.go: ServeApp` 将标准库响应缓存在 recorder，确认存储事务提交后才输出。普通 OAuth 4xx 不等于必须回滚，dependency 或 5xx 才走失败。这是高价值既有行为，统一“有 error 就 rollback”会造成安全回归。

相关测试：`internal/integration/proto_commit_test.go: TestProtoHTTPWaitsForCommit`，credential concurrency、refresh reuse、OTP/身份恢复等 integration tests。部分需要真实双库环境，本轮未运行。

### 3. 协议错误与框架可信边界

- 管理 `problem` 只输出固定 type/title/status/detail/code/request_id，未知错误映射 503 dependency_unavailable，不输出 e.Error()。401/403/404/409/410/429 既有语义要保留。
- `provider.go: failure` 输出标准 `{error: ...}`；OIDC 库处理 OAuth 细分错误。不能统一替换成管理 Problem。
- 固定 Kratos v3.0.0 的 `errors/errors.go` 已本地核对：Error.Error 会拼 cause/metadata；WithCause 仅保链；FromError 对非 Kratos/非 gRPC 错误会 New(..., err.Error())。用户提供 v2.9.2 相同实现的核验线索；本项目实际依赖是 v3。
- 固定 grpc v1.83.2 `status/status.go: FromError` 对包装 GRPCStatus 的情况会 `p.Message = err.Error()`。同一种 Kratos/status 类型、同一个 reason，并不能证明其 message/metadata 是可信公共数据。
- 现状自定义 HTTP encoder 避开默认转换的泄露路径；后续不可直接使用 FromError/DefaultErrorEncoder 输出任意数据库/upstream error，亦不可用 WithCause 后直接打印作为脱敏手段。未来若真增加 gRPC listener，必须另设显式 allowlist mapper 与 details 白名单，而不是推断现有 HTTP 已覆盖。
- 本地 Zitadel `oidc.Error` 的 Parent 默认不进入 JSON，WithParent 支持 Unwrap；但 Error()/LogValue 可含 Parent，WithReturnParentToClient 可开放输出（项目未发现启用）。不能笼统认定所有第三方 error 的日志表示安全。未复现实例泄露，属于应补合成秘密回归的信任边界。

## 真实缺陷、例外和风险

### 可诊断性与关联

1. **确认缺口：普通 HTTP cause 没有统一安全日志。** `http.go: problem` 只编码响应并设置 security reason；`security_events.go` 保存 invalid_proof/dependency_unavailable 等分类。`/readyz`、限流器错误还直接换 sentinel。用户拿 request_id 可能只找到“不可用”，找不到数据库 SQLSTATE、超时或解码失败的具体安全分类。
2. **确认缺口：panic 栈丢失。** `wrapHTTP` 的 `recover()!=nil` 丢掉值和调用栈，日志只有 ID。不能补成打印任意 panic 值；建议内部有界 frames/安全 panic 分类，公共响应保持不含 panic。已开始写响应时不能再可靠改为完整 Problem，也需覆盖 partial-write panic。
3. **确认缺口：trace 不是诊断栈。** `observability/tracing.go: End` 只 SetStatus，未 RecordError；`observability/http.go` 故意不记 URL/header/body，是正确脱敏。项目装配未发现 W3C propagator Extract/Inject，也没有统一带 trace_id/span_id 的日志；request_id 只挂 HTTP span attribute，未作为通用 context 元数据传给下游。关闭 exporter 时没有可用 trace ID 也是合理状态，不应伪造 trace。
4. **确认缺口：协议日志不可逐请求关联。** `Provider.logTransactionFailure` 每秒聚合 path/error/failures_since_log，缺 App/request/trace ID。限频是合理保护，但应留安全分类计数与关联策略，不能简单全量高基数 cause 日志。
5. **确认缺口：启动错误被完全替换。** `cmd/authova: run` 在 Store.Open 错误时只返回 errors.New("database unavailable")；Console 对 Valkey/文件错误也类似。安全文本正确，内部原因（DNS/TLS/ACL/格式/超时）却丢失。反过来 `main` 直接 slog("error",e) 不能普遍安全：迁移 `%w` 链/未来上游错误可能携带任意消息。应安全表示保链，而非在“全打印”与“全丢弃”间选择。

### cancellation、Join、重试

6. **确认语义缺口：joined cancellation 无统一优先规则。** `http.go: problem` 先 NotFound/Invalid/...，没有 context.Canceled/DeadlineExceeded 分支；例如 Join(ErrInvalid, Canceled) 会命中 400，Join(ErrUnavailable, DeadlineExceeded) 走普通 503。`securityFailure` 类似。存储 Error 能解包到 context，但调用边界未利用；不要仅依赖 errno/错误字符串匹配。
7. **确认 cause 丢失：查询客户端。** `contracts/sdk/query/client.go: Query/fetchToken` 网络错误替换为新字符串；`accessToken` 等待分支返回 errors.New("query token request cancelled") 而非保留 ctx.Err。现有 `TestWaitingTokenCallerCanCancel` 只断言非 nil 与及时返回，不断言 Is(Canceled)。CLI/MCP 因而无法可靠区分取消、deadline 与服务拒绝。
8. **合理例外：未知写结果绝不自动重放。** Store commit_unknown、Core SDK 非 rewindable 写 body、BFF proxyFailure 对 GET/写区分 dependency_unavailable/outcome_unknown、前端 ApiError.isOutcomeUnknown 和 OutcomeUnknownNotice 都已有安全设计。不要把 cancellation 优先分类改成可重试结论；commit-unknown 标记及对外 outcome_unknown 必须独立保留，即使 cause 含 cancel。新增 cancel/timeout 公共 code/status 需组织明确兼容策略。
9. `cmd/authova/main.go` Worker metrics 模式 errors.Join 两个 goroutine 结果，保留两支是好事；Worker 正常 shutdown 返回 nil 是合理流程。`all` 模式 `go worker.Run(ctx)` 则丢最终错误，adapter budget violation 会让 worker 静默退出而 HTTP 仍存活；独立 worker 默认是部署方式，不消除此可用命令的缺陷。

### 异步持久分类与 cleanup

10. **确认缺口：Worker 原因未保存。** `delivery/worker.go: Run/processMessage` lease、send、FinishMessage 错误不记录安全 cause 字段；span 也只 End，结果靠指标。`ports.DeliveryQueue.FinishMessage(ctx,id,attempt,outcome,next)` 与 `sqlstore/outbox.go` 只持久 completed/available_at/attempts；没有持久 failure code/phase。SMTP permanent reject、TLS/网络、缺 provider、SMS 非2xx 都重试至次数/过期上限，未区分可重试性。已有至少一次、指数退避、App 公平预算、attempt fencing 很好，不能为分类改动破坏。
11. **边界风险：投递后 ack 失败。** 必须保持至少一次与 lease 恢复，不能把 ack failure 误记为 provider 未接收；SMTP Quit 错误也可能发生在 DATA 已接收后。持久分类应能表示 provider accepted/ack unknown，不能承诺恰好一次。
12. **cleanup 不应一刀切。** `defer t.Rollback()` 成功 commit 后返回 ErrTxDone、read-only Response.Body.Close、固定字符串 map 的 json.Marshal 错误等常是合理忽略。SMTP `writer.Close` 已检查，这是关键完成阶段。待改善：trace exporter Shutdown、安全事件 Shutdown 超时被 `_=` 丢掉，丢日志/trace 无安全提醒；事务 rollback 的非 ErrTxDone 意外失败无诊断；Store.Close/资源关闭需按是否影响最终数据或仅辅助 flush 分级。不要把 close 错误 Join 后随意覆盖原认证/commit-unknown 语义。
13. **goroutine panic 边界未覆盖。** Worker process goroutine 无 recovery；panic 会终止进程，不能被 HTTP recover 接住。随机源失败/缺 App transaction 等 panic 是严重 invariant/安全失败，不能全部伪装成功继续。需组织明确 HTTP 边界恢复、worker fail-fast/安全记录策略与 supervisor 责任。

## SDK、Console 与前端兼容面

- `contracts/sdk/client.go` 是管理 SDK 源，`console/internal/coreapi/client.go` 是生成镜像，禁止仅修改后者。Problem.Error 只拼 Code/Title；Request 网络错误返回原始 error（保链但表示不保证安全），读取 body 成功后才保存 status/request ID，truncated body 会丢已收到的关联头。
- `console/cmd/authova-console/security.go` 生成本地 console_ ID；proxyManagement 透传 Core Problem/ID，失败时只按 GET/写返回安全码，原 err 被丢弃，没有 BFF→Core 关联日志。`invalidateRejectedSession` 的返回值虽被调用者忽略，helper 已有 defer 安全 Warn，故不是完全静默；但日志缺 request/trace/安全原因分类。代际比较与条件删除能防止迟到 401 清掉新会话，是应保留的优秀实现；后续每次权限核验继续 fail closed，清理失败不可被误报为已成功撤销。
- `console/web/src/api/client.ts` 从 body 取 request_id，不从 X-Request-ID 兜底；非 JSON、响应体中断时即使 header 有 ID 也丢失。ErrorNotice 显示 code/request_id；前端不自动重试未知写结果，这是可复用部分。
- `contracts/sdk/query.Error` 对 server code 白名单、request ID 字符/长度约束，不暴露原始上游 body，非常适合 CLI/MCP 的模型可见边界。不能为加 cause 就把原始 URL/响应/凭据塞进 Error()；可以增加非序列化 cause 并保持当前文本/退出码。
- `contracts/cmd/authova-query: main/run` stderr 错误、exit1；MCP `mcpquery.add` 直接回 query error 供 SDK 转成 tool error。未来新增内部诊断不应污染 stdout/MCP stdio，不要将业务拒绝变成协议崩溃。MCP 的 JSON 解码错误需与 SDK 安全表示策略对齐。

## 现有门禁与未覆盖点

- `Makefile: check` 为 gofmt/vet/全包 race tests；CI 固定 staticcheck v0.8.2、govulncheck v1.1.4。未发现 errorlint/errcheck 或等价专门错误链规则。
- 可直接扩展：sqlstore/errors_test.go，transport/proto_query_test.go、resources_test.go；Console proxy_test.go（本地 correlation、未读完整响应无重试）；contracts/sdk/query/client_test.go（取消/恶意响应）；delivery/worker_test.go（cancel drain、过期、App公平、adapter异常）；observability/observability_test.go（OTLP实际输出且无敏感属性）；securityevents/recorder_test.go（队列满、write失败、shutdown deadline）。
- 新验收应补 joined tree、context Is、HTTP 原因安全诊断与 request/trace 关联、panic 安全栈、持久失败分类和关键 flush/rollback 错误。不得以历史双库/浏览器报告替代这些新门禁。

## 建议最小实施批次（等待规范冻结）

| 批次 | 建议路径 | 验收 |
| --- | --- | --- |
| P1 安全错误投影与保链 | sqlstore/errors.go、store.go 的 open/seal，cmd/authova/main.go，SDK query/client.go；局部复用、不新建跨仓库库 | Is/As 穿过多层 %w/Join 保留原 cause；Query 等待取消可 Is；安全 Error/%+v/JSON/slog/trace 不含合成秘密；不能凭同类型任意 message 视为可信 |
| P1 边界一次安全诊断 | transport/http.go、protocol/provider.go、Console proxy/security、observability | 未知错误 wire 仍既有通用 code；内部记录有界 operation/class/driver code、request_id/可用trace_id；无原始 cause 字符串；聚合指标不丢分类；panic 仅内部安全 frames，不含参数/秘密值 |
| P1 取消优先与未知写保护 | HTTP/security 分类及 SDK；保留 protocol 标准映射 | joined cancel/deadline 比通用领域匹配优先用于内部分类；unknown-commit 对外 outcome_unknown 与不可重放不丢。现有 HTTP/CLI 表现先不变，公共新状态/错误码另行版本决策 |
| P1 worker 边界/cleanup | cmd/authova all 装配、delivery/worker.go、shutdown defers | all worker 异常可见/被监督；panic 策略明确；lease/send/ack 安全分类；flush/rollback 次错误保留但不覆盖主结果；无 stdout/MCP 污染 |
| P2 outbox 失败持久分类 | ports/storage.go、domain Message、delivery/worker.go、internal/ent/schema/outboxmessage.go、PG/MySQL迁移 | phase/code/retryability/acceptance-unknown 有界非秘密；重启后仍可诊断；保持 fencing、公平、至少一次、双库升级与旧行为；不得改旧 v1 迁移 |
| P2 关联补齐及生成一致 | contracts/sdk/client.go → Console生成镜像、console/web/src/api/client.ts | body损坏仍保header ID；BFF/Core两ID可联查；取消不触发误重试；SDK生成一致，stdout/exit1/Problem字段不破坏 |

持久分类需新版本化数据库迁移：当前 v1 历史不可重写；使用后续版本、显式默认值/允许旧记录无分类、双库独立审核，先定最小字段再生成 Ent/Atlas。公共 Problem/CLI 文本结构、OAuth error 与现有状态码保持；新增 gRPC、公共 cancel/timeout code 或更细错误语义，必须先列调用方影响与版本兼容方案。当前不安装依赖、不启用 lint、不提前执行上述改造。
