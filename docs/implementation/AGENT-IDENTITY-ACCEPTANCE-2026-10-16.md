# Agent 身份与委托：本地验收

日期：2026-10-16。范围：用户确认的 Agent 独立身份、接入、用户委托与自主运行、撤销、管理界面、资源 SDK 和实际 REST/MCP 示例。已完成下列本地实现与验收，尚未 push 或远端发布。

后续状态：实现基线之后的代码与安全复审、修复及最终门禁另见 [安全复审记录](AGENT-SECURITY-REVIEW-2026-10-16.md)。本文保留该实现基线的原始测试与性能结果。

设计、流程图、数据模型、API 和运行说明见 [Agent 身份设计](../technical-design/14-agent-identities.md)。机器摘要见 [summary.json](evidence/agent-identity-2026-10-16/summary.json)，候选文件及原始日志校验见 [manifest.json](evidence/agent-identity-2026-10-16/manifest.json)。

## 1. 实际交付

- Core 中新增 Agent / Installation / Grant / TokenBinding 四类事实，使用现有 App 事务和 OAuth/OIDC，PostgreSQL、MySQL 都支持。schema 升至 6。
- 用户委托保留用户 subject，并通过 `act` 标明 Agent；自主运行使用独立机器主体。每份授权可单独撤销，不撤销该用户的其他正常会话。
- 接入固定目标资源、权限子集、运行方式和期限。外部租户委托需要预先绑定此 App 的具体用户；成员关系由可信业务系统核实。
- 访问令牌最长 300 秒；签发、刷新和在线内省检查当前身份、接入和授权状态。管理写仅 owner，使用版本 CAS、审计及创建幂等回执。
- Console 提供注册、创建接入、停启用、授权查询/撤销、自主授权更新；用户账号页提供自己的 Agent 授权查询和撤销。
- Contracts 新增 13 个管理操作，当前总计 37 paths / 51 operations / 56 schemas；提供标准库资源校验 SDK、官方 Go MCP SDK 的客户端/服务端示例。
- App 仍是独立业务系统的隔离单位；没有恢复组织目录、业务角色引擎，也没有引入模型推理依赖。

## 2. 检查与证据边界

| 检查 | 实际结果 | 边界 |
| --- | --- | --- |
| 全量 Core race 回归 | 273 个叶测试通过、89 个顶层测试通过、无失败；PG/MySQL 使用随机独立库及受限 runtime 账号 | 3 个独立专项明确 skip：容量恢复、协议长 soak、外部恢复库事实检查 |
| 浏览器发现后的聚焦回归 | 注册授权页修复后，49 个叶测试通过，其中 Agent 24 个，PG/MySQL 均执行、无 skip | 与全量回归存在重叠，不能相加为不同场景数 |
| 最后整理 | Go vet/build、Demo RP 目标测试通过；后续 Core 改动仅 import 分组和示例 RP 浏览器绑定期限对齐 | 没有把格式调整后的构建冒称为另一次全量 race 执行 |
| SDK 组合 | Contracts 资源校验 SDK 对真实 Core，PG/MySQL 均通过；原管理 SDK 组合也通过 | 本地候选组合，未远端发布 |
| Contracts | 生成一致性、正式与草案 OpenAPI lint、Go vet/race 均通过 | 未认证所有第三方客户端的 OAuth 互操作 |
| Console | Go vet/race、真实 Valkey、生成绑定检查通过；33 文件 / 242 前端测试，TS/lint/格式/生产构建通过 | 后续表格 CSS/class 调整单独通过构建、lint、格式及桌面/手机视觉验证 |
| 实际浏览器 | 下节列出的真实登录、创建、核对、接入、注册、授权、访问与撤销 | 未遍历所有浏览器、设备和全部管理页面 |
| 实际 MCP | 官方 Go SDK 客户端完成工具发现和 `list_tickets` 调用，服务端经真实 Authova 内省 | 工单为合成数据；不是 ChatGPT/Claude/Cursor 的互操作认证 |
| 依赖 | Core 可达漏洞 0、导入包漏洞 0；Console/Contracts 无发现；实际上游 LICENSE/NOTICE 与 SBOM 已更新 | Core 保留一个未导入、未调用的模块公告，见下文 |
| 秘密 | 候选文件未匹配所核对的 30 项私有秘密值；密钥、浏览器登录日志及数据库备份不进入公开证据 | 字节匹配不是独立渗透审查 |

