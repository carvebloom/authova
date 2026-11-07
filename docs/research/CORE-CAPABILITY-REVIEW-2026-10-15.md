# Authova 同类系统与核心能力复核

调研日期：2026-10-15。对象是三个仓库当前工作区，而非只看 Git HEAD 或早期设计稿。本文是调研结论和建议，没有批准新增产品范围，也没有实现下文建议。

后续实施记录：六项建议后来已获批准并落地，当前状态见 [生命周期验收](../implementation/SECURITY-LIFECYCLE-ACCEPTANCE-2026-10-15.md)。下文和原证据清单仍保留调研时的源码基线。

## 1. 结论

**未发现当前定位下必须新增的独立核心系统或第四个项目。现有核心模块划分基本完整，但不能据此认定首版功能和生产验收已经完成。**

代码中可以确认六处应补齐的核心操作闭环：

1. App 级最小认证策略：允许哪些登录方式，是否开放注册，以及第三方首次登录是否允许创建用户。
2. MFA 凭据维护：安全换绑 TOTP、重新生成恢复码，以及恢复登录后的换绑路径。
3. 普通 App 用户的启用恢复：已有禁用，没有对应恢复接口。
4. 下游 OAuth confidential Client 的 secret 轮换：与已有的上游第三方连接 secret 更新是两件事。
5. 签名密钥泄露处置：已有正常轮换，缺少明确的应急撤销能力和跨缓存处置流程。
6. 安全事件记录：已有操作审计，但登录/MFA 失败、限流等没有形成足够的、可关联的安全事件记录。

这些工作应进入现有 Core、Contracts、Console，不需要新增微服务。旧用户导入、业务事件 Webhook、原生移动端形态等，仅在对应客户场景成立后升级为交付要求。

**持续 5000 QPS、真实供应商互操作、多机 HA 等仍有验收缺口。它们不是缺少模块，也不能由竞品的容量宣传替我们证明。**

## 2. 方法、范围与证据边界

- 比较 8 组同类项目：Keycloak、ZITADEL、Casdoor、Ory、Logto、SuperTokens、authentik、Dex。
- 竞品依据官方文档及部分官方仓库说明；没有部署全部竞品，也没有进行同条件性能测试、完整源码安全审计或重新认证其许可证。
- Authova 的正向能力以当前代码、正式 OpenAPI 为主；历史测试结果引用已有报告，本轮没有重新运行功能测试或压测。
- “没有找到能力”指已核查领域模型、用例、对外路由、正式契约和相关运维说明；直接改数据库或测试内调用底层写入不算产品支持。
- 商业版、云服务和开源自托管文档可能混排。本文不把官网出现的能力自动视为免费开源能力，也不从抓取时间推断项目活跃度。
- 首版边界依据 [首版范围与数据所有权](../technical-design/13-v1-scope.md)：独立 App 身份域；不恢复组织、成员、业务 RBAC/PDP、通用权限 Check 或占位扩展系统。
- `authova-contracts/openapi/v1/management.yaml` 是正式管理契约；Contracts 的旧 `docs/` 和 `openapi/drafts/` 是历史参考。旧设计里出现过某个字段，不等于当前实现已经支持。

本轮核查文件的 SHA-256 和仓库基线见 [证据清单](CORE-CAPABILITY-REVIEW-2026-10-15.evidence.json)。工作区已有上一轮未提交改动，后续代码变化可能使行号和结论需要更新。

## 3. 先对齐身份隔离单位

Authova 的 `App` 是独立用户池/身份域；App 内 `Client` 才是接入它的网站、服务端、SPA 等 OAuth 客户端。同一产品的 Web 和手机端若共享用户，通常应属于同一个 App、使用不同 Client。两个独立产品才使用两个 App。

不能把竞品都叫作 “Application” 的概念直接放在同一列比较。

