# 每 App OIDC/OAuth 协议设计 v1 草案

状态：待评审，尚未实现或通过互操作测试。标准协议的完整语义以 [OIDC Core](https://openid.net/specs/openid-connect-core-1_0-22.html)、[Discovery](https://openid.net/specs/openid-connect-discovery-1_0-22.html)、[RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html)、[RFC 9068](https://www.rfc-editor.org/rfc/rfc9068.html) 等原规范为准。此文只固定 Authova 的 App 隔离与首版协议组合，不自创替代格式。

## Issuer 与端点

令部署公共 origin 为 `https://identity.example.com`，App 的不可变 `app_key` 为 `a1b2c3d4e5f6`。issuer 是 `https://identity.example.com/a/a1b2c3d4e5f6`，不带尾部斜杠。App slug 改名不会更改 issuer。所有发现文档中的 `issuer` 字段与实际 ID/access token `iss` 必须字节级匹配。

| 能力 | 路径模板 | 调用方 | 核验重点 |
| --- | --- | --- | --- |
| OIDC Discovery | `/a/{app_key}/.well-known/openid-configuration` | Client/资源服务 | `issuer` 精确值、端点均属于同 App |
| JWKS | `/a/{app_key}/jwks` | 资源服务 | 只发布该 App 的公钥，保留验证窗口 |
| Authorization | `/a/{app_key}/oauth2/authorize` | 浏览器 | App/Client/redirect/PKCE/scope/audience/state/nonce |
| Token | `/a/{app_key}/oauth2/token` | Client | code 或 refresh，Client 类型/认证、App 绑定、一次性 |
| UserInfo | `/a/{app_key}/oidc/userinfo` | Client | access token audience、scope、App 与 User 状态 |
| Revocation | `/a/{app_key}/oauth2/revoke` | Client | Client 认证/归属；不暴露 token 存在性 |
| Introspection | `/a/{app_key}/oauth2/introspect` | 受权资源服务 | 活跃状态、audience、App/Client/Session、缓存时限 |

端点的具体 JSON/表单参数和错误由标准定义。授权端点采用 `response_type=code`，`code_challenge_method=S256`；不接受 `implicit`、`password` grant。首版支持 `client_credentials`，只给机密服务 Client，令牌主体是 Client，不伪造 User subject，只用于注册 audience 的机器身份访问，业务服务自行授权。Refresh token 只对允许的交互 Client/风险级别发放并轮换，服务 Client 默认无 refresh。`token_endpoint_auth_method` 首版支持 `none`（公开 Client）、`client_secret_basic`（机密 Client）；首版不预留额外 Client 认证方法。

Introspection 通过资源 Client 的标准认证和独立 `allowed_introspection_audiences` 列表限制 Token 可见性；首版内省 access token，其他类型按标准返回 inactive。用户 Token 查当前 User/Session/epoch，服务 Token 查 App/发行 Client；主库新快照、不缓存 positive active。依赖错误不能解释为可靠的 inactive/active。

## Client 注册约束

- `client_id` 由服务端生成 `avc_` + 26 位随机小写字母数字，全部署唯一并固定归属一个 App；不接受调用方任意选择 `client_id`，避免 `client_credentials` 的 `sub` 与高权限 User ID 混淆。公开 Client 必须 PKCE，`token_endpoint_auth_method=none`；机密 Client 使用明确认证方式，仍建议 PKCE。
- Redirect URI 精确匹配注册值；不支持通配符、片段、任意自定义 host；移动端自定义 scheme/loopback 回调按平台安全规范另行验收。
- scope 和资源 audience 预先注册于 Client，可请求值是注册上限。OIDC `openid` scope 触发 ID token；业务权限仍由资源服务决定。
- Client Secret 高熵，仅在创建/轮换响应及同主体同幂等键的短期加密重试窗口可见，普通 GET 永不返回；数据库存验证摘要。两个 Secret 可在轮换窗口并存，过期时间明确。后台服务若支持 JWT Client 认证，公钥按 Client 管理。

## Token profile 与验证

| token | 格式/用途 | 必验字段与限制 |
| --- | --- | --- |
| ID token | OIDC 签名 JWT，仅 Client 验登录 | `iss`, `sub`, `aud=client_id`, `exp`, `iat`, `nonce`（适用时）, `auth_time`（请求时）、签名/算法；不能用于业务 API |
| access token | 首版采用 [RFC 9068](https://www.rfc-editor.org/rfc/rfc9068.html) JWT profile，资源服务使用 | `typ=at+jwt`, `iss`, `sub`, `aud=具体资源服务`, `exp`, `iat`, `jti`, `client_id`, scope；资源服务还核验 App 与算法 |
| refresh token | 高熵不透明随机值，仅 Token 端点使用 | App/Client/User/Session/family/范围/绝对到期；每次刷新轮换，重用撤销整个家族 |

拟在用户 access token 中加入 `app_id`、`sid`、`session_epoch`、`auth_time` 等 Authova 私有 claim，但它们不能替代标准 `iss/aud` 验证，不能通过裸 `app_id` 判定有效。交互用户 token 的 `sub` 为 App 内稳定 User UUID；`client_credentials` token 的 `sub` 为 `avc_` Client ID，并携带 Authova 私有 `principal_type=user|client`，资源服务按主体类型解释，不可混成 User。这个命名空间隔离遵循 [RFC 9068 的混淆风险提醒](https://www.rfc-editor.org/rfc/rfc9068.html#section-5)。不同 App 中同一自然人没有自动全局 `sub`。管理 token 使用独立 audience；平台 operator 从专用平台 issuer（候选 `https://identity.example.com/platform`）登录，App 管理员从所属 App issuer 登录并额外检查 App grant。业务 access token 即使携带业务 scope 也不得直接访问管理 API。

## 失效、缓存与发现

发现文档和 JWKS 可缓存，采用 ETag/Cache-Control；新公钥先发布后签发，旧公钥至少保留至最长已签令牌寿命加缓存窗口。`kid` 仅在本 App key ring 查找，不接受 token header 自带外部 JWKS URL。ID/access token 使用不同 `typ` 与 audience 验证策略，防 token 混淆。资源服务可离线验证，但禁用用户/撤权后到 access token 过期仍可能放行；高风险资源使用 Introspection。

## 必做互操作与攻击用例

1. 以至少两种独立 OIDC Client 实现跑 Discovery、Code+PKCE、nonce、UserInfo、刷新和撤销。
2. App1/App2 的 issuer、JWKS、Client、Cookie、code、token 全部交叉注入并拒绝。
3. redirect URI 变体、编码/大小写/末尾斜杠、PKCE 不符、code 重放、Client Secret 轮换、未知 `kid`、错误 `typ/aud/iss` 均拒绝。
4. 上游 OIDC IdP 与 Authova 本地 issuer 分开验证，联邦登录不得将上游 ID token 直接当 Authova token。
5. 密钥轮换、App 暂停、时钟偏移、JWKS 缓存滞后和主库切换时协议行为可复现。

平台 issuer 的确切路径、管理 token 签发流与 `private_key_jwt` 是否进入 v1，在安全评审后冻结；草案不能被 SDK 视为已发布能力。
