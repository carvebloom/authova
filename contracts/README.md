# Authova Contracts

Authova 的协议、版本化管理 API 和客户端契约。**0.1.0-rc.1，本地生产候选版，尚未对外发布。Apache-2.0。**

## 运行契约

| 路径 | 内容 |
| --- | --- |
| `openapi/v1/management.yaml` | Proto 派生管理契约，OpenAPI 3.1.2，37 路径/51 操作 |
| `generated/management/client.gen.go` | 类型化 Go 模型和 HTTP Client，由 oapi-codegen v2.8.0 生成 |
| `sdk/client.go` | 轻量 Go transport，错误/登录 DTO；不自动重试写操作 |
| `../api/authova/v1/*.proto` | 管理/查询契约唯一权威源 |
| `scripts/generate-bindings.py` | 同源 SDK 生成 Console 内独立 transport；运行构建不要求相邻仓库 |
| `release/` | Go 模块 CycloneDX 清单与实际上游许可证/NOTICE；不替代 OCI OS 包扫描 |

Core 拥有数据库与身份/协议事务；Console 消费 API。SDK 不拥有用户状态或权限。OAuth/OIDC 采用标准 Discovery/Code+S256 PKCE/UserInfo/JWKS/refresh/revocation/introspection；完整实现约束和验收见相邻 Core 的 `docs/implementation`。

## 修改与验证

```bash
make generate
make check
# 根目录统一生成 Proto/Go HTTP/OpenAPI/Go clients/TS/Console：
python3 ../scripts/generate-api.py --check
```

生成文件提交入库，编译和运行不要求联网生成。API 修改须同步 Core 实现、OpenAPI、生成 Client、Console DTO 兼容断言及实际两库/浏览器用例；Client 配置更新使用 `config_version`，创建使用 `Idempotency-Key`。网络超时可能为结果未知，SDK 不把它当作确定失败重试。

`openapi/drafts/` 和早期 `docs/` 是历史设计参考，不是另一个已上线 API。与首版实现不同的部分以 Proto 派生的 `openapi/v1/management.yaml` 和 Core 实施报告为准；不存在组织、业务 RBAC/PDP 或通用权限 Check API。

规范 lint/类型编译属于契约结构证据，不能替代 Core 的数据库隔离、协议互操作、安全审查或部署 HA 验收。仓库同步状态以 Git 记录为准；本地门禁不能替代远端 CI 或稳定版本发布验收。

连接支持 OIDC / GitHub.com OAuth App / 微信网站扫码 / QQ 互联网站登录 / 支付宝网页授权；`GET/PATCH /api/v1/apps/{app_id}/connections/{connection_id}` 支持当前版本查询与名称/状态/secret 更新。Provider type、issuer、client_id 固定，secret 不返回；PATCH 必须有 config_version/reason。`authentication-config` 返回当前部署的 Email/SMS 就绪状态和回调地址。连接创建目前没有幂等支持，超时需查询核对。

当前 `Connection.provider_type` 枚举为 `oidc/github/wechat/qq/alipay`，该供应商扩展本身不新增管理路径。新增供应商与原有连接共享不可变身份域、版本化更新和密钥不读取契约。

## 2026-10-15 核心生命周期补齐

正式契约增加 App 认证策略读取/版本化更新、按当前 session_epoch 恢复 disabled 用户、confidential Client secret 轮换/撤销前一份凭据，以及签名公钥状态列表/应急撤销。secret 轮换响应只展示一次；GET 不回读明文，失败或超时需读回当前版本后显式处理，SDK 不自动重放写操作。正常签名轮换保留旧验证密钥，和应急撤销是不同操作。

用户 TOTP 换绑/恢复码维护采用 Core 账号安全页面和 CSRF 保护的表单流程；失败认证事件采用有界结构化安全日志，不存在虚构的管理重置 MFA 或安全事件查询 API。完整语义与验收见相邻 Core 的 `docs/implementation/SECURITY-LIFECYCLE-2026-10-15.md`。

### 生成一致性与消费者固定输入

`make check` 包含 `scripts/check-generated.py`：在临时目录中从 Proto 重建 Go HTTP、OpenAPI、Go/TS 客户端并逐字节比较，不依赖 `git diff` 忽略未跟踪文件。CI 执行同一门禁。

Console 通过 `python3 scripts/generate-bindings.py --console ../console` 显式选择输入快照。其 manifest 保存 schema/SDK SHA256 和生成器版本；独立消费者 CI 从仓库内的固定输入生成，不要求邻接 checkout。更新后在 Core 运行 `scripts/verify-contract-combination.py`，核对三仓候选的输入并执行所选真实 HTTP 兼容场景；它要求两库测试环境，不代表远端发布已完成。

支付宝创建与密钥更新使用 write-only `alipay_credentials` 对象（`private_key`、`alipay_public_key`），两项须完整提供，不与 `client_secret` 混用。读取 DTO 不含该对象；连接配置版本、原因、不可变身份域保持相同约束。

### 2026-10-16 App 资源预算与 Passkey 策略

新增 GET/PATCH `/apps/{id}/resource-policy`，完整六项 limits、config_version 和 reason；返回 per_process/shared_limiter 范围及 effective_limits。AuthenticationPolicy 增加必填 passkey_enabled，完整 PATCH 为七个布尔值；AuthenticationConfig 增加只读 passkey_available。旧六字段调用方必须同步升级，本轮候选尚未发布为稳定兼容版本。用户侧 WebAuthn ceremony 由 Core hosted UI 提供，尚未作为管理 SDK 的公开 API。

## Agent 资源接入

`sdk/agent` 使用新鲜在线内省验证 App、Agent、grant、租户和 actor，并要求业务授权回调。
`examples/agent-resource` 与 `examples/agent-client` 使用官方 MCP Go SDK v1.8.0，提供 REST/MCP 真实调用示例；仅监听本地，配置含秘密时必须为 0600。Core 的 `scripts/agent-demo.py` 可准备并启动完整独立示例。没有 RFC 8693 交换端点、自动客户端注册或厂商互操作认证声明。

## Authova 自身的只读查询

`cmd/authova-query` 提供 JSON CLI 与 MCP stdio；`mcpquery` 暴露最多12个固定读取工具，`sdk/query` 负责受限客户端与短期凭证。独立契约为 `openapi/v1/query.yaml`。复用 owner 批准的自主 Agent，但使用专用 query audience/scopes，普通业务或 Console token 不具有查询权限；没有写接口或管理员凭据。

使用、配置及权限边界见 [查询接入手册](docs/agent-query.md)，可分发 Skill 位于 [skills/authova-query](skills/authova-query/SKILL.md)。Core 需要包含2026-10-21查询端点的版本；数据库继续使用 schema v8。

当前架构与尚未完成的管理运行适配见 [Proto 迁移说明](../docs/migration/PROTO.md)。历史 sibling 路径仅保留在旧报告中，当前构建不依赖三个相邻仓库。
