# Authova 自有 HTTP API v1 详细设计草案

状态：字段级评审稿，尚未实现、稳定发布或生成 SDK。与 [标准协议设计](protocol-v1.md) 分开：OAuth Token/Authorization 端点按标准返回，本文的 `application/problem+json` 仅用于 Authova 自有 API。下面写的是**拟定契约**，所有路径和字段在 OpenAPI 冻结前可调整。

## 1. 调用主体与 API 分组

| 主体 | 凭据与 audience | 能调用的范围 |
| --- | --- | --- |
| Platform operator | 平台管理 issuer 签发的管理 token；需要 MFA/近期认证 | 创建/暂停 App、平台审计、授权 App 管理者；不能因掌握 App ID 自动看到用户明文凭据 |
| App admin | 本 App 用户经管理 audience 和 `app_admin_grants` 授权 | 所属 App 的 Client、用户、会话、连接器及有限审计 |
| Resource Client | 已注册机密 Client，经标准 Client 认证访问本 App 内省端点 | 只检查 `allowed_introspection_audiences` 注册范围内的 Token 状态；不调用管理 API |
| 普通 App User | OIDC Client 登录与授权 | 不直接调用管理 API；自助资料/会话 API 另版单列 |
| authova-console BFF | 用户管理登录后以管理 token 调用 | 与当前 operator/admin 完全同权限；BFF 不拥有新权限 |

所有 App 路径的 `app_id` 必须与已验证主体的范围比对。`app_id` 路径参数本身从不授权。控制台建议采用 Go BFF + Web UI：BFF 保存管理 token 的服务端会话，浏览器只持 `Secure/HttpOnly/SameSite` Cookie；控制台独立构建部署，但不存用户或角色真相。第一版也需对 BFF 的 CSRF 和会话轮换做安全测试。

Console BFF 有两条登录入口：平台 operator 使用平台 issuer 的内置 Console Client；App admin 先选择目标 App，再使用该 App issuer 下由核心自动注册的 `is_system` Console Client。两者是公开 Client + Code+PKCE + 精确回调，不需要在 App 之间分发 Client Secret；BFF 保存 token 并为浏览器建立服务端会话。管理 API 的 audience 固定为部署管理资源，普通业务 Client 不可申请；App admin 的 `app_admin_grants` 和路径 App 再次匹配，平台 operator 使用独立角色。BFF 的浏览器 Cookie 不等于 Authova App 登录 Cookie，退出分别撤销对应核心 Session/Token 和 BFF 会话。BFF 会话键包含 issuer/App，切换 App 不复用另一个 App 的管理 token。

Console 登录发现接口是公开只读的 `GET /api/v1/public/console-login-config`（平台）和 `GET /api/v1/public/apps/{slug}/console-login-config`（App）。仅返回 issuer 与内置公开 Client ID；BFF 只接受已配置 Authova origin 下的 issuer，再按 OIDC Discovery 取得端点。未知、停用 App 对外使用统一 404；接口按来源限速并禁止从请求参数指定任意远程 issuer。精确契约见 [console-login-v1.yaml](../openapi/drafts/console-login-v1.yaml)。

## 2. 通用 HTTP 规则

| 项目 | 规则 |
| --- | --- |
| Base URL | `https://identity.example.com/api/v1`；TLS 必需 |
| 媒体类型 | `application/json`；错误 `application/problem+json`，遵循 [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457.html) |
| 命名 | 路径复数小写；JSON `snake_case`；ID 是不透明 UUID 字符串，不从中推断 App |
| 时间 | UTC RFC 3339，有时区；Token 标准端点仍遵从协议自己的时间表示 |
| 列表 | `page_size` 1–100，默认 50；不透明 `cursor`，稳定排序 `(created_at,id)`；`next_cursor` 缺失表示结束；不默认返回 total |
| 幂等 | 创建类 `POST` 必须 `Idempotency-Key: UUID`；按调用主体+App+操作+请求摘要保留 24h（评审值），确保不重复创建。含 Secret 的密文响应只在同键同主体 5 分钟内可重放；5 分钟后至 24 小时返回同一资源但不返回 Secret，并标记 `secret_replay_expired=true`；同键异请求 `409 idempotency_key_reused` |
| 更新竞争 | `GET` 可给 ETag；修改需 `If-Match`；版本不匹配 `412 version_mismatch`，无条件更新默认拒绝 `428 precondition_required` |
| 追踪 | 每次响应 `X-Request-Id`；调用方可传合法 `X-Request-Id` 但服务端生成/限制格式；审计记录事件与关联 ID |
| 安全 | 普通资源响应、日志、错误 `detail` 不返回密码 hash、验证码、完整 token、Client Secret、上游 Secret；Secret 只在创建/轮换的授权响应或同主体同键 5 分钟内的加密重试响应中出现。密文到期清除，丢失 Secret 时由管理员轮换 |
| 限速 | `429 rate_limited`，可带 `Retry-After`；认证/找回场景统一外部响应以防账号枚举 |
| 删除 | 对生命周期资源优先停用/撤销，物理删除由保留策略处理；`DELETE` 语义在各资源中明确 |

