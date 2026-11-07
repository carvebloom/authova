# 本地验收：资源隔离、Passkey、可观测性

日期：2026-10-16。状态：本轮实现和下列本地验收通过，未 push、未远端发布。Agent 仅完成调研方案，没有增加模型依赖或自动执行能力。

后续说明：本文记录资源/Passkey/可观测性当时的验收；随后批准的 Agent 身份实现与验收见 [Agent 验收记录](AGENT-IDENTITY-ACCEPTANCE-2026-10-16.md)。本文历史测试数和当时的 Agent 范围不作为后续状态。

## 1. 证据范围

本轮使用真实 PostgreSQL 17、MySQL 8.4.11、Valkey、Mailpit、Chrome、Prometheus 和 OpenTelemetry Collector。数据库回归在随机独立库中执行、完成后清理；原演示库单独备份升级，不混入容量 fixture。

机器摘要和校验信息见 [summary.json](evidence/resources-passkeys-observability-2026-10-16/summary.json)。完整原始日志、数据库备份和凭据保存在 git 忽略的 `.dev/resources-passkeys-observability-20261016` 与 `.dev/passkey-demo`，不公开分发。文档中的测试数是实际执行记录，不以覆盖率或 package 数代替场景验收。

| 检查 | 实际结果 | 不能据此推导的结论 |
|---|---|---|
| schema 005 两库迁移 | PG/MySQL 都通过，受限 runtime 账号 race 回归通过；新表纳入 key rotation 演练 | 客户数据库升级、在线 DDL/RPO/RTO 已验收 |
| Core 全量回归 | 247 个叶测试通过；容量恢复、长 soak、独立恢复库检查 3 个专项明确 skip | 当前版本已完成长时间容量/恢复专项 |
| 后续小范围补充 | transport/resource/observability/demo-rp 共 47 个叶测试通过，无 skip；包含全量回归之后新增的 4 个 Passkey-only/readiness 模板场景 | 与上行完全独立的 47 个新功能场景（其中存在重叠） |
| 新增两库业务场景 | 6 个 Passkey 生命周期子测试 + 2 个资源隔离子测试通过，下面列出内部行为断言 | 真实硬件 Passkey/FIDO 认证、所有移动平台兼容 |
| Valkey | 两个真实 limiter client 同时争抢同一预算，准确准入 20/100，另一 App 仍可用 | 跨地域部署的限流 HA 或全局并发 semaphore |
| Contracts | 生成一致性/OpenAPI lint/Go race、两库真实 HTTP SDK 组合通过 | 远端 CI/正式版本兼容发布 |
| Console | 31 文件、235 个前端测试；TS/lint/Prettier/build 通过；Go BFF 真实 Valkey 19 个叶测试通过 | 每一个管理页面都重新完成浏览器回归 |
| 最终构建 Chrome | 3 条业务流程通过：Passkey全流程及未知提交恢复；新 Console 资源保存/恢复；原 18090 管理登录与新配置读取 | 真实设备、云同步、硬件密钥或第三方供应商已验通 |
| 噪声 App 检查 | 60 秒、噪声 9000 次、受保护 App 600 次、管理读取 30 次，结果见下文 | 持续 5000 QPS 或千万活跃用户容量 |
| 监控 | Prometheus 真实抓取 Core/worker，OTLP Collector 收到真实 spans；Go 测试解码实际 protobuf，验证字段省略和导出故障独立性 | Grafana Dashboard 已真实渲染、生产告警路由和遥测 HA 完成 |
| 告警 | promtool 解析规则并通过 3 个行为测试（目标故障、共享队列延迟聚合、空队列）；Collector validate 通过 | 每种告警都完成真实故障注入 |
| 依赖与秘密 | 最新 govulncheck：Core 可达 0、导入包 0；Console/Contracts 无漏洞；SBOM/真实上游 NOTICE 已更新；候选文件未匹配 8 项私有凭据值 | 独立渗透审查或完整法律认证 |

Core 仍有一个**未导入、未调用**的模块级公告 GO-2026-5932：`x/crypto/openpgp` 已不维护、无修复版本。当前使用的是该模块中的其他包，不导入 openpgp。记录保留在 verbose 扫描日志，不能表述成“依赖树绝无公告”。本轮将 x/net 升至 v0.60.0，已消除扫描发现的 5 项可达 HTTP/2 公告。

## 2. 已验证的具体安全行为

### App 资源隔离

- App HTTP/KDF/DB 上限彼此独立；许可释放幂等，取消和策略读取失败时拒绝准入，缓存读取合并。
- 使用真实数据库持有 App 1 事务，App 1 新业务请求返回 429，App 2 仍成功，平台管理读取利用预留容量成功。
- HTTP 许可耗尽、App 速率预算、App 每小时邮箱挑战预算分别生效；验证码额度独立于单目标地址限额。
- 完整六项校验、App 配置 CAS、旧版本冲突、不完整配置拒绝，App 2 配置保持原值。
- OAuth adapter 原先把 App 数据库准入拒绝统一包装成 503，本轮修复为 429；真正依赖故障仍 503。

