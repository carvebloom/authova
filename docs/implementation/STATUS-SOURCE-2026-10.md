> 历史来源记录：本文保留迁移前的版本与验收描述，不是当前仓库的迁移或部署指令。当前请见 [版本说明](../migration/VERSIONING.md) 和 [运行说明](../migration/RUN.md)。

# 首版交付状态

更新：2026-10-21。版本 0.1.0-rc.1，三个独立仓库，Apache-2.0。本轮验收在本地完成；不代表远端 CI 或稳定生产发布已通过。仓库同步状态以 Git 记录为准。

## Authova 只读查询 MCP 与 Skill

新增独立 query audience、八个读取 scope 和额外 contacts 能力，复用 owner 审批的自主 Agent，提供12条只读 API、12个 MCP stdio 工具、Go SDK、JSON CLI 和可分发 Skill。普通业务/用户委托/Console 凭据不转成查询身份，查询凭据不转成管理角色。App/tenant 边界由 Core 主库校验，默认掩码联系人，审计 reason 和秘密不返回；用户与连接等数据有界分页。schema 仍为 v8，没有新表或 Console 改造。

最终真实 PG/MySQL Core race 442 叶通过、16 项显式专项 skip、0 失败；Core/Contracts 实际组合通过。编译后的 MCP stdio 与 JSON CLI 已分别访问两库的真实 Core，验证工具发现、读取、跨 App 拒绝与撤销后拒绝；Contracts 全检查、Core vet、正常 CLI 构建和 Skill 格式校验通过。范围、运行方式及源码/日志摘要见 [查询实施记录](AGENT-QUERY-2026-10-21.md)。本增量尚未切换演示服务或推送，不继承历史容量或外部客户端互操作证明。

## 身份生命周期八项整改

本轮修复登录 OTP 证明归属、UserInfo/MFA 当前政策、最后登录凭据保护、全入口验证码配额、纯验证码注册、普通身份恢复隔离、会话分页和无效 JWT 有界验签；复用公共安全规则，同步 Core、Console 与 Contracts。schema 升至 v8，原迁移1–7保持不变，升级须停旧写入者。细节、迁移兼容性与完整恢复步骤见 [整改手册](IDENTITY-REVIEW-REMEDIATION-2026-10-18.md)。

最终源码的完整 PG/MySQL Core race：424 叶子通过、16 条显式专项夹具 skip、0 失败；Core/Contracts 真实组合通过。Console 245 项前端测试及 Go race/vet、类型/lint/格式/build 通过，Contracts 完整检查通过；两库实际旧备份 dump/restore 与隔离审批恢复、真实浏览器纯验证码注册分别验证。机器摘要见 [本轮验收](../../evidence/identity-review-2026-10-18/acceptance.json)。本轮不重新证明持续容量、目标部署 HA/PITR 或真实供应商送达；下方历史证据仍属于各自版本和画像。

Astra 极高（`gpt-6-astra` / `xhigh`）已完成最终工作树只读复审，八项全部 Closed；复审发现的注册渠道条件遗漏已修复并纳入最终全量门禁。当前整改范围内无未解决的 P0/P1/P2；不是外部专业安全审计或生产发布认证。

## Outbox 与 Valkey 安全增量

新增持久化 App 轮转、每 worker 4/每 App 2 的投递预算、隔离故障 App、租约/attempt fencing 与原子入队；生产 Valkey 强制验证证书的 TLS 和命名 ACL，支持 mTLS/password file。Core 与 Console 使用独立角色和 env/secret 挂载。schema v7 升级步骤及兼容性见 [运维说明](OUTBOX-VALKEY-2026-10-18.md)，本轮实际门禁见 [验收记录](OUTBOX-VALKEY-ACCEPTANCE-2026-10-18.md)。

本轮新版本另以真实 mTLS/命名 ACL 完成双库各五分钟回归：各 153 万次成功，平均约 5100 QPS，最低分钟 PG 5099.9494 / MySQL 5099.8900，错误、刷新失败、错配和丢弃均为 0。Core 全量 race 345 叶通过，补充 SQL+worker/迁移专项、TLS 故障恢复及非 root 只读容器通过。此短回归不替代 mTLS 新画像的一小时证明。

## 持续容量整改

