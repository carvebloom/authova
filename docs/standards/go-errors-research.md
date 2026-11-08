# Go 错误处理：行业方案与组织采纳依据

调查日期：2026-11-08。本文是统一规范的研究依据，不是声称存在唯一的 Go 最佳方案。取样优先选择语言维护者、公开工程规范、实际使用的框架源码、可观测性规范及成熟开源实现，不用 star 数或宣传语推断正确性。

## 1. 调查范围及判断

| 路线 | 代表来源 | 值得采纳 | 不直接照搬 |
| --- | --- | --- | --- |
| 标准错误值及因果树 | Go 官方 errors、ErrorValueFAQ、Go 1.13 博客 | errors.Is/As、%w、明确可依赖的错误契约；Join 保留多个原因 | 所有错误无限包装；所有底层错误都变成公共 API |
| 工程组织约定 | Google Go Style、Uber Go Guide | error 接口、清晰上下文、有意识处理、避免重复记日志 | 将某公司日志/退出实现当作标准库行为 |
| 协议错误适配 | Kratos v2.9.2/v3.0.0、gRPC status | 稳定 reason/status、安全 message、cause 留在进程内 | 把 FromError 当脱敏器；任意外部 status 直接透传 |
| 结构化诊断 | 标准库 slog | 固定字段、上下文关联、受控来源位置 | slog.Error(err) 自动提供完整因果栈或安全脱敏 |
| 分布式可观测性 | OpenTelemetry Go 与 recording-errors | span 状态、稳定 error.type、请求/trace 关联 | RecordError 自动 SetStatus；上报任意原始错误字符串 |
| 丰富错误诊断库 | CockroachDB errors | 保留栈、类型、允许公开的安全详情，支持边界隔离 | 为所有项目新增重型依赖，或把 unsafe details 全当可公开内容 |
| 多任务错误 | 标准库 Join、x/sync/errgroup | 关键次要失败不丢失；取消有界并等待退出 | errgroup 自动收集全部错误；Join 自动定义业务优先级 |
| HTTP 错误格式 | RFC 9457 | 稳定类型与公共解释，诊断和公开接口分开 | 借统一规范强制重写所有已有 JSON、OAuth/OIDC、CLI 契约 |

结论：采用“标准库因果处理 + 现有领域语义 + 项目协议适配 + 安全结构化诊断”。统一的是行为、字段语义、诊断要求和验收，不是要求四个项目使用同一个 error struct 或共享 SDK。

## 2. 官方 Go 错误模型

标准库支持单原因与多原因 Unwrap；Is/As 在原因树上搜索。单次 Unwrap 不足以遍历 Join。字符串可以用于显示，但不能作为程序分类的常规协议。多个分支同时匹配时，应用必须定义选择顺序。

包装会暴露可供调用者检查的错误属性，因而影响包的兼容契约。存储驱动错误可以保留给内部诊断，但领域调用方应依赖本项目语义。禁止一边要求 cause 无条件可见，一边声称底层依赖完全不构成接口。

已安装项目为 Go 1.27.x；当前文档包含 AsType 等新 API，但本次不为统一错误处理推动语言升级或无意义替换成熟的 errors.As。io.EOF 等受协议定义的直接比较也不能机械替换。