Core 的模块级 GO-2026-5932 属于未导入的 `x/crypto/openpgp`，没有修复版本；其他实际使用包不受该调用路径影响。这里没有表述为“整个依赖树没有任何公告”。

### Agent 行为分母

本轮有 **9 个顶层 Agent 集成场景**，都在 PostgreSQL 和 MySQL 执行；其内部参数化断言构成聚焦结果中的 24 个叶测试。另有资源 SDK 的两库组合场景。

| 场景 | 验证行为 |
| --- | --- |
| 自主授权生命周期 | 受限签发、单资源、到期、停用、撤销、明确更新授权 |
| 用户委托生命周期 | 实际登录确认、actor/subject、刷新绑定、不复活已撤销授权、自助撤销 |
| App/租户/用户隔离及审计 | 跨 App 读写和凭据拒绝、绑定用户校验、权限与资源收窄、审计归属 |
| 创建回执 | 幂等创建、创建者隔离、GET 回执不包含密钥 |
| 当前策略与用户绑定 | 不依赖过期 allow；用户和接入当前事实重新校验 |
| 公开客户端与角色 | PKCE 约束、非 owner 不可写；旧 Client API 也不能绕过角色或扩大 Agent 上限 |
| 并发签发与撤销 | 持有共享配置锁时另一签发仍能完成，排他撤销等待已有签发结束 |
| 保留期及清理 | 令牌绑定级联、授权/接入保留窗口、有界清理、可更新的自主接入不被提前删断 |
| 首次注册授权展示 | 注册 POST 直接进入确认页时仍展示 Agent、资源、到期时间和撤销入口 |

测试源位于 `internal/integration/agents*_test.go`。注册展示测试显式种入“已验证地址”状态，OTP 本身由下列真实浏览器与 Mailpit 流程验证；没有把种入状态描述成实际邮件验证。

## 3. 真实浏览器与示例

在最终生产构建的 Chromium 中执行：

1. owner 登录后注册公开 Agent；拦截器先让后端真实提交 201，再中断响应。界面冻结，实际只发一次 POST；通过不含密钥的回执确认创建，再显式关闭。公开客户端回执正确说明不含 client secret。
2. 在管理表单创建 `tenant-browser-acceptance` 接入，绑定已存在的 App 用户；实际 POST 201，并在接入列表读到对应记录。该租户仅为明确的合成验收上下文。
3. 浏览器通过真实 SMTP/Mailpit 接收 OTP、验证地址、注册密码账号。修复后的注册响应展示“工单 Agent”、批准资源和确定的 UTC 到期时间；确认后，真实资源服务返回该用户 subject 和独立 grant_id。
4. 已有账号的另一份委托先成功访问资源，再从账号页勾选确认并撤销。令牌尚未到期时，后续资源访问返回 401，刷新也返回 401；自主授权仍独立可用。
5. 最终运行版本的自主 CLI 经真实 Core 获取正常短令牌，分别完成 REST 和官方 MCP 工具调用。CLI 不输出 bearer token。

桌面与 375px 手机视口实际查看；手机整页没有横向溢出，密集记录在表格容器内横向滚动。修正了首次视觉检查发现的默认按钮外观、视图按钮过度分散和窄屏逐字挤压。