PG/MySQL 在同一份固定源码和相同二进制的本地 Linux 同网络画像中，分别完成 3600 秒、各 18,360,000 次成功内省；平均约 5100 QPS，最低分钟分别 5099.7658/5099.7729 QPS，HTTP/刷新错误、掉队列、主体错配全部为 0。每库 2856 次正常 TTL 真实刷新、跨实例撤销通过。安全回归 328 叶测试/120 顶层通过，Core/Contracts 组合和 Go vet 通过。

画像为真实每库 1200 万用户背景、24 个分散活跃用户/租户、两个独立 Core 进程、256 HTTP 并发及显式 16384 发生器缓冲；包含计划排队延迟及排空时间。端到端 P99 为 PG 4ms/MySQL 3ms，最大分别 1712/700ms。历史 macOS、较小发生器队列等失败画像继续保留；不外推为 TLS、跨主机、密码/签发或所有活跃用户容量。详见 [整改与机器证据](CAPACITY-REMEDIATION-2026-10-17.md)。下方历史统计保持其各自验收边界。

本次登录方式配置、支付宝及界面更新的验证范围见 [2026-10-16 验收](LOGIN-METHODS-ACCEPTANCE-2026-10-16.md)。下方容量、备份恢复、容器等条目属于各自历史验收；本次没有重新执行这些专项，也不能用它们证明当前演示库仍包含历史容量数据。

后续 Agent 专项已重新执行真实两库长测、1200 万用户备份恢复及多实例容量尝试，最新结论见 [Agent 持续负载与恢复验收](AGENT-ENDURANCE-ACCEPTANCE-2026-10-16.md)。这不更新历史 UI、容器或供应商验收的证据范围。

## Agent 身份增量

已加入预注册 Agent、接入、用户委托/自主授权、短期凭证、当前撤销、owner/CAS/回执、资源 SDK、管理 UI 和独立 REST/MCP 示例。设计与实际边界见 [Agent 身份设计](../technical-design/14-agent-identities.md)。本轮验收见 [本地验收记录](AGENT-IDENTITY-ACCEPTANCE-2026-10-16.md)，历史性能数据不代表新增 Agent 路径的容量。

后续全面复审修复了管理 scope 消费校验和拒绝刷新请求消耗凭据的问题，并补充跨 App、跨租户、跨用户及客户端凭据目的的实际测试，详见 [安全复审记录](AGENT-SECURITY-REVIEW-2026-10-16.md)。此前验收计数仍作为实现基线保留。

新增恢复后本地隔离命令：逐 Agent 原子封禁并清除旧 secret，按 100 条分页关闭接入/撤销 grant；后续页失败保留已提交的封禁，支持重跑。实际证明旧备份不能包含之后的撤销，恢复副本须先停止写入并隔离，不能直接开放。最终 Core race 为 307 叶子/104 顶层通过，Core/Contracts 组合通过；两库 Agent 与普通 OAuth 各一小时通过、两库各 1200 万用户实际恢复通过。独立容量阶段五分钟失败终止：PG 4985.73、MySQL 4773.41 成功 QPS，存在预算拒绝与刷新错误，持续 5000 QPS 仍未通过。详见上述专项报告及机器证据。

## 已实现

- Go Core、绑定 App 的领域/事务 ports、PG/MySQL adapter、八版本迁移、独立 runtime 账号、PG FORCE RLS、复合 FK/唯一约束。
- 51 项管理 API（37 路径）：平台运营账号、独立 App、Client、用户/会话、固定管理员角色、OIDC/GitHub/微信/QQ/支付宝连接配置、渠道就绪状态、审计和签名轮换；OpenAPI/Go Client/SDK/TS schema 与实现同步。
- 密码、Email/Phone 验证、注册/找回、TOTP/恢复码、OIDC/GitHub/微信网站扫码/QQ 网站登录/支付宝网页授权上游、多身份绑定状态与防锁号、自助密码/地址/绑定/资料清除；秘密加密、审计/outbox 原子写入。
- Code+S256 PKCE、RS256、at+jwt/ID token audience/at_hash、refresh 原子轮换/reuse 撤销、UserInfo、撤销、主库快照内省。
- 平台原生管理会话、App 管理专用 Client/PKCE/MFA、逐请求固定角色与最后 owner 保护。独立 Console BFF 加密共享会话，管理 token 留在服务端。
- 实际 RP 双 App Demo、SMTP/短信沙箱、独立演示库、一键启动/完整本地验证、非 root OCI、外部依赖生产 Compose、运维/升级/恢复文档。
- 开源规范、CI 定义、Go CycloneDX 模块清单与真实上游许可证/NOTICE；没有组织、业务 RBAC/PDP 或空扩展服务。

