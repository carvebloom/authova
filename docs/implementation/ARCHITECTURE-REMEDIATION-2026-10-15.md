# Go 架构复审整改与本地验收

日期：2026-10-15。范围：Core、Contracts、Console 当前本地候选。未 commit、未 push。前一轮发现和当时的原始失败证据保留在 [架构复审](ARCHITECTURE-REVIEW-2026-10-15.md)，本文件记录后续整改，不能用后续通过结果改写此前缺陷事实。

## 1. 实现结果

| 原问题 | 已落实的规则/改动 | 验证依据 |
| --- | --- | --- |
| F1：旧联系证明重置 | 发出验证码时绑定目标 user/epoch；验证及最终写入在 user 锁下重验 epoch、状态和标识当前归属；无绑定的历史 proof 失败关闭 | 两库受控并发；邮箱/手机的正常、换绑、重新分配、epoch、旧 proof、两个独立 reset 流程和标识 ABA |
| F2：失效 session 改密 | 统一 LockedSession：user→session 行锁下复核状态、epoch、时效；锁前 KDF 后继续比较原密码版本 | 恢复码撤销先提交的原时序；单 session 撤销与改密、MFA 换绑、恢复码、删除、解绑交错；失败不写用户/成功审计 |
| 同类敏感入口 | MFA 维护、绑定确认、删除、解绑、退出使用一致的锁后重验或锁顺序 | 真实事务及原有安全生命周期回归 |
| F3：自助改密限流 | KDF 前 App/user 5/min、App/source 20/min；拒绝 429，依赖不可用 503 | 实际 Cookie/CSRF HTTP；调用计数与 User 读取计数证明未进入 SelfPassword 的 KDF 前读取 |
| F4：输出失败计数 | 直接取得 slog Handler.Handle 的错误；分别 recorded、attempted、written、write_failed、queue_dropped；继续有界非阻塞 | ErrClosedPipe、背压、并发关闭、计数总账/race |
| F5：错误诊断 | SQL adapter Error 保留 operation、driver、SQLSTATE/编号、transient、原 cause 与稳定 domain 分类；Error/LogValue 隐去 SQL/参数/驱动原文 | errors.Is/As、PG/MySQL 代码、context/commit unknown 与日志泄漏检查；已有真实数据库负面回归 |
| F6：契约漂移门禁 | Contracts 重建 schema 和 Go 并逐字节比较；Console 固定输入 snapshot+SHA256，独立生成 Go/TS 零差异；组合检查核对权威源 | 两仓 CI/Makefile；三仓本地候选 hash 一致与真实 HTTP SDK/生成 DTO 组合测试 |

`LockedSession` 是同一事务中的校验 helper，不是新增认证服务。可选 Flow → 可选 App owner → User → Session 的顺序写入运维说明。单 session 撤销可以只锁 session，但不能随后反向锁 user。KDF 继续在数据库锁外。

## 2. 可维护性改动

- AppTx 按管理、Client、User、Session、Flow、协议、联邦、签名、审计和幂等能力组合，继续绑定同一个 App/事务。凭据校验及 Client/连接编辑依赖更小接口；没有拆分数据库原子提交。
- Client PATCH、Connection create/PATCH 的规则移入应用层。对应 JSON 和 disable/session revoke 的 reason 在进入写事务前解码；HTTP 保留路由、鉴权边界、响应和脱敏。没有声称管理 HTTP 的全部业务编排已经清零。
- FlowStep/FlowPurpose 使用有限类型及命名常量，密码重置/标识绑定完成有固定 transition helper，拒绝错误目的、步骤和重复完成。没有引入可配置工作流引擎。
- Service 增加构造时的小时钟函数；应用层的 flow、session、MFA/secret 时间判定可确定性测试。SQL、协议引擎和真实浏览器继续使用真实时间，不宣称全系统虚拟时间。
- 内层独立冻结 persistence DTO + codec v1、外层带 key ID 的密文；支持旧 gob/旧密文读取。领域模型不再直接决定写入格式，转换变化必须显式审查。
- 管理 token/session 的所有权与失效状态、App/RLS/复合约束、提交未知结果和 secret 一次展示语义保留。首版仍没有组织/业务 RBAC/插件市场服务。

这是有限整理；Service 部分依赖仍可见、少量 repository bool 参数与领域/API 结构复用还存在。无需为此机械地重写每个已有入口，后续随具体用例迁移。

## 3. 存储迁移

完整操作、安全边界和恢复步骤见 [存储格式与密钥迁移](STORAGE-KEY-MIGRATION.md)。新增 `authova rewrap`：默认只读验证，显式写入时每页 1–500 行、行锁 + CAS + 原子提交；出错不输出成功续跑 cursor。