依据：[Go errors](https://pkg.go.dev/errors)、[Go 1.13 Errors](https://go.dev/blog/go1.13-errors)、[ErrorValueFAQ](https://go.dev/wiki/ErrorValueFAQ)。

## 3. 工程处理与日志所有权

Google 强调错误应有意识地处理，返回 error 接口；错误值文本适于组合。Uber 强调同一层不要在上报前又重复记一次错误日志，也承认降级后终止传播可以记录。本组织据此定义终止边界：HTTP/gRPC 响应适配、CLI 顶层、worker 的一次尝试完成处。

“只记录一次”限制的是同一逻辑失败的重复诊断；请求访问统计、业务审计与不同尝试不是同一条错误日志。终止并降级的内部任务，也需要自己的诊断归属。

依据：[Google Decisions](https://google.github.io/styleguide/go/decisions#errors)、[Uber Errors](https://github.com/uber-go/guide/blob/master/style.md#errors)。

## 4. Kratos/gRPC 的重要真实边界

本次直接读取本机模块缓存中项目固定版本 `github.com/go-kratos/kratos/v2@v2.9.2/errors/errors.go` 与 `v3@v3.0.0/errors/errors.go`：

- WithCause 复制公共错误并保留 Unwrap 原因。
- Error.Error() 包含 code/reason/message/metadata/**cause**。
- FromError 对无法识别的普通错误，以 err.Error() 构造 message。
- GRPCStatus 使用 message 与 ErrorInfo metadata，而不是自动判断各字段是否安全。

因此，使用框架错误不等于 message/metadata 天然可信；错误链保留与公开序列化必须分开。框架处理外部 gRPC 错误时也有风险：status.FromError 对包裹 status 的错误，会把完整外层 Error() 文本作为 message。公开边界必须选择本项目可信语义及允许的字段。

文档与代码依据：[Kratos Errors](https://go-kratos.dev/docs/component/errors/)、[v2 固定源码](https://github.com/go-kratos/kratos/blob/v2.9.2/errors/errors.go)、[v3 固定源码](https://github.com/go-kratos/kratos/blob/v3.0.0/errors/errors.go)、[gRPC status](https://pkg.go.dev/google.golang.org/grpc/status)。项目版本优先于网站 latest，不能为规范顺手升级 Kratos v2→v3。

## 5. 从 cause 到可定位诊断

标准 error 和 %w 本身不捕获异常创建时的栈；logger 的 source 通常只是记录位置。若没有源头包装、明确操作与来源信息，等请求最外层再采集栈不能还原已经返回的调用链。

CockroachDB errors 表明可以将原因、栈与安全详情分开保存。其可公开报告默认过滤不安全字符串，安全字段需要明确选择；这不意味着 Error()、%+v 或所有内部详情天然可发给用户。本组织优先复用项目能力，对关键失败路径补可解包的操作上下文和首次应用接触点，在终止边界只输出允许字段。没有必要先建立公共运行库。

“可追踪到细节”应被拆成可验收目标：返回链能检查原始类型/原因；公开错误关联到本次操作；内部诊断有操作、原因类型和来源位置；异步任务保存自己的关联标识。对驱动内部/远端系统无法观测的细节，必须承认边界，不能承诺全栈还原。

依据：[CockroachDB errors](https://github.com/cockroachdb/errors)、[slog](https://pkg.go.dev/log/slog)。

## 6. OpenTelemetry 与信息边界

RecordError 不自动设置 span 状态。安全的状态及 error.type 应与操作结果一致；状态说明不能含敏感详情。上报 err.Error() 可能把 URL、查询、token 或驱动信息转移到遥测系统，不能因为是内部平台就免除筛选。

保留现有 tracing 的项目需要测试使用本地 recorder/in-memory exporter 的实际字段。没有 OTel 的项目不为本次规范强制部署后端；先满足结构化日志和关联 ID。

依据：[OTel Go](https://opentelemetry.io/docs/languages/go/instrumentation/)、[Recording errors](https://opentelemetry.io/docs/specs/semconv/general/recording-errors/)。

## 7. 失败组合、重试与协议兼容

errgroup.WithContext 会取消关联 context，Wait 返回首个非 nil 错误，并非任意错误的全集。只有调用方确实需要处理多个失败时才 Join；正常读路径的预期 EOF、事务已结束后的回滚等要按具体契约判断。关键写入/flush/commit/close 失败不能普遍忽略。

重试必须有领域条件：幂等、尝试次数/预算、取消、是否已发生副作用。commit outcome unknown 或 DDL 不明确完成不能当一般503盲重试。HTTP 错误格式保留原客户端协议，OAuth/OIDC 标准错误也独立映射；RFC 9457 本身不要求替代已有领域格式。

依据：[errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup)、[RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html)。

## 8. 组织已有实现的保留方向

初步代码核查（最终以各项目审计和验收为准）：

- Authova：领域 sentinel、固定 Problem 响应、request ID、受控 tracing 属性，以及 commit outcome unknown 的独立语义值得保留；不能为加诊断降低其隐私边界。
- DataDock：SafeErrors、TargetReadError、明确 SQL 诊断与基础设施错误的区别、worker 治理与审批状态值得保留；需核对任意 Kratos 消息透传、worker 持久 err.Error() 和原始日志。
- ContextHarbor：未知内部失败对外屏蔽、独立认证边界值得保留；类型断言的包装支持、因果诊断与关联需要核对。
- Kitarbor：CLI/本地事务稳定错误、恢复与失败保护值得保留；面向远程用户和本机操作者的公开信息边界不应混为一谈。

此处不以 rg 命中当漏洞结论；各项目应给真实调用链与反例。后续实现必须保留客户端兼容，且验证“原因保留、安全公开、安全诊断”三项同时成立。