## 实际验证

| 证据 | 结果/边界 |
| --- | --- |
| PG/MySQL conformance + race | 同一套真实数据库用例通过，跨 App、code/refresh 竞争/reuse、当前撤权、OTP 重用/失败计数、最后 owner、管理 MFA/角色、自助密码/资料清除 |
| 故障注入 | 真实 test-owned TCP 代理切断 DB；预热有效 token 后 503、恢复重新有效；两库验证，不伪造 inactive/active |
| 多副本 | 两个独立 Core provider 使用同一真实 DB，code/refresh 单次消费与 reuse revoke 跨副本可见；非多机 HA |
| 正常 TTL 持续流程 | PG/MySQL 各 30 分钟，7 次真实 refresh；PG 16025、MySQL 16228 次检查 |
| 维护/运行 | 两库 v1→v2→v3→v4 迁移/回填、dirty/checksum/权限/RLS 拒绝、清理中途失败全回滚、非过期 child 保留、outbox fencing；worker 取消/并发/到期、SMTP 停滞取消、HTTP drain |
| 备份恢复 | 两后端实际备份到新库，schema clean、用户密文与 RSA 私钥解码通过；各 1200 万全量库/各 1000 解密样本；单机完整恢复，非客户灾备 RPO/RTO |
| Console | 本轮 30 文件/228 项行为测试，TS/lint/Prettier/build、Go vet/真实 Valkey race；当前范围见 LOGIN-METHODS-ACCEPTANCE-2026-10-16.md，205/170/127 项为历史快照 |
| Chrome | 平台 owner/创建 operator/关闭一次性 secret、375px、双 App 同邮箱 SMTP 注册、PKCE、刷新/内省、密码修改/退出/重新登录/资料清除、第三方 OIDC/UserInfo、手机注册/换绑/密码与短信登录、GitHub 连接创建/停启用/secret 更新通过 |
| 契约 | 正式 + 三份历史 OpenAPI lint 无错误/警告；确定性生成、Go 编译和 Console DTO 断言通过 |
| 安全/静态 | Go 1.27.2、staticcheck/vet/format/race；govulncheck 可达 0、导入包 0，4 项未调用模块漏洞记录保留 |
| 容器/运行包 | 两镜像构建；UID 10001、只读根文件系统的启动/health/readiness/管理鉴权/Console CSRF smoke；容器 smoke 未验 TLS 入口；另外通过 production 模式真实测试 TLS 链/安全 Cookie/CSRF |

原始输出及机器可读报告位于 `evidence/`，完整本地脚本 `make verify`；本地开发者复审报告 `REVIEW-2026-10-14.md`（未完成独立外部审查）。没配置真实 DB 时的 skip 不能代替上述证据。

## 容量

PG/MySQL 各 COUNT 确认 12,000,000 用户及独立标识。历史 120 秒/1000 活跃用户 fixture 的两库约 5098 QPS 零错误/drop 仍保留；本轮长期结果优先用于验收结论。

PG 100,000 用户样本的 1 小时：4594.96 成功 QPS、792 失败、1,817,344 掉队列。PG 1000 用户样本的 1 小时：4928.72 成功 QPS、0 HTTP 失败、616,565 掉队列、P99 784.4ms。整段吞吐未达到 5000，容量 gate 未通过。连接池 128 的两库同时施压 5 分钟为 3977.57 QPS，不能称独立单库测试。

MySQL 一小时为 4427.75 QPS、311,671 个 HTTP503、2,106,960 掉队列；中断恢复后的请求继续正常，但整段不通过。完整结果在 `PERFORMANCE.md`；进程/DB 中断轮次和所有中间报告保留。只测持久化在线内省；合成 fixture TTL 为测量时长+10 分钟，正常 5 分钟 token/refresh 由独立持续流程测试，不能将此吞吐推广至密码、签发、短信或全 12M 活跃集合。

上述历史画像存在本机共享 Docker 和负载生成器资源竞争，失败结论保留。后续整改已在文首限定的 Linux 画像达到持续容量目标；不将其视为这些旧画像已经通过。