| 系统 | 官方模型与典型场景 | 对 Authova 的启发 |
| --- | --- | --- |
| Keycloak | Realm 管理身份和客户端，适合统一身份、企业接入及客户身份场景。 | Authova App 更接近 Realm，而非 Keycloak Client；可借鉴凭据维护和密钥运维流程。[官方管理指南](https://www.keycloak.org/docs/latest/server_admin/index.html) |
| ZITADEL | Organization 容纳用户和 Project，Project 组织相关 Application；另有跨组织委派。 | 用户池和业务应用是不同层级。其组织兼有身份隔离、B2B 委派用途，不能推导出 Authova 必须复制组织模型。[Organizations](https://zitadel.com/docs/guides/manage/console/organizations-overview)、[Projects](https://zitadel.com/docs/guides/manage/console/projects-overview) |
| Casdoor | 用户属于一个 Organization，可以登录该组织的应用；built-in 用户是全局管理员。 | 独立用户池需要关注 Organization 边界，不能仅建立两个 Application。可借鉴国内连接器与管理体验。[Core concepts](https://casdoor.ai/docs/basic/core-concepts/) |
| Ory Kratos / Hydra / Keto | Kratos 管理身份、凭据和自助流程；Hydra 管协议授权与签发；Keto 是权限决策服务。 | 模块职责确实可分，但跨服务集成和一致性成本也由接入方承担；不能只因 Ory 分仓就把当前 Core 强拆。Keto 不构成恢复业务授权范围的理由。[Hydra 官方仓库](https://github.com/ory/hydra)、[Kratos 官方仓库](https://github.com/ory/kratos) |
| Logto | CIAM 场景中，一个 tenant 可以承载多个应用和组织；Cloud tenant 是隔离环境。 | Authova App 更接近身份池边界。其文档区分 B2C、B2B 以及多个独立身份系统，正好说明“多应用”不必等于“多组织”。[架构选择](https://docs.logto.io/introduction/plan-your-architecture)、[Cloud tenant](https://docs.logto.io/introduction/set-up-logto-cloud) |
| SuperTokens | 一个 Core 可有独立 App，App 下再有 tenant；App 可共库并保持逻辑隔离。 | 独立 App 模型与本需求相近；文档明确多 App 属于付费能力，不应列成无条件免费开源能力。[Manage apps](https://supertokens.com/docs/authentication/enterprise/manage-apps) |
| authentik | Application 与协议 Provider 配对，支持 OIDC、SAML、LDAP、Proxy 等；Outpost 可保护既有服务。 | 重点参考企业和存量系统接入场景，不能把其 Application 当独立客户用户池。[Providers](https://docs.goauthentik.io/add-secure-apps/providers/)、[Forward auth](https://docs.goauthentik.io/add-secure-apps/providers/proxy/forward_auth) |
| Dex | Connector 把上游身份系统转换为 OIDC 接入，常见于基础设施身份联邦。 | 适合研究连接器边界，不适合作完整客户账号生命周期基线。它也有本地密码连接器，不能说完全没有本地用户。[Connectors](https://dexidp.io/docs/connectors/)、[Local connector](https://dexidp.io/docs/connectors/local/) |

由这些产品模型推得的工程判断：身份用例、标准协议、管理交付是稳定职责；是否独立进程，应由替换需求、部署所有权和一致性成本决定。目前没有证据要求新增项目。

## 4. 按实际使用场景复核

表中“已实现”仅代表有代码与相关已有证据，不表示所有供应商和生产部署均已验收。

| 场景 | 当前实际情况 | 判断 |
| --- | --- | --- |
| 两个独立 App 使用相同邮箱/手机号 | App 作用域用户、标识、绑定、会话、Client、issuer 和密钥；PG/MySQL 均有隔离实现及相关用例。 | 核心已有；保留跨 App 回归。 |
| 新建 Web 产品注册/登录 | 密码、Email/SMS OTP、验证码验证、找回密码、OAuth Code+PKCE。 | 主流程已有。 |
| 一个账号绑定多家第三方 | OIDC、GitHub、微信网站扫码、QQ 网站登录；显式绑定、解绑及可登录性保护。 | 核心已有；真实供应商仍需验收。 |
| App1 开放注册，App2 仅允许已有用户/指定登录方式 | 只有 `RequireMFA` 等有限 App 设置；本地方式与注册缺少 App 策略开关。 | 核心配置闭环不完整，见 G1。 |
| 用户换手机、丢失 TOTP 设备 | 有恢复码可完成 MFA，但不能通过正式自助入口换绑现有 TOTP 或重新生成恢复码。 | 凭据生命周期不完整，见 G2。 |
| 客服误封后解封 | 有用户禁用、会话撤销；没有普通用户重新启用接口。 | 管理生命周期不完整，见 G3。 |
| 服务间机器鉴权 | 已实现 `client_credentials`，区分 user/client 主体，并有真实 token 路径用例。 | 不缺机器身份模块；其 secret 运维见 G4。 |
| Client secret 或签名私钥泄露 | 上游连接可更新 secret；自身 Client 无正式 secret 轮换；签名 key 只有正常退休机制。 | 安全操作不完整，见 G4/G5。 |
| 用户投诉被撞库或反复收到验证码 | 有分布式限流和部分成功操作审计；缺少完整的失败/拒绝事件关联。 | 可防护不等于可调查，见 G6。 |
| 既有产品替换旧用户系统 | Schema migration、压测造数、备份恢复不等于用户导入；没有正式客户用户迁移流程。 | 若首批客户是存量替换，此项会变成接入门槛。 |
| 注册/注销后同步 CRM、业务资料库 | 有 Email/SMS outbox，没有通用用户事件订阅与投递。 | 需要实时下游同步时补；新业务也可在首次登录建立资料。 |
| iOS/Android/桌面/微信小程序 | PKCE 是基础，但当前 Client redirect 主要接受 HTTPS，开发模式才接受限定 loopback HTTP；微信/QQ 仅实现已声明的网站形态。 | 不宣称完整原生端、小程序或桌面适配。按目标终端另验。 |
| 多个 App 共用部署，一个 App 遭攻击 | 共享 Valkey 限流、全局 HTTP/KDF 上限已有；没有完整的 App 总量预算/公平性保障。 | 数据隔离已有，资源互不影响尚未证明；见第 7 节。 |
| 员工目录、部门、工单资源权限 | 不属于首版，业务服务仍拥有业务组织和权限事实。 | 不列缺失核心模块。 |

## 5. 当前已有的核心能力

| 核心区域 | 代码证据 | 结论边界 |
| --- | --- | --- |
| App 与存储隔离 | `internal/domain/model.go`、`internal/ports/storage.go`、`internal/storage/sqlstore/` | PG RLS 与 MySQL 作用域约束有实现；不是每 App 独立物理库的承诺。 |
| 用户与账号自助 | `internal/application/authentication.go`、`selfcare.go`、`internal/transport/account.go` | 密码/标识修改、显式绑定解绑、资料清除已有；MFA 维护见 G2。 |
| 外部身份 | `internal/federation/`、`internal/transport/federation.go`、相关 integration tests | 连接配置和协议适配已有；供应商审批、真实凭据和终端兼容不能由本地 fixture 替代。 |
| OAuth/OIDC 与机器主体 | `internal/protocol/storage.go:463`、`provider.go`、`internal/integration/security_review_test.go:177` 附近 | M2M 不是仅 Discovery 声明；用例实际请求 token 并检查 active。未因此获得完整 OIDC 产品认证。 |
| 会话与撤销 | 协议 storage、`session_epoch`、refresh family、主库状态检查 | 在线内省与离线 JWT 的撤销边界不同；不能承诺任意业务 SDK 的离线缓存立即撤销。 |
| 配置与管理面 | `internal/transport/management.go`、正式管理 OpenAPI、Console BFF | 配置版本、角色、最后 owner、防结果未知重放等已有；不是通用配置中心。 |
| 分布式限流 | `cmd/authova/main.go:117`、`internal/transport/limiter.go`、`login.go` | production 要求 Valkey，原子计数共享；不能错误地描述为“只有进程内限流”。 |
| 消息与运维 | `internal/delivery/worker.go`、`metrics.go`、`docs/implementation/OPERATIONS.md` | outbox 租约/重试/fencing、指标、清理、升级及恢复已有；不是通用事件平台或多机 HA 证明。 |

## 6. 已确认的六处核心操作缺口

### G1. 每 App 的最小认证策略

**证据：** `domain.App` 的认证策略字段只有 `RequireMFA`；`GET .../authentication-config` 返回 `password_login: true` 和部署级 Email/SMS 就绪状态，没有相应策略更新接口。注册用例检查流程、验证凭据和 App active，没有 App 级关闭注册开关。

**场景：** 两个隔离 App 有不同的准入要求：公开产品开放注册，另一个只允许预先存在的身份；某产品只用第三方，不希望本地密码成为另一条入口。

**最小补齐：** 类型明确的 App 配置，控制注册、本地认证方式及第三方首次登录建号；所有入口由服务端执行，包含已创建的流程和回调。保留系统管理 Client 的强制安全规则。关闭注册不应禁止现有用户登录或已认证用户安全绑定身份。封闭注册若需要预建用户，还必须明确受控建号途径，不能默认让客户改库。

**验收：** 双 App 策略互不影响；直接请求隐藏接口无法绕过；配置变化后的旧流程、OTP 和第三方回调遵守明确规则。无须引入策略 DSL 或组织模型。官方对照：[Logto 关闭注册，包括第三方建号](https://docs.logto.io/end-user-flows/sign-up-and-sign-in/disable-user-registration)。

### G2. MFA 换绑与恢复闭环

**证据：** `RecoveryMFA` 消耗一次恢复码并完成 MFA，没有替换 TOTP；`EnrollMFA` / `ConfirmMFA` 在已有 `TOTPSecret` 时拒绝。`NewRecoveryCodes` 的产品调用点只有初次确认绑定；账号安全页面没有换绑/补充恢复码入口。本地 `recover-operator` 仅针对平台 operator，不能当 App 用户恢复能力。

**影响：** 用户用恢复码登录一次后仍面对丢失的旧设备，恢复码又是有限的。已有“恢复登录”不等于已经完成“凭据恢复”。

**最小补齐：** 验证当前因素或合规恢复证明后，确认新 TOTP、原子替换旧因素、作废旧恢复码、处理旧会话并通知/审计。近期登录 Cookie 本身不应无条件获得移除第二因素的权限。所有证明都丢失时，首版可以明确拒绝自动恢复并提供受控支持流程，不能暗加弱身份校验后门。

**验收：** 旧设备失效；旧恢复码不可重用；新码只展示一次且有结果未知处置；并发恢复/换绑只产生一个有效结果；App 管理员仍满足强制 MFA。对照：[Logto TOTP replacement 与 backup-code maintenance](https://docs.logto.io/end-user-flows/account-settings/by-account-api)。

### G3. 普通用户禁用后的恢复

**证据：** 管理路由存在 `POST .../users/{id}/disable`，`DisableUser` 更新状态/epoch 并撤销会话；没有 enable 路由或通用状态更新契约。Client 和 Connection 的停启用能力不能替代 User。

**最小补齐：** 受权限控制的 disabled → active 状态转换、原因和审计。不能把 deleted 用户当作 disabled 复活；不能恢复旧 session/token。平台 operator 的管理流程与 App user 仍分别处理。

**验收：** 恢复后必须重新认证；旧会话保持失效；跨 App、只读角色、重复操作与竞争行为均明确。对照：[Logto 管理用户](https://docs.logto.io/user-management/manage-users)、[其 suspension/reactivation 事件](https://docs.logto.io/developers/webhooks/webhooks-events)。

### G4. 自身 OAuth Client secret 的生命周期

**证据：** `domain.Client` 只有一个 `SecretDigest`；客户端创建时生成 secret，正式 Client PATCH 不接受 secret，也没有轮换/撤销旧 secret 的管理操作。已有 Connection secret 更新针对上游 IdP，不解决下游业务 Client 泄露。

**最小补齐：** 保持 Client ID 的正常 secret 轮换；有限重叠期；旧 secret 的明确到期和紧急撤销。客户端禁用作为现有止损手段保留。需要明确 secret 撤销对已签发 M2M token 的在线/离线影响，不能默认“换密码等于所有 token 消失”。

**验收：** 重叠期新旧凭据按规则可用，过期/撤销后旧凭据拒绝；无常规明文回读；权限、审计、幂等、未知结果核对及双库/跨副本行为一致。对照：[Keycloak Client Secret Rotation](https://www.keycloak.org/docs/latest/server_admin/index.html)。

### G5. 签名密钥泄露的应急处置

**证据：** `RotateKey` 把旧 current key 的退休时间设为 24 小时后，`keycache.go` 对公开 key 有一分钟缓存。没有产品化的 compromised/revoked 操作；运维文档仅要求停止发行并事件恢复，尚无足够具体的应急流程。

**影响：** 正常轮换旨在让旧 token 平滑验签，不能冒充泄露密钥撤销。私钥泄露时攻击者可能继续伪造 token，不能仅等待正常已签发 token 的 TTL；在线内省的持久化 token 校验与资源服务离线信任缓存必须分开说明。

**最小补齐：** 区分正常轮换与应急撤销，明确新 signer、被撤销 key、Core 缓存、JWKS 和在线验证处理，以及资源服务刷新/拒绝旧 kid 的操作手册。业务服务未接入相应刷新或撤销规则时，不能承诺立即全网失效。存储主密钥轮换是另一个运维问题，当前文档已声明停机重加密限制，不与签名 key 混为一谈。

**验收：** 多 Core 实例、预热缓存、旧 kid、合法旧 token 和伪造 token 分别验证；可量化每条验证路径的失效窗口。对照：[Keycloak compromised keys](https://www.keycloak.org/docs/latest/server_admin/index.html)。

另有一处已确认的表达错误：Console `web/src/features/apps/OverviewTab.tsx:210` 写“旧密钥立即失效”，与现有 24 小时正常退休实现不符；这是需要修正文案/语义的缺陷，不是新增模块。本轮仅记录。

### G6. 失败和拒绝结果的安全事件记录

**证据：** 成功密码认证、MFA、注册、恢复及管理变更会写审计；密码错误等拒绝路径直接返回。`domain.Audit` 没有 outcome、request ID、来源等字段；HTTP 指标只有聚合计数，协议错误日志主要记录依赖失败，不能据此还原指定 App 的攻击过程。

**最小补齐：** 有界结构化安全事件，覆盖认证成功/失败、MFA/恢复失败、OTP 发送拒绝、限流及关键管理拒绝；关联 App、已知主体或脱敏标识、Client、request ID、原因分类和时间。可写标准日志并接外部收集，不要求首版自建 SIEM 或事件溯源平台。普通失败事件避免同步无限写入主业务事务；关键变更审计仍保证原子性，区分这两类可靠性承诺。

**验收：** 数据库回滚不吞掉需要保留的失败事实；日志不含密码、验证码、token、恢复码和第三方 secret；伪造 Forwarded 头不污染来源判断；攻击期间有明确的背压、丢弃指标和保留策略。对照：[ZITADEL 检索失败认证事件](https://zitadel.com/docs/guides/integrate/zitadel-apis/event-api)、[外部审计集成](https://zitadel.com/docs/guides/integrate/external-audit-log)。

## 7. 需要客户场景成立才增加的能力

这些不是首版候选接口清单，不应先建空模块。下表说明若目标客户发生变化，哪些交付承诺需要重新评审。

| 场景条件 | 当前缺口/限制 | 合理处理 |
| --- | --- | --- |
| 客户有存量用户，要求替换现有认证而不全员重注册 | 无正式用户导入、旧 ID 映射、密码 hash 兼容/渐进迁移；原生校验器仅接受固定参数 Argon2id。 | 有此首批客户时，优先受控导入/迁移工具：幂等、冲突处理、断点核对、已验证属性来源和 hash 升级。不必常驻独立迁移服务。参考 [ZITADEL 用户迁移](https://zitadel.com/docs/guides/migrate/users)。 |
| 下游必须及时响应创建/禁用/删除账号 | 当前 outbox 只投递 Email/SMS，没有业务 Webhook。 | 可添加 App 级异步事件投递，明确签名、重试、去重、重放、防 SSRF 和投递顺序；不能用异步事件代替业务请求上的权限检查。参考 [Logto Webhooks](https://docs.logto.io/developers/webhooks)。 |
| 共部署还承诺一个 App 不能耗尽其他 App 资源 | 共享限流已有，但全局 KDF/HTTP 池与投递资源缺少完整 App 预算/公平性控制。 | 先做攻击/高负载 App 对正常 App 的干扰验收，再根据结果补最小 App 级预算和发送总量上限；不直接扩成计费/配额平台。数据库隔离不等于资源 SLA。 |
| 首发 iOS/Android、桌面或小程序 | PKCE 存在，但 redirect、系统浏览器、回跳和微信/QQ 终端授权形态需要单独适配。 | HTTPS App/Universal Link、桌面 loopback、定制 scheme 等按实际端选定；不能把“支持手机号”当成这些形态已支持。 |
| 要求抗钓鱼因素或无密码优先 | 没有 WebAuthn/Passkeys。 | 属于明确安全目标下的增强；密码/OTP/TOTP 已有，不因此新增一个核心系统。 |
| 企业员工/合作伙伴目录或旧应用接入 | 无直接 LDAP/SAML/SCIM/代理接入。 | 仅在此类客户确定后评审；通用 OIDC 上游可以覆盖部分企业 IdP，但不等于直接实现全部企业协议。 |

不建议为了“扩展性”首版引入任意脚本引擎、同步业务 HTTP Hook 或插件代码执行。其故障、权限、网络和资源边界会进入登录热路径。Logto 官方也区分异步 Webhooks 与同步 Actions，且说明 Actions 的商业层级及自托管脚本权限风险：[Actions](https://docs.logto.io/developers/actions)。

## 8. 不应重新加入的首版模块

- 组织/部门/成员/团队目录：当前身份域隔离不依赖它们，业务可自己拥有这些事实。
- 通用业务角色、资源权限、关系授权或 PDP：与固定平台/App 管理授权、OAuth scope 不是同一层职责。
- 计费、订阅、CRM、营销、KYC/实名：不属于当前身份产品已确认的责任。
- LDAP 代理、零信任网关、跨 App 全局账号和自动 SSO：对应其他产品形态，不能当成现有首版漏做。
- 自建注册中心、配置中心、消息中间件：当前类型配置、正式 API、事务 outbox 已有落点，不因竞品采用分布式架构就新增基础设施项目。

“不属于当前首版”不代表永远不支持；只是本轮没有证据可以推翻既定范围。

## 9. 与模块缺失分开的生产验收

| 项目 | 已有事实 | 仍不能宣称 |
| --- | --- | --- |
| 用户规模 | PG/MySQL 已有各 1200 万用户及标识的实际数据、备份/恢复记录。 | 1200 万全部活跃访问或任意资料查询均达到目标 SLO。 |
| 持续在线内省 | 已有小时级长压；PG 热集约 4928.72 成功 QPS 且有掉队列，MySQL 小时约 4427.75 QPS 且有大量 503/drop。 | 已通过持续 5000 QPS。短测超过 5000 不替代小时结果。[容量报告](../implementation/PERFORMANCE.md) |
| 不同认证路径 | 有正常 TTL/refresh 流程与功能用例。 | 在线内省吞吐可直接推广到 Argon2 密码登录、签发、刷新和短信。 |
| 多副本与灾备 | 同库多个 Core 的竞争/撤销用例，以及单机备份恢复已有。 | 跨机故障转移、客户 RPO/RTO、多区域可用性。[当前状态](../implementation/STATUS.md) |
| 真实第三方 | OIDC/GitHub/微信网站/QQ 网站适配及本地协议用例已有。 | 所有真实供应商、账号审核、网络及回调场景已验收。[第三方形态边界](../implementation/SOCIAL-PROVIDERS.md) |
| 协议与安全 | 使用成熟协议库，已有本地安全审查及真实数据库/浏览器证据。 | Authova 自身已获完整 OIDC 认证、独立外部安全审计或生产环境认证。 |

现有 `/metrics` 有 HTTP、累计时长、运行时和连接池指标，不能说没有监控；但累计时长平均值不能回答 P95/P99，发送积压/死信、依赖故障与认证结果分布也需要进入部署验收。优先完善指标和运维集成，不自建监控产品。

## 10. 建议的执行顺序与项目归属

1. **修复明确语义问题并补安全恢复闭环：** 正常 key 轮换文案、MFA 换绑/恢复码维护、Client secret 运维及签名 key 应急流程。
2. **补核心管理和产品适用性：** User 恢复启用、最小 App 认证策略、失败安全事件；每项同步 Core → Contracts → Console 和场景用例。
3. **完成既定生产 gate：** 固定负载定义/硬件和错误预算，做独立容量复验、真实供应商验证、多机与灾备验收。功能补齐不能代替这些证据。
4. **仅按确定客户选择迁移/事件/终端工作。** 新应用首发不必先实现存量迁移；存量替换客户则应在接入承诺之前完成迁移能力。

仍保持三个项目：

- `authova`：身份事实、凭据、会话、协议、App 策略、安全审计/投递和存储。
- `authova-contracts`：正式管理契约、生成 transport/SDK 与兼容约束。
- `authova-console`：管理体验和 BFF；权限与策略的最终判断继续在 Core。

**最终判断：总体模块完整性基本足够，核心生命周期完整性仍有缺口，生产容量承诺尚未验收。下一步的收益在于补齐已有职责并证明它们，而不是增加一级系统数量。**
