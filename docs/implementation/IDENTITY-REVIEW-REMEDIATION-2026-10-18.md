> 历史来源实施记录：保留当时的 schema v8 与迁移描述；当前未发布仓库已合并为 [v1 基线](../migration/VERSIONING.md)，请勿按本文旧版本步骤初始化当前实例。

# 身份生命周期审查整改

日期：2026-10-18。范围：Core、Console、Contracts；整改与验收先在本地完成，再按用户授权提交并同步远端。

## 最终验收与模型复审

最终工作树完成 PG、MySQL 的完整 Core race 与 Core/Contracts 实际组合：424 个叶子用例通过、16 个显式专项夹具跳过、0 失败。Console 完整检查含245项前端测试，Contracts 完整检查通过。实际浏览器与两库小规模旧备份 dump/restore 分别运行，跳过项没有计作通过。完整阶段及源码指纹见 [机器摘要](../../evidence/identity-review-2026-10-18/acceptance.json)。

在双库回归通过后，`gpt-6-astra`、`xhigh` 对三个仓库的最终未提交工作树进行只读复审，包含新增源文件、测试、迁移及文档。结论：**原八项全部 Closed，当前整改范围内无未解决的 P0/P1/P2 或发布阻断。** 这是模型代码复审，不代替客户部署或外部专业安全审计。

复审额外发现一项 P2：页面把部署支持 WebAuthn 当成 App 已开启 Passkey，导致部分纯手机验证码注册渠道错误。已修正有效能力条件，补充16组策略/部署组合和两库完整 HTTP 注册，在最终全量通过的冻结源码中验证。复审者再次读取该增量与最终门禁摘要后确认关闭。

复审保留现有稳定 BaseURL/issuer、离线恢复隔离入口及停止其他写入者等运行前提；未将本轮验收扩展为新版本持续容量、真实供应商、HA/PITR 或客户 RPO/RTO 证明。复审时代码尚未提交和推送，演示服务和演示库未切换至新版本；之后的仓库同步不改变本轮验收范围。

## 八项变更

| 项目 | 实现 | 主要回归 |
|---|---|---|
| 登录 OTP 证明归属 | 发码绑定用户/epoch，验码锁定原用户并核对当前 identifier；未知用户、旧格式证明拒绝；版本戳同时拒绝升级前已验码但未完成的旧 OTP Flow；密码 KDF 后复用相同守卫 | `TestIdentityReviewRegression`，两库邮箱/手机、重分配、ABA、epoch、旧格式、晚注册、最终会话主体和一次消费 |
| UserInfo 当前 MFA | `ActiveToken` 与内省共用 `tokenMFA`，证明与存活事实来自同一主库语句；校验 session/user/family 身份 | 同一 token 的 UserInfo/内省政策变更对照及有效 MFA 正向用例 |
| 最后登录凭据 | Passkey 删除和外部身份解绑共用 `hasUsableLogin`；资料联系人/hash 不代替 identifier，保留系统管理员登录规则 | 无 identifier 的资料邮箱、可用本地别名及既有安全解绑/Passkey 并发测试 |
| OTP 配额 | 应用层统一预检/配额/锁内复验；所有登录、注册、重置和换绑入口共用 App+目标计数；限流网络 I/O 在 DB 锁外 | `TestChallengeQuotaAcrossReplicas`，两个真实 Valkey 客户端共享配额、混合用途、故障拒绝；HTTP 换绑不绕过 App 预算 |
| OTP-only 注册 | 密码关闭时，已验证且启用的 OTP 可以显式创建无密码用户；创建/identifier 同事务；MFA/同意/Code-PKCE 不变；注册渠道按 App 策略与部署能力共同计算 | 两库邮箱/手机真实 HTTP 注册、Code/PKCE 交换、在线内省；WebAuthn 可用但 App 关闭 Passkey 时的默认渠道；16组有效能力组合；注册策略变更拒绝；浏览器专项单独记录 |
| 普通身份恢复 | schema v8，独立 `quarantined` 状态；离线有界隔离、会话/刷新族/流程失效、旧 signer 撤销、新 signer；显式带外身份审批后重建凭据 | 中断保留 App fence、重复执行、跨 App、普通 enable/resume 不绕过；真实旧备份恢复演练 |
| 会话分页 | App+user 绑定的 keyset 分页，单页最多100，next_cursor；同步 OpenAPI、生成 Go/TS、Console | 两库155条混合状态会话、17条分页无重漏、最旧有效会话撤销、跨 App 拒绝 |
| 无效 JWT 有界工作 | 资源客户端鉴权后，对权威 inactive 早拒绝；按 App+主库 key_epoch 合并完整公钥集合，未知 kid 复用集合；仅加载未撤销且仍可验证的 keys | 两库不存在 jti 零 key load、随机 kid 一次集合加载、正常 token 可用；并发、App/epoch 边界、依赖失败和已有密钥轮换测试 |