## 未完成的外部发布门槛

客户 TLS/网络/数据库 HA、专用部署画像下的持续容量/攻击负载、灾备 RPO/RTO、真实短信厂商凭据、各 IdP 供应商兼容、私密安全报告联系人与独立安全评审。部署模板、开发验证和上游库认证不能证明这些已完成。当前是可运行、可测试的正式生产候选实现；本地 Linux 画像持续容量已通过，目标部署和外部生产验收仍须分别完成。

## 第三方、手机与配置补齐

详见 [能力与接入说明](IDENTITY-CONNECTIONS.md) 和 [本轮验收](IDENTITY-ACCEPTANCE-2026-10-14.md)。本轮增加 GitHub OAuth2、连接版本化编辑/停启用/secret 更新、真实绑定状态及可登录性保护、SMS 本地收件箱；通用 OIDC 保留。真实供应商验收和持续容量边界不变。

## 微信与 QQ 网站协议

补齐微信 `snsapi_login` 网站扫码和 QQ 互联网站授权码登录，四种连接类型共用已认证绑定、配置版本/停启用/secret 更新与审计。接入及形态边界见 [SOCIAL-PROVIDERS.md](SOCIAL-PROVIDERS.md)，最新证据见 [SOCIAL-ACCEPTANCE-2026-10-14.md](SOCIAL-ACCEPTANCE-2026-10-14.md)；先前记录是对应历史快照。

## Console 完整重构

Kimi K3 max 首轮完成前端主体，第二轮额度中断后 Codex 协助收尾；长久维护的 feature/API/外壳/设计系统结构、写状态机、一次性秘密保护与实际 BFF/Core 验收见 [CONSOLE-REDESIGN-2026-10-15.md](CONSOLE-REDESIGN-2026-10-15.md)。同时修复 Contracts SDK 隐式写重放、BFF 401 generation 失效与上游结果分类、Core Client 非空数组 JSON 契约。没有组织扩展或容量结论变更。

## 六项核心生命周期补齐

App 登录/注册策略、MFA 换绑与恢复码维护、普通用户恢复启用、机密 Client secret 轮换/撤销、签名 key 应急撤销与跨 verifier 缓存失效、有界结构化失败安全事件已实现。正式契约、数据库 v4、管理界面及账号安全页同步交付，详细语义见 [实施说明](SECURITY-LIFECYCLE-2026-10-15.md)，当前验收见 [验收报告](SECURITY-LIFECYCLE-ACCEPTANCE-2026-10-15.md)。持续容量及外部发布门槛不变。

## Go 架构与代码质量复审发现

2026-10-15 的进一步复审确认过两处 P1 凭据并发问题和四项其他工程问题，原始发现及当时未修状态保留在 [架构复审](ARCHITECTURE-REVIEW-2026-10-15.md)。后续整改已实现锁后 User/Session/Identifier/epoch 重验、验证码发出时绑定目标、自助改密限流、日志写失败计数、结构化 SQL 错误及契约生成门禁；两库原时序已由“可复现漏洞”变为拒绝旧证明。新增分层整理、固定持久化 DTO/codec、加密 keyring 和分页迁移工具，详见 [整改验收](ARCHITECTURE-REMEDIATION-2026-10-15.md) 与 [存储迁移手册](STORAGE-KEY-MIGRATION.md)。实际最终 gate 以该轮 evidence manifest 为准；这关闭的是已确认问题，持续 5000 QPS、最终版本长 soak、真实供应商/HA/远端发布等门槛仍未完成。

## 2026-10-16 App 资源、Passkey 和可观测性

新增 App 并发/共享额度预算、WebAuthn 注册/登录/近期账号维护、Prometheus/OTLP 与 worker/outbox 运维包。范围、API、schema v5 与兼容性见 [实施说明](RESOURCES-PASSKEYS-OBSERVABILITY-2026-10-16.md)，最新测试与实际边界见 [验收记录](RESOURCES-PASSKEYS-OBSERVABILITY-ACCEPTANCE-2026-10-16.md)。上方测试数、容量、容器和备份条目仍代表各自历史快照。该轮 Agent 仅完成调研；后续已按用户确认实现 [Agent 身份增量](../technical-design/14-agent-identities.md)，验收单独记录。Core 不依赖模型调用。
