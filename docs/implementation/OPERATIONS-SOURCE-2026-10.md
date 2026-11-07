> 历史来源记录：本文保留迁移前的版本与验收描述，不是当前仓库的迁移或部署指令。当前请见 [版本说明](../migration/VERSIONING.md) 和 [运行说明](../migration/RUN.md)。

# 运维与安全边界

更新：2026-10-18。当前身份生命周期整改与 schema v8、完整隔离恢复顺序见 [整改与恢复手册](IDENTITY-REVIEW-REMEDIATION-2026-10-18.md)。此处描述已实现的软件机制与部署操作；完整生产 HA/灾难恢复验收报告另行记录，不能从本地 Demo 推断。

## 启动与权限

核心 `authova serve`、后台 `authova worker` 与迁移 `authova migrate` 分开。`all` 仅用于本地便捷运行。迁移使用独立 `AUTHOVA_MIGRATION_DATABASE_URL`，runtime 必须非 PG superuser/BYPASSRLS/table owner/role member，不得持有 schema CREATE；实际 search_path 必须仅解析 public 应用 schema，拒绝额外 schema 的表遮蔽；24 张 App 表必须 FORCE RLS 且实际 app_scope 表达式一致。检查 clean version 8 和八个版本 checksum。MySQL runtime 只授作用域 SELECT/INSERT/UPDATE/DELETE，不授全局 DML、DDL、动态特权、角色或 grant option。连接 Ping 最长 5 秒，启动 schema/权限核验最长 10 秒。一个部署只选择一个事实数据库。 生产模板默认 Core/worker 共用 runtime DSN；部署时可给 worker 进程单独覆盖 AUTHOVA_DATABASE_URL：仅授 outbox、outbox_dispatch SELECT/INSERT/UPDATE/DELETE、authova_schema/authova_migration_history SELECT 和 schema USAGE，即可领取/完成消息并通过启动检查。禁止给 worker migrator 权限。worker 仍持有当前软件主密钥，此建议只限制 DB 权限，不宣称进程完全隔离。

生产设置 `AUTHOVA_MODE=production`、HTTPS 的 `AUTHOVA_BASE_URL` 和 `AUTHOVA_CONSOLE_URL`。TLS 由受信 Ingress 终止；发行服务用 `Secure` Cookie，并不采信 Forwarded/X-Forwarded-For 头。若 Ingress 汇聚来源 IP，限流画像与客户端 IP 策略需在该部署单独配置验证；App/标识/flow 限流仍有独立作用域。Valkey 为生产限流和 Console 共享会话必需，故障明确拒绝相关请求。指标接口应由部署网络限制访问。

主密钥通过 `AUTHOVA_MASTER_KEY_FILE` 提供 32 字节随机值；Console 使用独立 `AUTHOVA_CONSOLE_KEY_FILE`。二者不要提交仓库。主密钥丢失不能从数据库恢复密码、私钥或提供方配置。数据库备份与密钥备份分别保管，恢复必须包含正确密钥版本。当前是软件密钥方案，不宣称 HSM/KMS 隔离。

## schema 升级

嵌入迁移按版本、checksum、数据库锁和 dirty marker 执行。版本 1 → 2 在维护窗口停止 App/Client 配置写入：增加独立 issuer、Client 类型约束、单一当前 signer 和协议 consent。两后端的物理迁移独立；当前 PG 多语句也逐条执行，不宣称整次迁移原子，MySQL DDL 隐式提交；失败不会自动清除 dirty marker。真实旧 v1 schema/加密 Client → v2 issuer/Client 类型回填已经在两库验收。

遇到 dirty schema：停止发布，检查执行到了哪一步；优先恢复迁移前数据库/密钥备份，再执行验证过的修复。不盲目删除 marker 或再次运行多语句迁移。运行二进制拒绝不兼容/dirty schema。schema 2 向 schema 1 回滚不能靠降版本号，使用迁移前备份及兼容二进制。

