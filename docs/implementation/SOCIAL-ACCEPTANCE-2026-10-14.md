# 微信/QQ 网站登录验收

日期：2026-10-14。仅本地交付，未 push。接入范围、配置、官方来源与安全边界见 [SOCIAL-PROVIDERS.md](SOCIAL-PROVIDERS.md)。

## 完成内容

- Core 增加微信 `snsapi_login` 网站扫码和 QQ 互联网站授权码适配；固定端点，不接受自定义 social issuer。QQ 请求官方支持的 JSON 格式，验证 AppID/OpenID 与用户信息明确成功返回；微信验证换码和 UserInfo OpenID 一致性与 scope。
- 外部主键保持 OpenID + App/connection namespace，不根据可选 UnionID 切换主键或自动跨 App 合并。上游 token 只在内存短暂使用，不创建假 email/phone。
- 复用完整用户绑定、当前会话/epoch、一次性 state/cookie、MFA/同意、配置版本/密钥更新/停启用与审计。管理契约 provider_type 扩至四种，路径/操作数仍为 22/30；没有新增数据库迁移，schema 仍为 3。
- Console 四种连接类型显示准确形态与固定 issuer，微信 AppID/AppSecret、QQ AppID/AppKey 提示；保留近期补齐的未知结果冻结、版本冲突读回/清空旧 secret、只读权限和进行中禁止关闭表单。

## 本地 gate

| 验证 | 结果 | 归档证据 |
| --- | --- | --- |
| Core `make check`，真实 PG/MySQL 环境，vet/format/race/count=1 | 通过 | `core-check.log` |
| 两库 `TestSocialProviders` | 通过：实际管理 API 配置、state/cookie 回调、单账户两种绑定、登录复用原用户、占用冲突、相同 OpenID/UnionID 的跨 App 隔离、错误 state 不发上游请求、重放拒绝、issuer 覆盖拒绝 | `integration.log` |
| provider unit/race | 通过：授权参数编码、固定 HTTPS 目标、QQ JSON/ret/AppID、微信 scope/OpenID/UnionID、一致性/错误/缺字段/JSONP 拒绝、网络/503、敏感 URL 错误不透传、1 MiB 与 UTF-8 nickname 限制 | `provider-unit.log` |
| Contracts 生成/lint/vet/race | 通过 | `contracts-check.log` |
| Console 实际 Valkey BFF race + typecheck/lint/build | 通过；15 文件/127 行为测试，连接页 19 用例 | `console-check.log` |
| 三仓库 Staticcheck v0.8.2 | 无诊断，通过 | `staticcheck-core.log` / `staticcheck-console.log` / `staticcheck-contracts.log` |
| 最新 Demo 的完整 Chrome 流程 | **3 passed，38.3 秒**：手机注册/换绑/OTP，Console 四种供应商配置与密钥不泄漏，双 App 邮箱/实际本地 OIDC/PKCE/刷新/内省 | `browser.log` |
| 连接页截图稳定后再验 Console | **1 passed，35.7 秒**；桌面与 375px 截图实际查看，页面无水平溢出，表格内部横向滚动 | `console-browser-settled.log` / 两份 PNG |

供应商 HTTP 响应由固定端点 transport fixture 提供，数据库、服务端回调与权限/事务是真实实现。Browser 通过真实 Console/BFF/Core 创建微信/QQ 配置并禁用，断言成功响应不含输入密钥；使用假的网站应用凭据，不调用真实微信扫码/QQ 账号。测试后保持这些 fixture disabled，避免在 Demo 登录页暴露不可用选项。

## 初次失败

初次 social 集成用例错误地要求所有重放都返回 403；已完成的绑定 flow 返回 409，未发起任何新的供应商 I/O，实际是正确拒绝。测试调整为 403/409 均必须拒绝、且供应商调用计数不变。初次集成和当时全量检查失败记录保留，最终两库集成及 Core 全量检查均重新通过。不把首次失败记作通过或产品越权。

## 外部与容量边界

- 没有真实微信/QQ 网站应用 AppID/密钥与审核好的回调域，未做真实扫码/账号供应商验收。公众号、小程序、原生 SDK、企业微信及其他专用协议不在本次范围。
- 未给微信/QQ 上游声称 PKCE 支持；Authova 给业务客户端的 Code+S256 PKCE 保留。凭据传送格式按供应商官方 HTTPS GET 文档，错误/日志不得包含敏感 query。
- schema 未变化，无需重新执行迁移。本轮不重新认证 OCI 镜像、客户 HA、灾备或千万用户持续 5000QPS；前轮容量 gate 未通过的结论保持。
- Kimi Code 0.42.0 / K3 max 完成限定 UI/行为测试；本轮 28 次原生请求元数据验证通过。Codex 完成官方协议核对、Core/契约、独立安全复核、两库与真实 Chrome 验收。模型调用参数不是质量证明。
- 验收报告与代码保存在本地三个仓库。Demo 仍运行，Core readiness 正常。

证据：`evidence/social-2026-10-14/manifest.json`，包括文件 SHA256、大小和源快照摘要；不归档私钥、bootstrap、供应商秘密、完整供应商文档或 `.dev` 收件箱数据。
