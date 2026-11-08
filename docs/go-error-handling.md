# Authova Go 错误边界

本轮采用组织规范 **v1.0.1**（2026-11-08）。[冻结副本](standards/go-errors-v1.0.1.md) 来自 `github.com/carvebloom/kitarbor/docs/standards/go-errors-v1.0.1.md`，SHA256 `10e29e102ee1e7c7fc9d61ffad0e1b9458990f765be3c49491fc7a46399e6d05`。副本逐字复制，不在本项目改写；[来源清单](standards/source.json) 记录所有摘要。v1.0.0 只保留为历史证据，不是当前入口。[原审计](error-handling-audit.md) 保留当时状态；下列内容是本轮实施说明。

## 公共契约

管理 API 继续固定 Problem、HTTP 状态与 machine code（not_found、invalid_request、forbidden、authentication_required、conflict、flow_expired、rate_limited、dependency_unavailable、outcome_unknown）；未知/任意 Kratos/gRPC 错误都不直接使用框架 FromError。outcome_unknown 为写副作用事实，优先于取消与普通错误，绝不自动重放写操作。取消优先用于内部诊断，不引入499或新的公共超时代码。

OAuth/OIDC 继续其标准 JSON/error/redirect 形态与原状态码，注册的 error code、已验证 redirect 目标及 caller-bound state 保留。安全收紧：第三方 error_description 改为固定公共文案，不再透传任意 parent/metadata/error_uri。这是人工解释文本的安全规范化，不是新的机器错误码；调用方必须依赖 error code。标准 state 作为调用方绑定值的协议回传是有意例外，不能写进诊断日志。

Console BFF 对上游错误重新投影固定安全字段，不信任同名 Problem 的任意 title/detail/metadata。正常业务成功 DTO 不重写。管理 SDK Response 在读 body 前保存 status/header ID，DependencyError 同时携带受限 header ID，Login 只返回 error 时也不丢失关联；DependencyError 的 Error 为固定文案，Unwrap 可检查 context/网络 cause（仅 context 分类作为稳定承诺，不承诺底层网络/驱动实现）。查询 SDK/CLI/MCP 保留读操作、stdout/stdio结构、exit1；错误可解包取消，新增关联信息不输出原始传输 URL/body。CLI 的未知错误和未知参数改为安全通用输出，不回显任意 Error()；SDK自有校验文案、已登记拒绝码及退出码保留，终止诊断另行写stderr，不污染stdout/stdio。

## 诊断所有权

`internal/diagnostic` 是Core的项目局部实现，不是跨项目运行库；Console 使用自己的 `console/internal/diagnostic`，不引用Core领域或数据适配器。SDK/CLI/MCP 不依赖服务端 internal 包。

- `Wrap` 只在选定首次应用接触点保存 operation/phase 与最多8帧的 file/function/line。已捕获的应用帧不是远端/驱动原始栈。没有接触点快照时，终止边界明确标记 `recorded_at`。
- `Inspect` 对两种 Unwrap 树最多检查32节点；不调用 Error/Format/LogValue/任意 Is。稳定分类顺序与 Join 排列无关，取消优先于 deadline/通用领域类；outcome_unknown 独立优先。存储 Error 在可解包链中保留 kind 与 cause，并维持安全 Error/LogValue。
- `Secondary` 在合并前保存 Primary/Cleanup/Committed；Is/As 可达两支，诊断/公共投影的主结果不被 cleanup 取消重定义。事务 rollback 异常按此保留；正常 ErrTxDone 忽略。迁移锁释放失败会强制丢弃物理连接（sql.Conn.Close本身只是归还池），并保存主/次结果；已提交＋cleanup失败的语义单独测试，不称未提交。BFF refresh锁释放错误使用独立有界上下文记录主/次原因，已成功轮换的会话继续返回成功，不以cleanup取消覆盖；CAS及TTL不改。
- 终止边界调用 `Log`；同一请求上下文最多一次应用失败诊断。业务审计、HTTP统计、不同 Worker 尝试另计。协议既有限频保留，聚合计数/指标仍在，不进行全量原始错误日志。
- 允许字段：operation/phase/kind、cause_type、driver/driver_code、transient（仅诊断）、outcome_unknown/committed、request_id/diagnostic_id、有效 trace_id、first_operation/first_phase、source/frames；组合错误增加 secondary_kind/type/driver/code/source；BFF 增加经界定的 upstream_request_id；Worker另记attempt/delivery_state/delivery_code/ack_unknown。动态值限制长度/数量；未知值不回退 Error()。
- process logger 的 `diagnostic.Handler` 在第三方日志 LogValue 解析前丢弃原始 message、URL、request对象及 metadata，保留安全原因摘要和受控来源。应用自己的记录仍必须使用已审计字段，不把任何 Error() 当日志脱敏器。
- tracing 仅加入稳定失败类别/来源与受控事件，不调用 RecordError(rawErr)，不新增遥测后端或伪造 trace。SDK body 丢失时 BFF仍可用上游 header ID 关联本地 ID，前端也从 header 兜底。

HTTP 应用恢复位于实际 handler/decoder 内侧；没有启用 Kratos 默认 Recovery。raw setup 还有安全 HTTPGuard。任意 panic 值/请求/运行时参数栈均不打印；仅受控 Callers 帧，source=panic_unwind。已写响应及 http.ErrAbortHandler 均维持 stream abort，不二次拼JSON。Console、CLI、MCP tool callback、Worker process/lease/send goroutine 边界分别处理，Worker 不变量失败由进程监督返回非零，不能留下只有HTTP仍工作的 all 进程。