### Problem Details 示例

```json
{
  "type": "https://authova.example/errors/version-mismatch",
  "title": "Resource version mismatch",
  "status": 412,
  "code": "version_mismatch",
  "request_id": "req_7f3a4c"
}
```

`type` 的稳定域名、错误码与本地化文案在发布前定稿。SDK 只按 HTTP status + `code` 分支，不匹配 `detail` 文本。跨 App 对象查询通常以 404 隐藏存在性；已知 App 但管理员缺权时用 403。未知/停用 Client 与 User 的登录失败保持统一对外语义。

## 3. 平台管理 API

现有 [platform-v1.yaml](../openapi/drafts/platform-v1.yaml) 已有建/列/读 App 草案。此表补全状态与策略：

| 方法/路径 | 权限 | 输入 | 输出与关键语义 |
| --- | --- | --- | --- |
| `POST /apps` | platform `apps:create` | `slug`, `display_name`，必带 Idempotency-Key | `201 App`；内部 key 准备完毕才可返回 active；不确定超时可同键重试 |
| `GET /apps` | platform `apps:read` | `cursor/page_size/status` | AppPage，游标按创建时间+ID |
| `GET /apps/{app_id}` | platform `apps:read` | 路径 UUID | App + ETag |
| `PATCH /apps/{app_id}` | platform `apps:update` | `display_name`/`slug`，`If-Match` | 改 slug 不改 `app_key/issuer`；返回新版本 |
| `POST /apps/{app_id}/suspend` | platform `apps:suspend` + 近期 MFA | `reason`，Idempotency-Key | 停新业务授权/刷新，资源 Token 内省返回 inactive；旧离线 JWT 仍有效到期 |
| `POST /apps/{app_id}/resume` | platform `apps:suspend` + 近期 MFA | `reason`，Idempotency-Key | 恢复新流程；不恢复已撤销 token |
| `POST /apps/{app_id}/signing-keys/rotate` | platform `keys:rotate` + 近期 MFA | `activation_at`（可选） | key 先发布后签发，返回 operation ID；不可直接暴露私钥 |

App 资源公开字段：`id`, `slug`, `display_name`, `status`, `issuer`, `created_at`, `updated_at`, `config_version`；`app_key` 可由 issuer 获取但不作为权限凭据。公开状态当前 `active/suspended`；内部 `provisioning/deleting` 不在这一版本列出，API 只在 ready 后返回 App。

## 4. App Client、Provider 与用户 API

所有以下路径前缀为 `/apps/{app_id}`。写入均须 App 管理授权及审计。