schema 6 → 7 增加 `outbox_dispatch` 及索引，在维护窗口停止全部旧 Core、worker、cleanup 和写入者后迁移；回填待处理 App 的最早可用时间，保留 payload、attempts 和完成状态。旧写入程序不会维护新调度表，不支持混合版本写入或直接降版本回滚。失败按 dirty schema 流程恢复备份。存量 outbox 较大时应预先评估索引建立时间。

## 撤销与密钥

内省从主库新快照读 App/Client/User/Session/family 状态，不缓存 positive active。读取前已提交的退出、封号、暂停可见；并发读取的边界为该快照。资源服务自行缓存内省会扩大窗口。离线 JWT 仍可能被接受至 TTL 和时钟容差结束。

内省快照 adapter 会先释放 DB 连接再验签。PG 使用参数化的事务内 App 作用域和只读管道事务，错误后不把未确认 idle 的连接放回池；MySQL 连接固定 autocommit=1、session Repeatable Read，拒绝关闭 autocommit、降低 session isolation 或旧 tx_isolation 覆盖。无需 schema 升级；有自定义 MySQL DSN 时应先核对这些参数。常规写事务仍为 Read Committed。

数据库准入保持每实例 App/业务共享/全局硬上限和管理预留；业务请求可在无 DB 许可、无 DB 连接时进入最长 1 秒的有界 FIFO 队列。HTTP inflight 仍限制总请求，HTTP/KDF 不增加等待。等待上限为全局 `max(8,8×pool)`、单 App `min(4×effective DB limit, global waiting bound/2)`，管理请求不进入业务队列。`authova_resource_wait_duration_seconds` 可区分吸收的抖动与 timeout/queue_full/cancelled/policy_changed；持续饱和仍明确拒绝，不能依赖队列补偿长期容量不足。方案及实际容量结果见 [整改记录](CAPACITY-REMEDIATION-2026-10-17.md)。

内省路由缓存及公钥缓存的 miss 会合并同一项的在途加载，公钥按 App/epoch/kid 分组。路由/资源策略的元数据读取计入业务共享预算，另限制为 `max(1,business DB capacity/8)` 个活跃读取，不借用管理预留。不要把 public-key 或路由元数据缓存理解为 allow 缓存；当前 Client、用户/会话、Agent/接入/grant 和 key epoch 仍由主库快照判断。

签名轮换在 App 锁保护的事务中切换 signer，数据库额外保证至多一个 current key。正常退休旧公钥保留 24 小时，发行 access token 默认 5 分钟、ID token 10 分钟。密钥泄露应停止发行并按事件恢复；正常轮换不等同于应急撤销。

平台首次引导：`authova bootstrap < private-bootstrap.json`，部署已存在 operator 时拒绝。主机管理员恢复：`authova recover-operator < private-recovery.json`，更新该 operator 密码/TOTP 并撤销全部管理会话，事务内记录 `platform.operator_recovered`。这是拥有部署 DB 凭据及主密钥的本地恢复操作，没有无鉴权的 HTTP 后门。

## 邮件、短信与 outbox

邮件使用 SMTP，生产强制 STARTTLS。短信接受受控 HTTP adapter，传 `{message_id,app_id,destination,body}` 和 Idempotency-Key；生产仅允许 HTTPS/public upstream。提供方 accepted 不等于实际送达。配置缺席明确失败，不绕过标识验证。

outbox 与挑战同事务写入，秘密 payload 加密。领取使用 SKIP LOCKED、2 分钟租约与 attempt；以持久化 outbox_dispatch 按 App 轮转，按空闲槽位领取；每 worker 最多 4 个并行投递，同一 App 最多 2 个。该预算按 worker 实例计算，N 个实例时一个 App 最多占用 2N 个槽位，不是全局固定 2 个；单次 DB 5 秒、提供方 10 秒。新 OTP 消息随 challenge 在 5 分钟到期；历史无 Expires 消息保守使用 Created+15 分钟，过期确认码依然由认证层拒绝。SMTP 取消关闭连接，worker 取消后不再开始新提供方调用。完成更新按 attempt fencing，旧 worker 不得覆盖新处理。状态 0 待处理/重试、1 提供方受理、2 到期/终止。SMTP 不能证明端到端 exactly-once，响应丢失可导致重复邮件，验证码仍仅能消费一次。

