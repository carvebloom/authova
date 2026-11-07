# Authova 首版实施与验收

日期：2026-10-14。用户已授权全面评审并接管正式开发，目标是完整可测试的产品和使用真实接口的 Demo。尚未通过的门槛不能称为生产验收完成。

## 评审结论

| 优先级 | 问题 | 实施决策与验收 |
| --- | --- | --- |
| 必须修正 | 原公开草案只覆盖部分接口 | 实现、OpenAPI、Go SDK、Console 同步；未实现接口不得标为正式 |
| 必须修正 | 平台身份流程未冻结 | 平台 operator 独立于 App User，采用独立管理会话，MFA 与当前角色逐请求检查；不宣称平台 OIDC issuer |
| 必须修正 | 协议库仍是候选 | 评估锁定 ZITADEL oidc/v3 v3.51.14；通过实际 code/PKCE/refresh 事务反例才准入。库的认证不等于 Authova 认证 |
| 必须修正 | 32 表 SQL 尚无实现映射 | 实施迁移是唯一运行 schema；概念草案保留历史参考，以关键关系/隔离/事务和隐私不变量校验实际迁移 |
| 必须修正 | PG/MySQL 的隔离能力混淆风险 | 两套迁移与同一语义测试；PG 必须 FORCE RLS + 非 owner runtime；MySQL 明确只有作用域查询与复合 FK |
| 必须修正 | refresh 重用错误可能回滚撤销 | Token 请求共享作用域事务，缓冲响应直到提交；确定的 4xx 可提交安全状态变化，5xx 回滚；提交未知不返回成功令牌 |
| 必须修正 | Demo 易绕过验证/权限 | 独立 RP 走真实 Code+PKCE；验证码走邮件沙箱，不能把演示用户植入生产启动流程 |
| 必须修正 | 许可和安全响应规则未决定 | 已选 Apache-2.0；贡献、安全报告、依赖清单、CI、升级/恢复与证据报告入库 |
| 必须修正 | 容量目标无实测 | 分开报告数据量、内省、刷新、密码登录、离线验签；本地基准不等于 HA 或生产部署能力 |

## 交付门槛

1. 所有首版认证和管理用例拥有真实持久化实现、完整错误语义及 API 契约。
2. PostgreSQL/MySQL 真实集成测试覆盖 App 隔离、唯一注册、code 竞争、refresh 重用、最新状态内省、审计原子性。
3. 浏览器跑 Console 与双 App RP Demo；邮件/手机投递配置缺席明确失败，不降级为免验证注册。
4. 迁移、备份恢复、密钥轮换、依赖故障及升级说明均可执行且有证据。
5. 格式、静态检查、race、安全扫描、契约、构建、容器及目标容量报告通过。
6. 独立安全评审与生产 HA/恢复演练是生产发布门槛；本地自动化不能替代。

## 执行顺序

评审和冻结接口 → 核心/两个存储 adapter → 协议和身份闭环 → SDK/Console/RP Demo → 全面安全反例与故障测试 → 性能与发布证据。进度和未完成项记录在本目录，保持正式实现与设计目标分开。

## 交付入口

- `STATUS.md`：实际实现、验证层次与外部发布门槛。
- `REVIEW-2026-10-14.md`：本轮代码/安全复审、修复和本地验收。
- `REVIEW.md`：初版评审历史。
- `ARCHITECTURE-IMPLEMENTED.md`：实际存储、模块与替换边界。
- `FLOWS-AND-DATABASE.md`：当前运行关系、登录/refresh 流程图和实际表映射。
- `PERFORMANCE.md`：两库千万数据/内省负载与失败记录。
- `OPERATIONS.md`、`../../deploy/README.md`：迁移、部署、撤销、密钥、清理和恢复。
- `evidence/`：原始报告；图片仅含生成演示账户且无秘密字段。

- [第三方、手机、身份绑定与配置管理](IDENTITY-CONNECTIONS.md)
- [能力补齐验收报告](IDENTITY-ACCEPTANCE-2026-10-14.md)

- [微信与 QQ 网站登录](SOCIAL-PROVIDERS.md)
- [微信/QQ 本轮验收](SOCIAL-ACCEPTANCE-2026-10-14.md)

- [六项核心生命周期补齐](SECURITY-LIFECYCLE-2026-10-15.md)：App 策略、MFA 维护、用户恢复、Client secret、签名 key 应急撤销和安全事件。

- [六项生命周期验收](SECURITY-LIFECYCLE-ACCEPTANCE-2026-10-15.md)

- [Go 架构与代码质量复审](ARCHITECTURE-REVIEW-2026-10-15.md)

- [Go 架构复审整改与验收](ARCHITECTURE-REMEDIATION-2026-10-15.md)
- [存储格式升级与加密密钥轮换](STORAGE-KEY-MIGRATION.md)
