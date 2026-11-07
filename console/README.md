# Authova Console

独立 TypeScript Web UI + Go BFF。管理 token 只保存在服务端加密 Valkey 会话，浏览器仅持 HttpOnly Cookie；使用同源 CSRF，逐请求由核心校验当前管理权限。没有数据库访问权限。

平台 operator 使用密码 + TOTP 登录；App 管理员经本 App OIDC、PKCE、MFA 登录。固定角色 owner/admin/auditor，App 主体不能创建或暂停其他 App；系统 Client 受保护。

```bash
make build
make check
```

`AUTHOVA_CONSOLE_URL` 为 Console canonical origin；`AUTHOVA_BASE_URL` 为核心；`AUTHOVA_CONSOLE_KEY_FILE` 是独立 32 字节会话密钥；`AUTHOVA_VALKEY_ADDRESS` 配置共享会话库。生产只接受 HTTPS 配置，静态资产由 BFF 同源提供。

本地全流程从相邻 authova 执行 `make demo`。Console 地址 http://127.0.0.1:18090。当前前端 31 文件/235 项行为测试，真实 Chrome/BFF/Core 联调见 [重构验收](docs/console-redesign-acceptance.md)；单测不替代浏览器或真实供应商验证。平台 owner 可以管理运营账号，初始 TOTP secret 只显示一次。

前端按 feature、API、外壳、共享交互组件和设计 tokens 拆分；支持亮/暗/系统主题、移动导航、版本冲突读回、未知结果冻结与一次性秘密的离开保护。长期维护规则见 [架构](docs/console-architecture.md)、[设计系统](docs/console-design-system.md)、[BFF 安全](docs/console-bff-security.md)。核心仍逐请求裁决角色与 App 隔离。

Go BFF 的 API transport 和 TypeScript schema 由 contracts 生成并提交，运行构建不要求 SDK sibling repository。修改契约后 `make generate`，检查生成 diff、UI DTO 兼容断言和实际响应；不手改生成文件。

Apache-2.0。当前尚未对外发布生产稳定版本。

本轮复审记录见相邻 Core 的 `docs/implementation/REVIEW-2026-10-14.md`。`AUTHOVA_TEST_VALKEY_ADDRESS` 配置真实共享会话测试，缺失时该层会跳过，不能当作已验证。

Go 和浏览器运行依赖分别记录在 `release/sbom-go.cdx.json`、`release/sbom-web.cdx.json`；上游许可证原文保留。Web 构建同时将合并通知文件放入静态包 `third-party-notices.txt`。

连接页支持 OIDC/GitHub/微信/QQ/支付宝创建、名称/停启用/密钥更新、版本冲突、未知结果冻结与读回；概览显示部署的 Email/SMS 配置状态和精确回调地址。渠道能力见 Core 的 `docs/implementation/IDENTITY-CONNECTIONS.md`。SMTP/SMS 部署秘密仍由服务端环境/密钥管理系统配置。

微信/QQ 当前覆盖微信开放平台网站扫码及 QQ 互联网站登录；AppID 和密钥在连接配置中填写，固定 issuer 不可修改。部署与供应商形态边界见 Core 的 `docs/implementation/SOCIAL-PROVIDERS.md`。

六项核心生命周期新增界面、可靠性复审和后端联调见 [补齐验收](docs/core-gaps-ui-acceptance.md)；该记录更新早期重构的 170 项测试快照。

### 独立构建的契约输入

`contract-inputs/` 保存显式选定的 Contracts schema/SDK 源快照及 SHA256 manifest。`make generate` 从本仓输入生成 Go/TS；`make check-generated` 校验输入 hash 并在临时目录复现生成，CI 同步运行。普通 Console 构建不要求邻接 Contracts 仓库。

更新权威输入使用 Contracts 的 `scripts/generate-bindings.py --console <本仓路径>`，随后运行 Core 的 `scripts/verify-contract-combination.py` 组合门禁。快照标记为本地 working-tree 选择，并未伪造已发布 tag 或远端 commit。

### App 资源预算和 Passkey

App 配置页支持六项资源预算、实际生效值和只读角色；写入必须携带完整预算、配置版本和原因，409/未知结果冻结并读取当前状态。登录策略新增 Passkey 开关与域名就绪提示，完整提交七个策略字段。Passkey 凭据由 Core 的账号安全页维护；平台运营账号仍使用密码 + TOTP。

完整设计与本轮验收见相邻 Core 的 `docs/implementation/RESOURCES-PASSKEYS-OBSERVABILITY-2026-10-16.md` 和验收记录；独立 localhost 演示 Console 为 http://localhost:18190。

## Agent 访问

App 工作区增加 Agent、接入记录和授权记录；写操作仅 owner 可用，创建/变更结果未知时冻结并通过权威 GET 回执核对，不自动重试。用户同意仍在 Core 的真实 OAuth 页面完成。新增类型来自已锁定的 Contracts schema；接入记录中的 tenant_id 是外部业务上下文。

## Valkey 生产连接

生产强制验证证书的 TLS 和命名 ACL 用户。配置 `AUTHOVA_VALKEY_USERNAME`、`AUTHOVA_VALKEY_PASSWORD_FILE`（或 PASSWORD，二选一）、`AUTHOVA_VALKEY_TLS_CA_FILE`；可选 `TLS_SERVER_NAME` 与成对的 `TLS_CERT_FILE`/`TLS_KEY_FILE`。不支持跳过证书验证或自动明文降级。开发默认可继续使用 loopback 明文实例。

Console 使用独立 `authova_console` 账号，只允许 `authova:console:*` 及共享会话所需命令，不复用 Core 的限流账号。Console 部署 env 不得包含 Core 的数据库、SMTP、SMS 或主密钥。secret 按单文件只读挂载，owner UID10001、0400。完整模板在相邻 Core 的 `deploy/console.env.example`，轮换/部署手册在 `docs/implementation/OUTBOX-VALKEY-2026-10-18.md`。

实际 mTLS/ACL、BFF 会话/CSRF/刷新与真实停机恢复门禁由 Core 的 `scripts/valkey-tls-gates.py` 创建隔离实例执行两仓 race；它不会改现有演示 Valkey。缓存停机返回 503；若缓存数据丢失，恢复后原会话返回 401，必须重新登录。无 TLS 夹具时相关 opt-in 测试会 skip，不算已验收。