### Passkey

测试独立生成 P-256 私钥、COSE 公钥、CBOR attestation 和签名 assertion，生产库执行实际协议解析与密码学校验。

- 正确注册、登录、计数更新；错误 Origin、缺少 UV、错误 userHandle、challenge、RP hash 与签名都拒绝。
- 注册/登录重放拒绝，并发完成同一挑战仅一次成功；非递增非零计数拒绝。
- App 1 凭据不能在 App 2 的有效挑战中解析；数据库跨 App 读取也拒绝。
- 挑战开始后策略关闭，完成时拒绝；App 要求 MFA 时 Passkey 不绕过 TOTP。
- 最后登录方式保护、凭据删除版本冲突、其他会话撤销、当前会话保留；未完成的旧注册挑战不能跨 Epoch 恢复凭据。
- 验证地址后无密码注册，完成实际 session；资料清除移除凭据；加密重包和旧密钥退役覆盖新增表。
- Chrome 虚拟认证器完成邮箱验证 → Passkey 注册 → consent/PKCE/RP → 再登录；同邮箱在两个 App 获得不同 subject。
- Chrome 实际执行账号新增认证器、删除；模拟 finish 已提交但连接丢失，界面冻结、禁止自动重放，明确读回后确认已创建再继续。
- 只开启 Passkey 时的模板、注册入口与不可用域名显示另有测试。原 IP 地址演示不放宽 RP ID 校验。

### 遥测

- Prometheus histogram/text/gzip 可被真实 parser 读取；原 URL、未知 method、密码/SQL错误文本和 Authorization 不进入相关导出字段。
- 实际 OTLP HTTP 接收器解码 protobuf，检查 root/upstream spans 和失败状态；不可用 exporter 不阻塞 span 生产。
- outbox 读取失败标记 snapshot_valid=0，保留旧值且明确新鲜度；停止轮询时等待 goroutine 退出。
- 共享 outbox 计数以 max 聚合，超过 10000 标记 lower bound，不将每个副本的相同队列相加。

## 3. 60 秒噪声流量结果

独立 localhost 数据库、同一台开发机、同时存在本地回归和 Docker 依赖。App 1 限额为每秒 25 次，施加每秒 150 次 `/keys` 请求；App 2 每秒 10 次探测；平台每两秒读取一次资源配置。

| 类别 | 总请求 | 结果 | p95 | 最大耗时 |
|---|---:|---|---:|---:|
| App 1 噪声 | 9000 | 1450 成功、7550 返回 429 | 18.86ms | 319.74ms |
| App 2 受保护请求 | 600 | 600 成功 | 18.46ms | 279.11ms |
| 平台管理读取 | 30 | 30 成功 | 19.19ms | 20.53ms |

测试结束按当前版本恢复原预算。它只证明这一请求类别、速率、时长和本机条件下的隔离行为，不能推出密码吞吐、5000 QPS、长 soak 或共享物理资源强隔离。

## 4. 运行环境与数据保留

原环境已备份并从 schema 4 升至 5，Core/worker/Console 只重启已核对 argv 的进程。原 2 个 App 的 ID/key/配置版本、6 项既有策略、用户数量、运营账号数量保持一致；新增 passkey_enabled=false。原管理员凭据登录验证通过。

- 原 Console：[http://127.0.0.1:18090](http://127.0.0.1:18090)，原凭据继续可用。
- 原 RP：[http://127.0.0.1:18088](http://127.0.0.1:18088)，issuer 不变。
- Passkey RP：[http://localhost:18188](http://localhost:18188)，独立库与密钥；手动注册自己的真实设备凭据。
- Passkey Console：[http://localhost:18190](http://localhost:18190)，管理员文件 `.dev/passkey-demo/bootstrap.json`。
- Prometheus：[http://localhost:19091](http://localhost:19091)，本地 Collector 14318；测试时 localhost 环境采样率为 1，生产建议按预算配置。

原库备份 SHA256、大小与迁移核对在机器摘要中。没有把独立 Passkey 数据库替换成原库，也没有宣称恢复更早已经丢失的历史数据。浏览器虚拟认证器只属于临时测试上下文；已生成的合成测试账号可用 Email OTP 登录，但其虚拟 Passkey 私钥不会交给用户。

## 5. 作者与剩余门槛

本轮按既有偏好调用 Kimi K3 max，但其返回 `403 weekly (7-day) usage limit`，只留下部分类型/API/策略改动。Codex 接续完成其余前后端、复核与验收；不把本轮标记为 Kimi 独立交付。

后续仍需真实硬件与浏览器矩阵、供应商验收、目标部署的持续容量与长 soak、独立安全审查、HA/灾备/监控路由、实际 Grafana 渲染及远端发布流程。Agent 的工具/审批/用户委托方案待讨论后实施，不计为本轮已交付功能。
