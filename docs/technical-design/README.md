# Authova 技术方案 v0.4（评审稿）

状态：**历史设计基线，实际实现与当前证据见 [实施目录](../implementation/README.md) 和 [状态表](../implementation/STATUS.md)**。本方案面向可供其他团队自行部署的正式产品，不是演示系统。`authova` 是 Go 核心及默认登录页面；`authova-console` 是独立管理 UI；`authova-contracts` 拥有公开 API 与 SDK 契约。只在本地工作，不涉及远程仓库。

2026-10-11 修订：首版聚焦独立 App 的身份认证、会话、令牌和固定管理权限，移除全部组织模型及其业务 RBAC/Check 依赖，不预留扩展接口。存储继续通过领域 Repository + Unit of Work 支持 PG/MySQL 两个适配器目标；当前只有 PG SQL 草案，尚无正式实现。

## 阅读顺序

1. [边界、部署与威胁模型](01-boundaries.md)
2. [认证协议、登录与令牌](02-identity-and-protocol.md)
3. [数据模型与隔离](03-data-and-isolation.md)
4. [管理授权、令牌有效性与公开接口](04-management-and-apis.md)
5. [容量、可靠性与运维](05-scale-and-operations.md)
6. [实施阶段、验收与未决问题](06-delivery-and-validation.md)
7. [Go 工程结构与关键状态机](07-implementation-shape.md)
8. [端到端流程和异常分支](08-flows.md)
9. [逻辑数据模型与 PostgreSQL 物理草案](09-database.md)
10. [技术选型与部署形态](10-stack-decisions.md)
11. [默认登录页的内部流程 API](11-login-flow-api.md)
12. [存储抽象与多数据库设计](12-portable-storage.md)
13. [首版范围与数据所有权](13-v1-scope.md)

接口字段、端点和示例见相邻 [authova-contracts 的 API 设计](../../../authova-contracts/docs/api-v1-detailed.md)；标准协议端点见其[协议设计](../../../authova-contracts/docs/protocol-v1.md)。两份均为评审草案。可执行 SQL 形态的初稿见 [schema-v1.sql](schema-v1.sql) 与 [rls-v1.sql](rls-v1.sql)，尚未作为生产迁移发布。
第三方身份、邮件和短信的出进程边界见 [适配器协议草案](../../../authova-contracts/docs/adapter-v1.md)。

## 设计前提与状态

| 事项 | 当前提案 | 状态 |
| --- | --- | --- |
| 租户语义 | 一个部署内有多个独立 App；App 是用户、凭据、登录态、协议授权和管理权限的数据隔离单元；一个 App 可有多个 Client | 已由产品需求确认 |
| 身份共享 | 不预置全局用户，不在 App 间自动复用会话或账号；同一邮箱在两个 App 是两个身份 | 已由产品需求确认 |
| 交付形态 | Go 模块化单体加默认登录 UI；管理 UI 与公开契约各在独立项目 | 已由项目划分确认 |
| 规模 | 超过 1000 万用户、鉴权相关流量超过 5000 QPS | 目标，尚未测得；请求类型需分别验收 |
| 存储 | 同一领域/事务接口，PG 17/18 与 MySQL 8.4 InnoDB 分别适配；一个部署选一个事实源；Valkey 承载可重建状态 | 多数据库为已确认需求；适配器与容量尚未实现/验证 |
| 首版协议 | 每 App 独立 OIDC issuer，授权码 + PKCE；JWT access token 与在线内省按场景组合 | 技术提案，需互操作测试 |
| 首版范围 | 身份认证、会话、OAuth/OIDC、固定管理权限和正式自部署交付；业务授权由业务服务负责 | 范围已由用户确认；接口及实现仍待验收 |
| 5000 QPS 含义 | 暂按混合负载准备，分别列在线内省、刷新、交互登录和离线验签 | 待用户确认；不得混成单项性能声明 |

术语：**Deployment** 是一套由部署方运营的服务；**App** 是独立身份域；**Client** 是 App 的 OAuth/OIDC 调用者；**Platform operator** 是部署方管理员；**App admin** 只管理获授权的 App。App 的内部 `app_id`、对外不可变 issuer 路径标识和可变展示名称分开。

## 重要原则

- 所有后端以可信作用域、应用查询、复合约束、缓存/任务范围和令牌校验实现隔离；PG 额外强制 RLS，MySQL 共享表不能宣称有同样的数据库行策略。`app_id` 只是归属标签，不能当作授权凭据。
- 协议端点遵循 OIDC/OAuth 原有错误与响应规范。自有管理 API 使用 `authova-contracts` 中的 OpenAPI 与 Problem Details。不能让内部数据库结构成为外部契约。
- 身份、会话、协议和管理共享 App/User 语义及事务数据库，先在一个 Go 运行时内分模块。外部身份、短信和邮件通过版本化适配器隔离；不承诺核心身份库可无损热替换。
- 存储适配器可替换，领域代码不依赖 SQL/驱动；SQL、索引、迁移和故障/容量验收按后端分别维护。首版范围见 [第 13 章](13-v1-scope.md)，不建立空的业务扩展模块。
- 明确撤权时效与一致性。离线 JWT 的有效性持续到过期；立即撤权要使用在线内省；资源服务自行承担业务权限检查。
- 所有容量数字都绑定硬件、数据规模与偏斜、流量组合、成功率、延迟和故障状态；实施前的数字只能是验收目标。

## 项目接口及变更顺序

`authova-contracts` 先发布评审草案，服务端及控制台消费已批准的版本。当前 `platform-v1.yaml` 覆盖 App 建立、读取、修改、暂停与恢复；`app-core-v1.yaml` 覆盖部分 Client、User 和 Session 管理；其余仍是字段级设计。本设计不暗示草案已实现。协议兼容由标准测试约束，API 兼容由 OpenAPI、错误码和语义测试约束。重大语义调整先修订本设计，再变更契约，最后实现、迁移并发布。

## 参考基线

- [OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0-22.html)、[Discovery](https://openid.net/specs/openid-connect-discovery-1_0-22.html)
- [OAuth 2.0 Security Best Current Practice，RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html)
- [JWT Best Current Practices，RFC 8725](https://www.rfc-editor.org/rfc/rfc8725.html)、[JWT Access Token Profile，RFC 9068](https://www.rfc-editor.org/rfc/rfc9068.html)
- [NIST SP 800-63B](https://pages.nist.gov/800-63-4/sp800-63b.html)
- [PostgreSQL Row Security](https://www.postgresql.org/docs/current/ddl-rowsecurity.html)、[Partitioning](https://www.postgresql.org/docs/current/ddl-partitioning.html)