| 方法/相对路径 | 权限 | 重要输入/输出 | 约束与错误 |
| --- | --- | --- | --- |
| `POST /clients` | `clients:write` | `display_name`, `kind`, `redirect_uris`, `allowed_grants/scopes/audiences`, `allowed_introspection_audiences`, `token_endpoint_auth_method` → Client；机密 Client Secret 限定重试窗口展示 | redirect 精确 URI；public 强制 PKCE；管理 audience 和 `is_system` 保留给核心；不支持 grant 拒绝 422 |
| `GET /clients` / `GET /clients/{client_id}` | `clients:read` | 分页/Client | 无 Secret；`client_id` 对外用协议 ID |
| `PATCH /clients/{client_id}` | `clients:write` | 状态/名称/已注册 URI/scopes/audiences/可内省 audiences；If-Match | 更新不能扩大已有授权码/Refresh 范围；已发行 token 到期语义明确 |
| `POST /clients/{client_id}/secrets/rotate` | `clients:write` + 近期认证 | TTL/生效时点 → 新 Secret 一次展示 | public Client 不适用；轮换窗口内旧 Secret 可用 |
| `POST /provider-connections` | `providers:write` | name/kind/issuer/config/secret → connection ID | 校验上游配置并防 SSRF；`draft` 不可用于登录 |
| `POST /provider-connections/{id}/activate` | `providers:write` | 无或版本 | 必须通过上游连通/密钥测试 |
| `GET /users` / `GET /users/{user_id}` | `users:read` | `cursor/page_size/status` → 受限用户资料、验证过的标识遮罩 | 不允许通过模糊邮箱枚举全 App；精确标识搜索另授权/审计 |
| `POST /users/{user_id}/disable` | `users:disable` + 近期认证 | reason → 用户新状态 | 同事务禁用身份/提升 session_epoch，撤销 family 可分批；内省新快照先拒绝；禁止禁用最后可用 owner |
| `POST /users/{user_id}/enable` | `users:disable` + 近期认证 | reason → 用户新状态 | 不自动恢复旧 Session/Refresh |
| `GET /users/{user_id}/sessions` | `sessions:read` | 活跃/已撤销会话分页 | 设备信息脱敏 |
| `DELETE /users/{user_id}/sessions/{session_id}` | `sessions:revoke` | 空 | 204；重复撤销仍 204；跨 App/User 不匹配 404 |

Client 的 `allowed_introspection_audiences` 默认空，仅机密 Client 可配置；它与申请 Token 的 `allowed_audiences` 分开，不自动授予内省或管理权限。App 管理 API 不能为普通 Client 配置管理 audience。

User 对外返回候选：`id`, `status`, `created_at`, `updated_at`, `verified_identifiers:[{kind,masked_value,verified_at}]`, `profile`（App 策略允许的字段）。用户隐私数据和搜索语义需单独批准，**不能把 DB 的 `profile_ciphertext` 直接暴露为 JSON**。管理端修改密码不提供“写入明文密码”操作；找回走一次性挑战/用户自助流程。

现有 [app-core-v1.yaml](../openapi/drafts/app-core-v1.yaml) 覆盖部分 Client、User 和 Session 管理；其余端点仍是字段级设计，不能被 SDK 当作已发布 API。管理权限列是代码固定的动作名称，按 [管理角色矩阵](../../docs/technical-design/04-management-and-apis.md)执行，不存在可供业务注册的 Permission/Role 模型。

## 5. 审计与数据导出

建议 `GET /apps/{app_id}/audit-events` 支持 `from/to/event_type/actor_id` 与游标；导出使用异步 `POST /audit-exports` 返回 operation ID，查询与下载分开。平台审计与 App 审计权限不同。操作人读审计本身也写审计。高频内省的逐次读取日志默认不存主库；保留完整指标、抽样/风险驱动日志，部署方可配置额外日志管道与保留期。包含敏感标识的导出需要额外权限和短期下载 URL。

## 6. 状态码、幂等与兼容测试

| HTTP | 稳定 code 示例 | 适用情形 |
| --- | --- | --- |
| 400 | `invalid_request` | 格式/缺失字段 |
| 401 | `authentication_required` | 无效/过期调用凭据 |
| 403 | `permission_denied` | 已识别主体无固定管理权限 |
| 404 | `resource_not_found` | 不存在或跨 App 隐藏的对象 |
| 409 | `identifier_conflict`, `idempotency_key_reused` | 领域唯一性、幂等冲突或最后 owner 保护 |
| 412 | `version_mismatch` | If-Match 不符 |
| 422 | `validation_failed` | 语义校验，如 public Client 配置 confidential auth |
| 428 | `precondition_required` | 更新缺 If-Match |
| 429 | `rate_limited` | 限额 |
| 503 | `dependency_unavailable` | 数据库/KMS/供应商故障 |

异常 5xx 不能由客户端推定“写入没发生”。创建类重试沿用同一个幂等键，并可在成功后 read-back。24h 仅是普通资源创建的候选保留期；平台建 App、密钥轮换等操作可单独加长，但必须在契约中写明。OpenAPI 变更通过响应/错误快照、服务端 conformance、SDK 矩阵和 App1/App2 交叉攻击测试验收。