## 公共安全规则

- `identifierProofUser`：Flow → User 的锁顺序；凭据版本和 identifier 归属共同构成证明；KDF 在锁外。
- `hasUsableLogin`：真实本地别名、启用的投递方式、Passkey、有效外部绑定及系统管理员例外。移除操作持用户锁，避免并发移除最后凭据。
- `challengeForIssuance`/`admitChallenge`：先验证请求和当前策略，释放读事务后获取共享配额，再锁内复验。并发修改预算要求重新发起；已消耗的计数保守保留，不补偿为额外放行。计数表示准入尝试，不代表实际送达。
- `tokenMFA`：使用同一主库语句的 session 证明。在线授权仍不缓存 positive allow。

## 升级至 schema v8

1. 备份数据库及密钥版本；停止所有旧 Core、worker、cleanup 和写入者。
2. 用配套 migrator 执行 `authova migrate`。v8 只扩展 App 状态 CHECK，保留既有状态和全部身份事实，不主动启动恢复隔离。
3. 运行账号检查 clean v8 和全部八个迁移 checksum；同步 Console/Contracts，再启动新版本。
4. MySQL DDL 隐式提交，失败保留 dirty marker，按既有修复/备份恢复程序处理；不得只删除 marker。PG/MySQL 均不支持用旧二进制操作已隔离的恢复库。

升级前签发或已验证、尚未完成的旧登录 OTP Flow 没有新的证明版本戳，将拒绝继续；用户须重新发起登录并获取新验证码。此项不依赖批量改写存量 Flow，也不允许退回旧二进制绕过。

原迁移1–7未改写。两库 v6→当前的 outbox 回填回归继续检查 payload、attempt 和调度时序；新增 v8 状态可在真实运行角色下使用。

## 旧备份恢复的运行顺序

恢复不是普通暂停/启用。旧备份可能回退密码、MFA、Passkey、外部绑定、封禁、管理员角色、client secret 和 Agent 委托。不能仅轮换签名 key 或撤销 refresh 后直接开放。

### 1. 隔离与保留证据

隔离目标 ingress，停止目标库**全部**写入者；保留原库、备份、校验摘要及密钥。恢复到新库并完成 schema/权限/解密核验。此阶段不能把恢复副本接到用户流量上。以下命令只有持有部署 DB 凭据和密钥的主机管理员可执行，没有对应未鉴权 HTTP API。

### 2. 隔离全部恢复身份

将私有 JSON 文件权限设为0600，输入不放入 argv、URL 或日志：

```json
{"reason":"Restore from reviewed checkpoint", "confirm":"quarantine_restored_identities"}
```

```sh
authova quarantine-identities < private-quarantine.json
```

该命令先隔离全部 operator 并撤销管理会话；逐 App 提交 `quarantined` fence，再按100条分页处理用户、客户端、会话、refresh family、Flow、signing key 和 Agent。用户保留归属，但普通状态恢复不能解封；旧 App 管理员角色被移除。普通客户端被禁用且清除当前/previous secret；固定管理 Client 保留，其用户和角色独立受 fence 保护。全部旧 signer 撤销；完成后创建新 signer 并写入完成标记。

