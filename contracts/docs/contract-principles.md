# 契约设计原则（草案）

## 1. 名词和所有权

- **Deployment**：由部署方运营的一套 Authova 服务。
- **App**：该服务中的身份数据隔离单位。两个 App 的相同邮箱或手机号对应不同用户；其凭据、外部身份绑定、会话、协议授权和管理权限互不共享。
- **Client**：一个 App 下注册的 Web、移动端或服务端调用者。Client 不是独立用户池。

`app_id` 是资源归属标识，不是授权凭据。公开接口即使在路径中写出 `app_id`，也必须从已验证的调用者身份、Client 或 issuer 确定权限范围。只有平台管理员接口能进行显式跨 App 操作，而且必须审计。

公开 API 不暴露数据库方言或驱动类型。PG/MySQL storage adapter 共用外部领域语义，迁移/SQL 与额外隔离能力分别验收，见 [存储设计](../../docs/technical-design/12-portable-storage.md)。首版只保留身份认证、会话、令牌和固定管理能力，见 [首版范围](../../docs/technical-design/13-v1-scope.md)。

## 2. 协议边界

标准 OIDC/OAuth 端点遵循对应标准的参数、响应和错误格式；Authova 自有管理接口使用 OpenAPI 定义。不能把自定义的 `application/problem+json` 错误格式强加到标准 OAuth 令牌端点上。

每个 App 有独立的 issuer、发现文档和签名密钥集合。令牌消费者需校验 issuer、audience、有效期、签名算法和业务 App 范围。当前设计选择全部署唯一 Client ID、同域不可变 App key 路径 issuer；仍需 OIDC Discovery 兼容测试后冻结，SDK 不自行从 slug 推导 issuer。

浏览器登录页面与核心服务同仓库交付，不作为此仓库的公开接口。自定义登录 UI 所需的流程 API 会在威胁模型和 CSRF/会话边界确定后单独定义。

## 3. Authova 自有 API 规则

- 路径以 `/api/v1/` 开头，JSON 字段使用 `snake_case`。ID 为不透明字符串；公开草案当前使用 UUID 表示 App ID，但调用者不能从 ID 推断 App 归属。
- App 所有的资源地址包含 `app_id`；服务端独立验证调用者范围和资源归属。跨 App 读取默认返回 `404`，避免泄露资源存在性；已知且被拒绝的 App 管理操作使用 `403`。
- 列表使用游标分页，不承诺稳定的总数和偏移分页。时间用带时区的 RFC 3339 字符串，服务端写入 UTC。
- 成功响应只包含明确声明的字段。凭据、验证码、刷新令牌、第三方 Client Secret 和密码哈希永不出现在普通资源响应中。
- 自有 API 错误使用 [RFC 9457 Problem Details](https://www.rfc-editor.org/rfc/rfc9457.html) 的 `application/problem+json`；稳定的机器可读扩展字段为 `code`，可选 `request_id`。`detail` 供人阅读，不作为程序分支依据。
- 创建等可重试写操作要求 `Idempotency-Key`。在保留期内，同一个 key、同一调用主体和相同请求不重复产生副作用，并返回同一资源；相同 key 携带不同请求返回冲突。含 Client Secret 的响应仅在短期加密重试窗口返回相同 Secret，过后只返回相同资源并提示 Secret 不可重取；具体保留期见 API 草案，发布前需要故障测试。

## 4. 版本与兼容

OpenAPI `info.version` 是契约版本；URL 中的 `/v1/` 是不兼容变更的边界，两者不等同。发布后同一主版本内可新增可选响应字段和新端点，不删除字段、不改变既有字段含义、不放宽安全约束。新增枚举值可能让调用者做出错误授权决定，因此安全相关枚举的扩展也需要明确兼容评审。

Go SDK 版本单独发布，但必须标明支持的服务端契约范围。SDK 不能通过猜测 issuer、缓存跨 App 数据或吞掉未知错误来掩盖服务端不兼容。发布门槛包括 OpenAPI 规范校验、服务端契约测试、SDK 测试、跨 App 攻击用例和升级兼容测试。

## 参考规范

- [OpenAPI Specification 3.1.2](https://spec.openapis.org/oas/v3.1.2.html)：首版草案采用该格式，3.2 的工具链兼容性另行评估。
- [RFC 9457：Problem Details for HTTP APIs](https://www.rfc-editor.org/rfc/rfc9457.html)。
- [RFC 9700：OAuth 2.0 Security Best Current Practice](https://www.rfc-editor.org/info/rfc9700/)。
- [OpenID Connect Core 1.0](https://openid.net/specs/openid-connect-core-1_0-22.html) 与 [Discovery 1.0](https://openid.net/specs/openid-connect-discovery-1_0-22.html)。