真实两库的小数据演练：16 种表、17 个平台/App 表作用域、每库 19 条合成 payload；制造旧直接 gob 和无 key ID 密文，逐行续跑、损坏后整页回滚、全量 inventory、移除旧加密 key 后读回全部 payload，验证凭据/epoch、邮箱、外部身份、Client secret 查找不变。

查找/协议根不轮换；尤其外部 subject 没有原值，不能离线重算。原根仍作为查找根存在，不能宣称旧根已密码学擦除或根泄露处置已完成。旧二进制不能读全部新写入，部署必须安排维护窗口，Core/worker 一起升级。

## 4. 验收方法与证据

证据目录：[architecture-remediation-2026-10-15](evidence/architecture-remediation-2026-10-15/manifest.json)。

- 原缺陷先以“应该拒绝”的永久回归运行，得到预期的红灯，再修复；`regression-before.log` 仍保留。
- `scripts/local-quality.py` 创建随机命名的独立 PG/MySQL 测试数据库、配置真实受限 runtime 账号 DML 和一个新测试根，迁移 v4，运行 Core 全量 race 和组合测试，最后删除数据库/临时 key。无需改 demo 库。
- 首次复用历史共享测试库的全量执行失败于 outbox：该库存在历史开发根/测试 fallback 根加密的数据。只读探针确认了不同根的混写，原失败日志和诊断计数保留；没有删历史数据来制造通过。独立环境的通过结果单独记录。
- 前端检查覆盖 typecheck、lint、format、30 文件/205 项测试和生产 build；Console Go 使用真实 Valkey race。Contracts 做 schema lint、Go vet/race、Go/schema 生成零差异。
- 组合测试实际调用 Core HTTP，使用选定 Contracts SDK 和生成 DTO 检查 App、认证策略、用户、Client、key 列表，以及 policy PATCH/version conflict/problem 响应。它不是 36 个 operation 的完整 OpenAPI 合规覆盖；独立仓库的 CI 生成检查也不是远端发布验收。

最终结果：Core 14 个测试包、178 个叶子检查通过，其中本轮新增 60 个；另有跨契约组合的两库 2 个检查通过。Console Go/真实 Valkey race、30 文件/205 项前端测试及 build、Contracts fresh race/lint/生成检查、Core/Console vet/staticcheck、两库迁移演练均通过。实际 Chrome 5/5 通过。Core 漏洞扫描未发现可达调用漏洞，另报告所依赖模块中 4 项未被当前调用路径触达的漏洞，不能称全部依赖零漏洞。

`TestCapacityRestore`、`TestProtocolSoak`、`TestRestoredEncryptedFacts` 是本轮未启用的专项场景，明确列为 skip，不计入 178 项通过检查；命令包和 port 包无测试也单独列出。新增 60 项仍是所选场景分母，不是身份系统安全覆盖率。

另对临时副本注入 Console Go 生成漂移、消费者输入 hash 漂移、Contracts Go 生成漂移，三个门禁均拒绝。第一次组合断言曾错误期待基础 SDK.Request 将 HTTP 409 返回为 Go error；修正为 SDK 的真实响应语义后通过，原测试错误日志保留，SDK 行为未为此改动。最终 gate、计数、源/二进制 hash 见 evidence manifest。

## 5. 性能测量与未完成边界

新增 `BenchmarkTokenProfileCPU` 直接执行现有 profile 的解析/验签/claims 修正/重签名逻辑，数据库事实固定，仅测 CPU/分配。在本地 Apple M4 Pro、darwin/arm64、Go 1.27.2 的一次 3 秒样本约 **1.637 ms/op、42,146 B/op、460 allocs/op**；pprof 中 RSA signing 累计占样本约 71.75%。输入 token 的首次引擎签发、HTTP、SQL、并发和真实业务分布都不在该微基准内。

因此重签名是签发路径的真实额外成本，下一步应评估上游 claims/profile 扩展点；本轮保留已通过的 token typ/aud/at_hash 和验签规则。没有为了减少 CPU 删除校验。顺手将合法签名但 jti 类型错误的处理改为失败关闭，避免类型断言 panic，并有回归测试。

**持续 5000 QPS 的历史 gate 仍未通过。** 本轮没有取得独占生产画像资源，也没有完成目标容量或最终版本长时间 soak；该微基准不能用来推断在线内省容量。千万级迁移窗口、HA/故障转移、真实供应商、OCI/远端 CI/发布及实际部署验收也未因此完成。