- [桌面管理页](evidence/agent-identity-2026-10-16/console-desktop.png)
- [手机管理页](evidence/agent-identity-2026-10-16/console-mobile.png)
- [用户确认授权](evidence/agent-identity-2026-10-16/consent.png)

示例 RP 的浏览器绑定期限已与 Core 的十分钟授权流程对齐；原先五分钟 Cookie 会导致较慢的首次注册在 Core 仍有效时无法返回 RP。超时和错误浏览器绑定仍然拒绝。

## 4. 两分钟资源路径检查

专用 PostgreSQL 本地库，24 个 worker，每秒计划 100 次请求、持续 120 秒，使用正常 299 秒剩余期限的自主 Agent 令牌。每次访问都经资源 SDK 在线内省，并验证响应中的机器主体、Agent 和 grant。未扩大生产令牌期限。

| 请求 | 完成 | HTTP 200 | 失败/无效响应 | 掉队列 | p95 | p99 |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 12,000 | 12,000 | 12,000 | 0 | 0 | 7.099ms | 8.527ms |

实际耗时 120.025 秒，成功吞吐 99.979 QPS。详见 [机器结果](evidence/agent-identity-2026-10-16/agent-steady-smoke.json)。这是有界稳定性检查，**不是长时间 soak、MySQL 性能测试、持续 5000 QPS 或千万 Agent/活跃用户容量证明**。既有容量门槛的失败结果仍保留，不因本轮功能通过而改变。

## 5. 本地运行与数据保留

- 原 Console：[http://127.0.0.1:18090](http://127.0.0.1:18090)，原账号继续使用；App 内新增“Agent 访问”。
- Agent 用户示例：[http://localhost:18288](http://localhost:18288)，可以自己注册、确认授权和访问示例工单。
- 带预置 Agent 的专用 Console：[http://localhost:18290](http://localhost:18290)；管理员凭据文件为 `.dev/agent-demo/bootstrap.json`，不是原 Console 的同一个账号。
- 示例资源：[http://localhost:18289](http://localhost:18289)，REST `/api/tickets` 与 MCP `/mcp`。

原 `authova_demo` 在核对进程归属后静止备份，从 schema 5 升至 6。原两个 App 的 ID、key、版本、策略、用户数量和运营账号数量保持一致，原管理员实际登录与新路由读取通过。备份大小 220,209 字节，SHA256 `01c9cbc629862654d3cafc3a8d0d76321ddd39d7d395c69094c9d51af50ba1df`。

Agent 示例使用独立 `authova_agent_demo`。没有切换原库，没有恢复更早已丢失的历史数据。Core 最终二进制与独立 Agent 示例的 Core 一致；原 RP 进程保留，独立 Agent RP 已更新。

完整原始日志在工作区 `.dev/agent-identity-acceptance`；凭据及浏览器登录日志位于私有目录；原库备份位于 Core `.dev/agent-identity-upgrade`。公开证据只包含脱敏摘要、哈希与不含秘密的界面截图。

## 6. 剩余边界

- 外部业务系统继续检查当前成员关系、用户权限与对象归属。当前没有外部租户管理员自助身份、整租户批量同意或业务权限数据库。
- SDK 在线校验可以在撤销提交后的新请求中拒绝；离线 JWT 最多仍有 300 秒窗口，已经放行的在途业务操作不会自动回滚。
- 没有 RFC 8693 Token Exchange 端点、CIMD/DCR 自动客户端注册、第三方 Token Vault、子 Agent 自动转授权或 Agent 执行引擎。
- 仍需目标环境持续容量/长 soak、HA/灾备与跨地域撤销、真实外部 Agent 客户端互操作、客户业务权限接入及独立安全审查。不能称为已经完成生产认证。

本轮尝试委派 Kimi K3 max，其在产生该模块代码前返回 weekly quota 403；Codex 完成实现、接口安全复审和本地验收。没有将本轮 UI 描述为 Kimi 交付。所有修改仍在本地，未 push。