## 本地例子

`make dev-env infrastructure migrate bootstrap` 后分别运行核心、Console 和 RP；或首次从干净环境 `make demo`。只监听 127.0.0.1 的 18080/18090/18088，邮件沙箱 58025。开发数据库示例密码为公开 fixture，不用于生产。遇到端口冲突应选择本项目自己的空闲入口，不能结束其他项目进程。

## 审计与清理

`authova cleanup` 按 App 的受限事务分批删除超过 24 小时的过期临时记录，遵循 code/challenge/flow 与 token/family/session 的 FK 次序；每表每 App 每批最多 500，保留 User 与 issuer；同时清理完成 outbox、过期 management_sessions/platform_idempotency 和平台/App 审计。不会把尚有活跃子记录的 family/session 强行删除；计数只含成功提交的删除。审计默认保留 90 天，部署须按其要求调整/执行保留政策。提供方终止状态与已受理状态分开，不能把死信计为成功投递。

## 已验证的恢复范围

2026-10-14 本轮实际备份/恢复了 PG `authova` 和 MySQL `authova_capacity` 全量库至随机新库；两库各 COUNT=12,000,000、1000 个解密 User 与源事实精确比对、RSA 私钥解析通过。PG 未压缩 dump 15,559,012,514 字节，备份 73.78 秒、恢复 306.86 秒；MySQL gzip dump 8,198,700,593 字节、最终恢复 1359.65 秒。MySQL 重用此前备份，原备份时长未保留，不记为 0 秒。

MySQL 在 1.5 GiB 和 3 GiB 容器上均有恢复 OOM 失败；提高内存本身没有解决问题。最终脚本对本工具自己的 mysqldump 格式移除表级 LOCK/UNLOCK，并每约 16 MiB INSERT 提交。大表事务/锁累积是原因推断，批次调整有效是实测事实。失败诊断保留在仅管理员可读的 `.dev/backups/`，公开证据不含 dump/凭据/用户密文。

这证明单机恢复流程与样本事实，未完成跨主机恢复、HA、增量/WAL/binlog 恢复、业务恢复演练或客户 RPO/RTO。完整记录在 `evidence/review-2026-10-14/review-capacity-restore.json`。

## Agent 备份恢复后的隔离