每页独立提交。错误返回已提交计数，保持外部隔离后重跑；已完成 App 跳过，不反复轮换密钥。完成标记不等于业务目录已对账。命令不会自动放开 App，也不会把恢复的旧身份事实当作最新事实。

### 3. 建立可信管理身份与重建批准用户

使用 `recover-operator`，以新密码、新 TOTP 和**显式批准的角色**恢复经过带外核验的 operator。隔离账号不能通过旧的仅改密码/TOTP输入解封，也不默认信任备份中的旧角色。私有输入示例：

```json
{"email":"approved-operator@example.invalid", "password":"NEW_PRIVATE_PASSWORD", "totp_secret":"NEW_PRIVATE_TOTP_SECRET", "role":"owner", "reason":"Operator identity and current owner authority verified out of band", "confirm":"operator_identity_and_role_verified"}
```

其他 operator 继续隔离；HTTP 角色/状态修改不能启用它们。

对每个经过权威目录/人工带外核验的用户，执行：

```json
{
  "app_id":"APP_ID", "user_id":"USER_ID",
  "kind":"email", "value":"independently-verified@example.invalid",
  "password":"NEW_PRIVATE_PASSWORD",
  "admin_role":"",
  "reason":"Identity, current contact and ban status independently verified",
  "confirm":"identity_and_contact_verified"
}
```

```sh
authova recover-user < private-user-recovery.json
```

只能恢复对应 App 的隔离用户，且要求该 App 的隔离处理已完整完成。事务内清除旧本地标识、外部绑定、Passkey、MFA 和恢复码，撤销旧会话，然后写入新联系人、密码和明确批准的固定管理员角色。不会给未知用户批量解除封禁，也不会直接接受备份中的旧 OTP 联系人作为当前身份凭据。

OTP-only App 仍按其策略登录，新设密码不会绕过 App 的密码关闭策略。系统管理员登录继续要求 MFA，可通过现有首次设置流程建立新认证器。

### 4. 对账、解除恢复 fence 和切流

核对 App 配置、业务租户/成员、owner、禁用、外部 IdP 和资源/scopes；重建或重新审批客户端配置，使用全新 client secret。无法权威确认的身份继续隔离。

```json
{"app_id":"APP_ID", "reason":"Configuration and external authority reconciled", "confirm":"external_facts_reconciled"}
```

```sh
authova release-recovered-app < private-release.json
```

只有完整隔离处理的 App 可以从 `quarantined` 转为 `suspended`。可信平台 operator 再通过正常管理流程检查配置并 resume。未批准的用户、operator、client、Agent 和 grant 仍保持拒绝。恢复隔离期间不允许普通暂停/恢复、用户禁用/启用或常规签名 key 变更替代本地恢复流程。

离线 JWT 验证器须同步清除/拒绝撤销的 kid，不能承诺它们立即观察到数据库撤销。旧外部目录和业务权限事实不由本工具恢复。HA、PITR、跨主机恢复、千万级恢复耗时和客户 RPO/RTO 仍需部署验收。

## 可复现验收

```sh
python3 scripts/local-quality.py --evidence .dev/identity-review-full
python3 scripts/verify-identity-recovery.py --evidence .dev/identity-recovery-roundtrip
```

两脚本仅新建和销毁随机命名的自有临时数据库，不读写演示业务数据。第二个脚本执行真实 pg_dump/pg_restore、mysqldump/import：备份 → 源库改密码/MFA/封禁/轮换 client secret → 恢复旧快照 → 确认回退实际存在 → 隔离/批准恢复 → 检查旧凭据拒绝。只证明小规模的实际恢复机制。

UI由 Kimi Code 0.42 的 `kimi-code/k3`、max 配置编写；模板/差异由主审复核，实际浏览器验收由主审执行。短信/邮件在本轮测试中使用真实加密 outbox 的捕获适配器，不代表真实供应商送达验收。

每类证据分别保留：静态/单元、两库集成、真实浏览器、实际备份恢复、Astra复审。当前版本没有因本次回归自动获得新的容量认证；原一小时和mTLS五分钟证据保持各自版本与画像边界。