## 投递持久化与 v1.0.1 例外

新增 schema **v2 / 002_delivery_diagnostics.sql**（PG/MySQL各自 Atlas checksum），不改历史001或payload v1。四列：failure_phase varchar(16)、failure_code varchar(32)、delivery_state varchar(16)、outcome_unknown smallint。旧记录默认 unknown/unknown/unknown/0。Ent 生成实体同步。运行时不自动迁移。

worker 使用新增可选 DiagnosticDeliveryQueue 接口，原 DeliveryQueue/FinishMessage 保留供现有适配器兼容；正式 Store 实现严格的 FinishMessageWithFailure。phase/code/state 都按枚举验证（包括 provider_missing/provider_rejected/transport 与取消/超时等稳定类），绝不保存原始 error。strict API 零匹配返回 ErrLeaseLost；旧兼容API保持既有幂等 no-op 约定。新生产Worker不会把stale ack当成功。

发布 lease 时先写 send/unknown/unknown/1，崩溃后不会误留上一次 accepted 事实。正常 finish 原子写分类与状态；dispatch row→message 的锁顺序、attempt fencing、每App预算、公平性、退避/次数/过期不变。SMTP 在应用首次失败时保存源位置，区分未进入发送、发送未确认、DATA已接受后Quit失败；其他无法证明的供应商失败保守记unknown。缺provider与过期是not_sent；明确成功是accepted。ack失败的诊断同时保留首因/ack原因及delivery_state/ack_unknown，若DB不可写则不能保证新分类落库，持久 lease 的unknown与关联日志用于恢复。

**至少一次投递例外**：provider已接收但本地ack失败后，租约仍可恢复再投递；即便SMTP没有幂等保障，也保持原来预算内的可能重复行为，不改为至多一次、不静默丢弃、不宣称恰好一次。未知业务事务commit、DDL cutover不因该例外获准重放。

部署以后需先备份并显式 migrate 到v2，再启动新binary。旧binary会按既有版本检查拒绝v2，不承诺直接降级；回滚需匹配版本/备份方案。新增列允许旧SQL读写保留默认值，但不绕过版本保护。本轮不部署或触碰Lab/原迁移实例。

## R01—R12 对照与验收

| 规范 | 项目落实与有意例外 | 回归入口 |
| --- | --- | --- |
| R01 | domain sentinel、sqlstore安全类型；有界公共分类、Join确定顺序 | diagnostic/error_test、sqlstore/errors_test |
| R02 | 存储编码/解码、启动Open、SDK网络/取消关键cause保留；故意隐藏认证/存在性仍返回domain语义 | 多层Is/As、SDK query取消、真实storage commit wrapper、Store.transaction的driver commit失败 |
| R03 | Secondary保主次；unknowncommit事实优先；HTTP码保持兼容 | 主失败+cleanup取消、主取消+cleanup真实失败、已提交+cleanup失败、Join双序 |
| R04 | OTP/reuse提交顺序不改；非rewind写body；outbox至少一次登记例外 | 原Proto commit/OTP/reuse/SDK no-retry、Worker ack失败 |
| R05 | 固定Problem/OAuth投影；BFF/SDK不信任上游消息；没有gRPC listener | 伪造Kratos/gRPC/code/metadata、OAuth error/redirect投影 |
| R06 | 白名单日志/树上限/来源；不把cause转字符串 | canary SQL/密码/URL/body不进HTTP/log/trace；框架logger过滤 |
| R07 | server ID→context→诊断；SDK先header后body；UI header兜底 | raw Kratos handler、SDK截断body、BFF错误投影、前端单测 |
| R08 | 受控frames、recorded_at区分；Core/raw/BFF/CLI/MCP/worker恢复 | 带秘密panic/请求、ErrAbortHandler、partial response、MCP tool panic |
| R09 | v2有界持久列、strict lease lost、ack首因次因、恢复 | 双库ReadAfterReopen、stale fencing、旧v1记录默认unknown |
| R10 | rollback/trace与安全事件shutdown诊断；ack独立有界completion ctx | 组合错误和监督、cancel drain；正常ErrTxDone/只读Close/固定序列化合理忽略 |
| R11 | 001 checksum不动；002双库；Console SDK按源重新生成；不改既有HTTP码/CLI退出 | archived v1 binary升级、重复migrate、Ent/Atlas对齐、契约组合 |
| R12 | 安全与有用诊断双断言，隔离fixture不附着旧项目 | scripts/tests/go-errors.py、make check、生成检查与Console检查 |

可重复 DB 门禁：`python3 scripts/tests/go-errors.py`。创建随机 Compose project、回环随机端口与随机凭据，PG/MySQL/Valkey为本轮独立容器；从基线归档构建v1 binary，实测旧库初始化与v2升级/默认值、重复迁移，再执行真实双库race/契约组合。现有outbox fixture另建随机空库验证v2初始化与持久分类，均退出清理自己资源。日志/结果保存在Git忽略的 `.migration/go-errors/`，不可拿无DSN的Skip代替通过。

实际运行结果、Skip和未覆盖项见[本轮验证报告](go-errors-validation.md)。不是全项目每层都包栈/日志；第三方远端原始栈、所有平台/外部供应商故障、完整浏览器登录以及真实生产部署均不在本轮成绩中。demo/benchmark程序的显示/错误方式没有机械统一；SDK未知网络原因的具体实现类型不作为长期契约。