完整旧快照恢复须先执行 [普通身份与 Agent 的完整恢复隔离流程](IDENTITY-REVIEW-REMEDIATION-2026-10-18.md#旧备份恢复的运行顺序)。`quarantine-identities` 已包含本节的 Agent 隔离；不能只执行 `quarantine-agents` 就开放普通用户或平台管理流量。本节保留 Agent 专项机制及其历史验收工具说明。

恢复旧备份会丢失快照之后的撤销、接入停用和密钥轮换。不得直接把恢复副本切为对外服务。Agent 的恢复顺序：

1. 隔离目标入口，停止目标库全部 Core、worker、cleanup 和其他写入者；保留源库、dump、主密钥、所有 keyring 版本及校验摘要。不要在原库覆盖试恢复。
2. 完整恢复至新库，检查 schema clean/version/checksum、运行角色、用户与签名密钥/Agent payload 解密。用配套二进制及原 issuer；此时仍不接收外部请求。普通用户与 operator 须按完整恢复手册隔离和带外审批；不能用一次密码/TOTP重置代替身份与当前权限核验。业务目录仍需独立核对。
3. 配置目标库及正确密钥，在本机执行 `authova quarantine-agents < private-recovery.json`。输入示例（不含密钥）：

   ```json
   {"reason":"Restore from the approved backup checkpoint", "confirm":"disable_all_agents"}
   ```

4. 保存命令输出计数和审计。命令关闭全部 Agent/installation，清除机密 Agent 当前及 previous secret，并撤销 active grants。先在事务内封禁单个 Agent 并清除旧密钥，再按 100 条分页事务关闭接入、撤销授权并审计，避免大事务。后续页失败仍保持已提交的封禁；输出已经完成的计数，保持隔离并核对后重跑。重复执行不重复提升已处理对象版本。该操作不改写千万级用户表，也不清除用户/审计归属。
5. 逐项与业务权威来源核对恢复前后的租户成员、owner、资源和 scopes。给机密 Agent 轮换新 secret（grace=0），重新批准并启用身份和 installation；自主模式 renew 出替代 grant，委托模式重新登录并同意。重新启用旧配置本身不恢复旧凭据和旧委托。
6. 在实际资源服务验证旧凭据、旧 refresh、跨 App/租户及撤销拒绝，确认离线验证器的最大过期窗口/缓存策略，再逐步开放流量。已签 JWT 不因本地主机维护而即时失去密码学有效性；在线内省/资源 SDK 会读取当前状态并拒绝。

本地可复跑验收使用 `scripts/agent-acceptance.py`：`prepare --users 12000000`、`seed`、`restore`、`verify`，每步都提供相同的 `--directory .dev/<private-run>`。真实 dump/凭据仅写入私有目录，操作使用随机新库，不复用 Demo。`verify` 先在源库制造备份后的撤销，再比对恢复快照，最后运行实际恢复隔离 CLI；它会消费恢复夹具并关闭恢复副本，重跑应从保留 dump 建立另一个新副本，不能盲目重放已使用的 refresh。

`soak --duration 1h` 依次执行真实 Agent 与普通 OAuth 的独立长期流程（每项一小时，两库在各项内并行）；`capacity --driver postgres --duration 5m --qps 5100` 使用正常 120 秒 Agent token 和真实 refresh，另支持 mysql。容量生成器为开放环，报告完整错误、掉队列与包含排队的延迟。默认 App DB 并发预算为 32，可用 `--app-db-concurrency` 显式测试其他画像；不能通过取消隔离/撤销检查或忽略 429 来获得通过。五分钟画像不能替代持续一小时的 5000 QPS 证明。

## 交付运行包与复验

生产包见 `deploy/README.md`。`make demo` 会创建独立 PG `authova_demo`，只建立两个演示 App 和实际 Client；用户在浏览器注册。`make integration`/`make verify` 使用独立测试库，不向 Demo 加入测试 App。`make verify` 必须实际连接两库，不能靠默认 skip 获得成功。

`python3 scripts/container-smoke.py --agent-directory .dev/<owned-agent-run> --valkey-fixture .dev/<owned-tls-fixture> --evidence .dev/<smoke-evidence>.json` 使用独立夹具密钥和已有测试专用 DB，容器启用 production 配置验证、UID 10001、只读文件系统和 capability 移除，测试 HTTP/鉴权/CSRF。Valkey 走真实 mTLS/独立 ACL；origin 为 HTTPS，但入口请求走隔离本地端口，**不是外部入口或 SQL TLS 链验收**。生成私有 env 在临时目录中，结束删除且不打印秘密。

存储加密支持带 key ID 的 keyring 与维护窗口分页重加密，见 [存储迁移手册](STORAGE-KEY-MIGRATION.md)。查找/协议根继续保持稳定，不能直接替换主密钥文件。正常 App 签名轮换有独立机制。Valkey adapter 原生支持验证 CA/SAN 的 TLS、命名 ACL 用户及可选 mTLS；生产模式强制 TLS 和命名用户，禁止明文降级或跳过校验。部署文件和轮换步骤见 [Valkey 与 outbox 运维说明](OUTBOX-VALKEY-2026-10-18.md)。

`/metrics` 记录 HTTP 请求、实际响应状态类别、5xx/panic、inflight 和累计处理时长；另有 Go heap/对象/GC/goroutine、DB pool 和等待指标。`authova_failures_total` 包含 5xx 和 panic，`authova_panics_total` 单列 panic。累计平均时长不能代表 P99，入口需记录延迟分布、outbox/provider 和全部依赖故障。重复协议事务错误日志每秒最多一次并附窗口计数，审计事实不采样。

Core/BFF 每请求上下文最多 15 秒。Core 同时处理请求默认最多 1024（AUTHOVA_HTTP_CONCURRENCY 可设 1–4096），BFF 默认 256；超出快速返回 503/Retry-After，liveness 不受该限额阻断。这限制等待 DB/上游的请求总量；仍需入口/OS 的 TCP/连接与速率控制，不宣称 HTTP 层能防住所有网络 DoS。KDF 同时工作数由 AUTHOVA_KDF_CONCURRENCY 限制，等待/工作队列总量为预算的 4 倍，超过返回 503。数据库 pool 由 AUTHOVA_DB_CONNECTIONS 控制（默认 64）；按实例数、数据库 max_connections 和其他连接总量计算，不能无界加大连接池。

待完成 Flow 新增 AuthEpoch。升级时已有零 epoch 的主认证证明拒绝继续，用户重新登录。新字段为内部 gob 格式可增加字段，无需 schema 3；旧易受影响二进制不作为安全回滚目标。

内省每次主库读取当前 liveness 和请求 Client 密文/status，解析缓存仅以 App+当前密文 SHA-256 为键且有条数/字节界限；secret/许可变更即时改变解析键。公开 claims 来源是已经验签且与主库 ID/sub/client/sid/expiry/principal 匹配的 JWT，没有缓存 positive active。离线 JWT 的撤销窗口依然由 TTL 决定。

Console 在外部 refresh 前写入共享 pending。旋转结果未知或进程中断时不自动重放旧 refresh，要求重新认证；刷新保存必须 SET XX，不能复活退出会话。App/Client 创建未知结果在同一页面保留 payload/key；完整刷新后先核对服务端，不能承诺页面外意图恢复。


## v4：认证策略、凭据维护与密钥撤销

部署顺序：备份配置和数据库 → 停止旧 Core/worker → 使用专用 migrator 执行 `authova migrate` → 新 Core/worker 启动检查 → readiness 和登录/管理验收。运行中的旧二进制不理解新策略，不允许让旧实例与已启用新策略的实例混跑。v4 修改 App 和 signing_keys 的小表元数据，不回写千万级 users。两库迁移各自保持 checksum；MySQL 使用有固定上限的 VARCHAR 保存六个布尔策略，避免不兼容的 TEXT 默认值。

`PATCH /api/v1/apps/{id}/authentication-config` 要求完整 policy、当前 config_version 和 reason。注册开关控制新用户；external_registration_enabled 额外控制第三方首次建号。Email/SMS 开关控制验证码登录，不禁止已有用户验证联系方式，也不等同于 SMTP/SMS 就绪状态。管理系统 Client 保留密码入口且强制 MFA；关闭公开注册不会允许创建管理身份。策略在接受证明、第三方回调和登录完成时按当前事务状态重新检查；它不宣称撤销所有既有会话。

Client secret 正常轮换允许 0–86400 秒重叠。已有活跃的 previous secret 时拒绝覆盖，须先显式撤销或等待到期。secret 撤销控制后续客户端认证；已签发令牌仍按其状态和 TTL 验证。泄露时可先禁用 Client，在线内省读取禁用状态；离线业务仍须遵守自己的刷新/撤销策略。轮换结果未知时先 GET Client：版本变化说明凭据可能已更换，GET 无法找回明文。确认旧 previous 的状态后才能显式执行下一次轮换，不能盲目重试。

正常签名轮换保留旧公钥 24 小时。泄露处置使用 `GET .../keys` 和 `POST .../keys/{key_id}/revoke`，提供读取到的 config_version 与 reason；撤销当前 signer 会原子建立替代 signer。事务同步提升 App key_epoch，在线内省从原主库 liveness 语句读取 generation，不依赖路由缓存或可能丢失的 pub/sub 通知。提交后开始的新状态读取拒绝已撤销 kid；与提交并发的读取遵循数据库快照边界。JWKS 不再返回被撤销公钥。App 管理员若持有该 key 签发的管理 token，也需重新认证；平台 operator 是独立恢复路径。

**外部离线 JWT 验证器必须刷新其 JWKS 并拒绝撤销的 kid。不能承诺它们立即失效，也不能仅等待合法 token 的 TTL——泄露私钥可被用于伪造新 token。** 应在实际资源服务上演练缓存清除/拒绝规则与最大窗口，再恢复流量。存储加密轮换使用上述维护窗口迁移；查找根不在此轮换范围。

MFA 恢复码验证后必须确认新 TOTP，才可继续登录，并得到替换后的恢复码。账号安全页的主动换绑要求最近认证和当前 TOTP/恢复码；准备换绑不会提前消耗最后一个恢复码，确认时检查凭据 epoch 和原证明。恢复码重新生成必须验证当前 TOTP；只有恢复码的用户先换绑新认证器。成功后旧恢复码和旧会话失效，新恢复码仅展示一次，相关通知只表示写入 outbox，真实送达仍取决于部署提供方。丢失全部证明时没有无鉴权 HTTP 重置入口。

安全事件通过 stdout 的单行 JSON `msg=security_event` 输出，与事务内管理审计分离。事件含服务端 request_id、App、已知 Client/主体或 App 作用域 HMAC、来源 IP 的 HMAC、动作、结果与原因分类；不记录密码、验证码、恢复码、token 或原始请求体。认证/MFA/恢复/验证码请求、联邦流程及管理拒绝均有对应路径记录。成功在线内省不逐请求产生日志。队列固定 1024；饱和时丢弃并累加 `authova_security_events_dropped_total`，成功写入累加 `authova_security_events_written_total`，不能把该日志视为无损合规审计。采集器需独立设置存储、保留、访问控制和磁盘轮转，并对 dropped 增长告警；对关键管理变更的原子审计没有采样或队列丢弃。


## 凭据事务与诊断指标（2026-10-15）

敏感写入的锁顺序为可选 Flow → 可选 App owner 锁 → User → Session。KDF 与网络在锁外；锁后重新验证用户状态、epoch、原密码版本及当前 session 的状态/时间。单会话撤销可以只持 session 锁，但不得再请求 user 锁。SQL 写事务为 ReadCommitted；不要改成读取旧快照后继续写入的实现。

重置验证码从发出时绑定用户及 epoch；验证和提交均复核标识当前归属。联系方式重新分配、换绑、MFA 维护/撤销和另一次重置均可使旧证明失效。

自助改密在 KDF 前应用 App/user 和 App/source 预算；拒绝返回 429，依赖故障返回 503。全局 KDF 资源限制仍独立保留。

安全事件指标增加 `authova_security_events_recorded_total`、`attempted_total`、`write_failed_total`（后两项同一 `authova_security_events_` 前缀）；written 只表示 Handler 成功返回，dropped 表示队列/关闭期间丢弃。停止并排空后 recorded=written+write_failed+dropped。监控输出失败和队列丢弃；日志依然不具有崩溃无损或远端持久化确认语义。

SQL adapter 的结构化 Error 保留 errors.Is/As 因果及受控 driver/code/operation/kind。日志使用其 Error/LogValue；不要直接序列化 Unwrap 原始驱动消息，其中可能包含 SQL/参数。transient 仅用于诊断，不是允许重放写入；commit unknown 必须按原规则读回。

## App 资源、Passkey 和遥测配置（schema v5）

升级前备份并协调 Core/worker/Console/契约客户端。登录策略 PATCH 从六个布尔字段变为七个（新增 passkey_enabled，迁移默认 false）；旧客户端必须更新。资源预算每实例并发限制与 Valkey 共享额度分别生效。Passkey 要求稳定 HTTPS 域名，开发用 localhost；IP 地址不能作为 RP ID。详见 [实施说明](RESOURCES-PASSKEYS-OBSERVABILITY-2026-10-16.md) 和 [监控配置](../../deploy/observability/README.md)。
