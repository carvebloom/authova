# 第三方、手机与配置补齐验收

日期：2026-10-14。只在本地修改、测试、构建和提交，未 push、未运行远端 CI。能力和 API 详见 [接入说明](IDENTITY-CONNECTIONS.md)。

## 本轮改动

1. 提取 `internal/federation.Provider`，保留通用 OIDC 适配器，增加 GitHub.com OAuth App 协议适配。固定 GitHub 端点、S256 PKCE、逐次 `/user` 稳定数字 id、verified primary email；不持久化第三方 access/refresh token，不按邮箱自动合并账户。
2. 连接增加 `provider_type/config_version/created_at`。管理 API/Console 支持创建、单条读取、名称编辑、停启用、secret 更新、原因与原子审计；issuer/client_id/provider_type 不可变，secret 不读取，旧版本 409。新增部署登录渠道就绪状态和精确回调地址。
3. 账号页显示真实绑定数量和状态。解绑检查有效其他身份源、真实已验证的 alias + 密码/已配置 OTP；同连接多条身份、已停用身份源、仅 profile email 都不能绕过最后登录方式保护。用户锁与再次鉴权防止并发移除全部方式；成功解绑撤销旧会话。
4. 修正 profile 值与新验证 alias 相同但原来没有 alias 的情况：相同字符串不会跳过已验证 alias 的写入。
5. 手机原有注册/密码/OTP/恢复/换绑能力补齐验收。E.164 拒绝 0 起始国家码。SMS URL/token 启动校验；Demo 增加只监听 loopback 的 HTTP 短信收件箱，发送鉴权、幂等、防 DNS rebinding、HTML 转义、有限容量/保留期。
6. 新增两库 schema 3 索引，账号外部绑定按 `(app_id,user_id,connection_id)` 查询，不全表扫描。001/002 文件没有修改。新二进制要求 clean version 3，目标部署需维护窗口迁移。
7. Console 防止请求进行中关闭并重开表单；未知结果在父页面保留、冻结其他连接更新。409/未知结果读回后以当前服务端值重新编辑，清空旧输入和 secret，避免重放旧密钥操作。

## 实际验收结果

| Gate | 结果 | 证据 |
| --- | --- | --- |
| Core `make check`，真实 PG/MySQL 环境，Go vet/format/race/count=1 | 通过 | `core-check.log` |
| 新增两库 `TestIdentityConnections`，race/count=1 | 通过；并发版本更新仅一个成功、绑定两连接、占用冲突、配置变更阻断、无 email 自动合并、回调 replay 拒绝、禁用/同连接多身份/并发解绑保护、手机注册/OTP/密码/换绑/跨 App | `connections-integration.log` |
| Go provider/SMS config/E.164/demo mailbox 单测 | 通过 | `feature-unit.log` |
| 两库 test-owned 维护/迁移/权限/RLS/outbox/取消/HTTP drain 门禁 | 通过；旧 v1 经完整迁移到 v3，dirty/checksum 失败关闭 | `maintenance.log` |
| Contracts lint、Go vet/race、代码生成 | 通过，22 路径/30 操作/31 schema；正式及历史契约无 lint 错误/警告 | `contracts-check.log` |
| Console 实际 Valkey BFF race、TS typecheck、lint、build、行为测试 | 通过，15 文件/123 测试；其中连接页 15 个案例 | `console-check.log` |
| 三仓库 Staticcheck v0.8.2 | 通过，无诊断 | `staticcheck-core.log`、`staticcheck-console.log`、`staticcheck-contracts.log` |
| Chrome 真实 Demo 后端，完整 3 测试 | **3 passed，49.8 秒** | `browser-final.log` |
| 桌面/375px 连接页 | 已实际检查截图，无页面水平溢出；小屏连接表内部可横向滚动 | `connections-config.png`、`console-mobile.png` |

浏览器新增检查：

- Phone OTP 经真实 Worker/outbox/HTTP adapter 到本地 SMS 收件箱；手机注册，验证新手机号换绑后，密码登录和 OTP 登录都对应同一个 subject。验证码由真实收件箱读取，未覆盖服务端验证状态。
- Console 通过真实 owner 登录创建 GitHub 配置，停用 version 2，再启用并更新 secret 到 version 3，最终停用为 version 4。所有连接成功响应不含提供的两个测试 secret。测试使用假的 GitHub 凭据，仅测试配置；最终保留 disabled，避免把不可用的测试身份源暴露为 Demo 登录选项。
- 原有双 App 同邮箱隔离、SMTP 注册、Code/PKCE、refresh/introspection、自助修改密码/资料清除、实际本地 OIDC 与 UserInfo 流程继续通过。

协议 fixture 中的 GitHub 端点由测试 HTTP transport 回应；它不能证明与真实 GitHub 的实账号登录。两库 phone 细分集成用例使用固定测试 OTP digest；真实投递/OTP 链路另由 Chrome + Worker + HTTP SMS mailbox 验证。

## 初次失败与修复

手机浏览器测试先后失败于：Demo RP 配置没有 app_id 而测试用该字段匹配消息；嵌套 HTML label 的类型选择器不能按完全匹配找到。已修正测试为带非空 app_id 的唯一 destination/message_id 匹配与实际 label 定位。初次失败输出保留，不作为产品通过证据。补充 Console 回归测试后曾发现测试库 `ByRoleOptions` 不支持 `exact`；移除后最终 typecheck 与全部测试通过。最终结果以上表为准。

## 交付和剩余边界

- Demo 已按最新源构建并启动：RP `http://127.0.0.1:18088`、Console `http://127.0.0.1:18090`、SMS `http://127.0.0.1:18089`、Mailpit `http://127.0.0.1:58025`。`.dev` 中所有秘密与验证码收件箱原始数据均不归档。
- Kimi Code 0.42.0 的 K3 max 完成 UI 初版和 13 个新用例；本轮原生请求元数据验证 37 次。Codex 完成安全修复、两个新增 UI 回归用例、服务端、契约、独立复核及真实 Chrome 验收。模型请求参数证据不代替任务验收。
- 新配置页面不提供 SMTP/SMS 部署秘密的 Web 管理；这些通过环境/密钥管理系统配置。渠道“已配置”不是健康或实际送达保证。未知记录在当前页面内存中保留；刷新/离开页面后应核对服务端列表/版本/审计，不能假设先前写入失败。
- 没有真实 Google/GitHub/手机供应商凭据；微信/QQ/钉钉/飞书专用适配器、SAML/LDAP、GitHub Enterprise/Apple client assertion 没有实现，不声称已支持。
- 不重新宣称持续 5000QPS gate 通过。前轮 12M/一小时负载与备份证据是原 schema 2 快照；本轮验证功能与索引迁移，不替代目标硬件下持续容量和真实供应商验收。新代码的 OCI 镜像未在本轮重新认证，前轮镜像证据仍属于前轮版本。

证据目录：`evidence/identity-2026-10-14/manifest.json`，包含 SHA256 和大小；不包含私钥、bootstrap、供应商 token、数据库备份或上游 token。
