# 4. 管理授权、令牌有效性与公开接口

修订日期：2026-10-11。首版聚焦独立 App 身份认证。本章取代原组织 RBAC、业务 Role/Permission 和在线 Check 提案；这些模型、接口及策略版本机制均移出首版，不保留占位实现。

## 4.1 首版授权的边界

| 类型 | 首版责任 | 判定依据 |
| --- | --- | --- |
| OAuth 授权 | 限制 Client 获得的 scope、资源 audience 与授权方式 | 已注册 Client 配置、用户同意、App/主体状态 |
| Authova 管理授权 | 保护 App、Client、用户、连接配置、会话、密钥与审计接口 | 独立管理 audience、固定管理员角色、可信 App 范围、近期 MFA |
| 业务权限 | 由业务服务负责 | 业务自己的访问规则、资源归属和实时状态 |

OAuth scope 是 Client 请求令牌的边界，不能自动证明用户能读取某张发票或操作某个商家。Introspection 只判断令牌及其关联身份/会话是否有效，不返回业务动作的 allow/deny，也不替代业务授权。

## 4.2 固定管理角色

首版采用代码定义的管理权限矩阵，不提供自定义角色、业务权限注册或角色继承 API。`platform_operator_roles` 和 `app_admin_grants` 只保存以下固定管理角色。

| 主体域 | 角色 | 允许范围 | 限制 |
| --- | --- | --- | --- |
| 平台 | `owner` | 平台管理员授权、App 生命周期、平台配置和恢复 | 敏感操作要求近期 MFA；首次引导一次性；不能失去最后一位可用 owner |
| 平台 | `admin` | App 创建、配置、暂停/恢复与平台运维 | 不授予平台 owner；不修改恢复凭据或最后 owner |
| 平台 | `auditor` | App 元数据、脱敏平台审计 | 不写配置、不读取 Secret 或 App 用户隐私数据 |
| App | `owner` | 指定 App 的配置、用户/会话管理、App 管理员授权 | 不管理其他 App 或平台；系统 Client、issuer 和 key 标志受核心约束 |
| App | `admin` | 指定 App 的普通 Client、连接、用户/会话和审计管理 | 不修改管理员授权；不能修改系统 Client 的保护字段 |
| App | `auditor` | 指定 App 的非敏感配置、脱敏用户资料与审计 | 不写入、不读取明文凭据、完整 Token 或供应商 Secret |

平台身份不等于所有 App 的用户数据读取凭据。进入某 App 的操作通过显式平台用例构造目标 AppScope，并检查该操作的权限与审计；不能让平台 repository 执行任意 App 查询。管理权限落实到每个端点，未知操作默认拒绝。App 管理者从本 App issuer 登录，再验证 `app_admin_grants`；普通业务 Client 不能申请管理 audience。

## 4.3 管理授权变更与撤销

管理员授权写入、审计和 outbox 同事务提交。授予/撤销 owner 时只锁该管理域的 owner 保护范围，验证剩余可用 owner；普通登录、刷新和用户写入不竞争这把锁。被禁用的 User/operator 不算可用 owner；禁用最后 owner 同样拒绝，受控恢复流程另有审计。

每次管理 API 从主库新快照核验调用主体、Session、App 状态及固定管理角色；首版不缓存管理 allow，也不依赖 Token 中的角色快照。角色撤销后，新开始的校验必须拒绝；撤销前已通过校验的在途请求可能结束，敏感写入在其事务内再次核验并与授权变更协调锁。该并发边界须通过真实数据库反例验证。

管理令牌使用独立 audience，敏感写入要求近期 MFA。App 暂停后保留平台 operator 的管理恢复路径；公开登录发现、新业务签发和业务资源内省按暂停规则拒绝，不能因管理可用而绕过暂停。

管理员角色变更不生成通用 `policy_version`。App/Client `config_version` 用于配置并发，User `session_epoch` 用于会话失效，两者不承担业务策略版本功能。

## 4.4 在线令牌内省与离线验签

首版提供标准 OAuth Introspection。调用方使用已注册机密资源服务 Client，经 Client 认证后才能内省；其固定配置限制可检查的 audience。App 来自 issuer 路由和调用 Client，不采信 Token 自报的 `app_id`。允许内省的资源 audience 使用 Client 配置的独立列表，不从“可申请某 audience 的令牌”推断可内省权限；该字段已同步进入 Client OpenAPI 和 PG SQL 草案，实际语义仍需验收。

用户 access token 的 active 判定至少核验签名/profile、issuer、目标 audience、期限、App/发行 Client/User 状态，以及对应 Session 未撤销且 `session_epoch` 相符。服务 `client_credentials` token 不伪造 User/Session，其 active 判定核验 App/发行 Client、签名、期限和 audience。无效或越范围 Token 按标准返回有限的 inactive 结果；调用方认证失败使用标准错误，依赖故障返回明确失败，不冒充可靠的 active/inactive 结果。

状态从主库新的一致快照取得：优先单语句；多语句用新的只读快照事务。首版不缓存 positive active，不从延迟副本推断未撤销状态。读取开始前已提交的退出、封号或 App 暂停必须可见；与撤销并发的请求以读取快照为边界。资源服务若自行缓存内省结果，会增加撤销窗口，必须明确上限。

离线 JWT 验签无法即时知道封号、退出或 App 暂停，其暴露窗口至少为剩余 Token TTL 加时钟容差；高风险接口选择在线内省。业务权限变化始终由业务服务处理，Authova 不对其撤权时效作承诺。

## 4.5 API 面与契约归属

| API 面 | 调用者 | 关键语义 | 归属 |
| --- | --- | --- | --- |
| 每 App OIDC/OAuth | 浏览器、Client、资源服务 | Discovery/JWKS、code/token、UserInfo、revoke/introspect；标准响应与错误 | 标准 + `authova-contracts` 协议约束 |
| 平台管理 `/api/v1/apps` | 平台 operator | App 生命周期、管理授权、密钥操作、平台审计 | `authova-contracts` OpenAPI |
| App 管理 `/api/v1/apps/{app_id}/...` | 本 App 管理员或明确受权的平台用例 | Client、provider、User、Session、管理员授权、Audit | `authova-contracts` OpenAPI |
| 默认登录内部流程 | 同源 UI、CSRF/浏览器绑定 | 注册、登录、挑战、找回、同意 | 与核心同版本，不作为公开扩展 API |
| 外部提供方适配 | 受控进程身份 | 身份证明、邮件/短信投递、超时、错误与幂等 | 现有登录接入所需的适配器契约 |

自有 API 使用 JSON `snake_case`、不透明 ID、游标分页、Problem Details 与稳定 `code`、创建操作 `Idempotency-Key`，见[契约原则](../../../authova-contracts/docs/contract-principles.md)。跨 App 对象查询默认统一 404；已识别管理主体但缺权限时返回 403。标准协议端点不使用自有 Problem Details 替代原有错误。

## 4.6 发布与二开边界

只发布实现且验收过的协议与管理契约。OpenAPI 草案目前覆盖部分 App/Client/User/Session 操作；管理员授权和部分连接/密钥操作仍需补齐，不能由表结构推断已存在 API。

二开依赖公开管理 API、标准 OIDC、现有提供方适配器与登录模板。内部包和数据库结构不承诺第三方兼容；不提前建立通用权限引擎接口或插件体系。标准协议、管理 API、适配器、事件和迁移各自管理版本，通过兼容矩阵及安全反例约束变更。
