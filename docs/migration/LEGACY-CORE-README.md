# Authova

Go 身份认证服务，一个部署承载多个独立 App。App 间用户、标识、凭据、登录态、issuer、密钥和授权码隔离；同一邮箱可分别注册。首版不包含组织和业务权限引擎。

**状态：0.1.0-rc.1，本地生产候选版。** 首版实现、真实两库/浏览器/容器验证及 1200 万用户背景下的在线内省压测已完成；独立安全审查和客户环境生产验收尚未完成。

## 能力

- 密码、Passkey（WebAuthn）、Email/Phone 验证、找回、TOTP/一次性恢复码、外部 OIDC/GitHub OAuth2/微信网站扫码/QQ 网站登录/支付宝网页授权、单账号多身份绑定与配置管理。
- OIDC Code + S256 PKCE、RS256 access JWT profile、Discovery/JWKS、UserInfo、refresh 轮换/重用撤销、撤销和在线内省。
- 独立平台 operator 与 App 管理员，固定 owner/admin/auditor，MFA、当前角色核验、最后 owner 保护。
- PostgreSQL/MySQL 存储接口与独立迁移，PG 额外 FORCE RLS；审计和按 App 公平调度的加密 outbox，Valkey 分布式限流、原生 TLS/mTLS 与独立 ACL 角色。
- App HTTP/KDF/DB 资源预算、共享 RPS/验证码额度、Prometheus histogram、可选 OTLP 链路与 worker/outbox 告警。
- 独立 Console Go BFF + TypeScript UI；真实双 App RP 示例；Apache-2.0。

## 项目

| 项目 | 边界 |
| --- | --- |
| authova | 核心、默认登录页面、worker、迁移、真实 RP Demo 与系统验证 |
| authova-contracts | 正式管理 OpenAPI、协议约束、Go SDK、契约生成 |
| authova-console | 独立构建的 Web + Go BFF，只消费 API，不直连数据库 |

## 本地启动

依赖 Go 1.27.2、Node 26、Docker Compose、Python 3。三个项目放在同一父目录即可使用开发生成脚本；Console 与 Core 的运行构建彼此独立。

```bash
make demo
```

首次运行生成 `.dev` 私有密钥和管理员凭据、启动数据库/Valkey/Mailpit、迁移并引导、建立两个 App/Client、构建并启动真实服务。浏览器自行注册用户，验证码在本地邮箱沙箱读取；不会预置用户或免验证登录。

- RP Demo：http://127.0.0.1:18088
- Console：http://127.0.0.1:18090
- 邮箱：http://127.0.0.1:58025
- Core：http://127.0.0.1:18080

管理员凭据在本地 `.dev/bootstrap.json`，不要提交、分享或用公开开发数据库密码部署生产。加入认证器后登录 Console。再次启动保留数据库；配置写入与重复启动行为见运维说明。

Demo 使用独立 `authova_demo` 数据库；PG conformance/容量使用 `authova`，MySQL conformance 使用 `authova`、容量使用 `authova_capacity`。回归不会把压测和测试 App 加入演示列表。

## 检查

```bash
make check
make infrastructure
make integration
npm ci
npm run test:e2e
# 启动 Demo 后的一体化本地验证（两库必须实际可用）：
make verify
```

真实数据库测试需要环境变量；未配置时会标为 skip，不能当成数据库支持证明。浏览器测试使用 Chrome、真实接口和 SMTP 沙箱，不写数据库代替 UI。

## 文档

- [首版实施与评审](docs/implementation/README.md)
- [进度与验收缺口](docs/implementation/STATUS.md)
- [运维、迁移、密钥与恢复](docs/implementation/OPERATIONS.md)
- [实际结构与替换边界](docs/implementation/ARCHITECTURE-IMPLEMENTED.md)
- [1200 万用户/5100 QPS 的实测与限制](docs/implementation/PERFORMANCE.md)
- [生产部署包](deploy/README.md)
- [详细设计历史与边界](docs/technical-design/README.md)
- [实现的管理 API](../authova-contracts/openapi/v1/management.yaml)

数据规模、QPS、故障测试以具体报告为准。部署方需要完成其环境的 TLS、网络、HA、备份/恢复及独立安全评审。

第三方与手机接入、配置管理、验收边界见 [能力说明](docs/implementation/IDENTITY-CONNECTIONS.md)。本地短信 Demo 收件箱为 `http://127.0.0.1:18089`。

微信/QQ 专用网站登录的配置和身份边界见 [接入说明](docs/implementation/SOCIAL-PROVIDERS.md)。

本轮核心生命周期、正式 API 与升级说明见 [六项能力补齐](docs/implementation/SECURITY-LIFECYCLE-2026-10-15.md)。实际验收与容量边界仍以 [当前状态](docs/implementation/STATUS.md) 为准。

### 本地质量与存储演进

`make quality-local` 在独立临时 PostgreSQL/MySQL 库上执行真实受限账号 race 回归及选定 Contracts 组合检查，并清理临时库/测试 key。需先启动 `deploy/compose.dev.yaml` 的基础设施；不会使用 demo 数据库。存储加密 keyring/codec 升级及分页维护命令见 [迁移手册](docs/implementation/STORAGE-KEY-MIGRATION.md)，复审整改与验收边界见 [报告](docs/implementation/ARCHITECTURE-REMEDIATION-2026-10-15.md)。

按 App 选择登录方式、仅第三方登录与支付宝 RSA2 配置见 [登录方式与支付宝接入](docs/implementation/LOGIN-METHODS-AND-ALIPAY.md)。

### App 资源预算、Passkey 与可观测性

本轮实现与升级约束见 [实施说明](docs/implementation/RESOURCES-PASSKEYS-OBSERVABILITY-2026-10-16.md)，实际验证见 [验收记录](docs/implementation/RESOURCES-PASSKEYS-OBSERVABILITY-ACCEPTANCE-2026-10-16.md)。Passkey 演示使用独立 localhost 环境：`python3 scripts/passkey-demo.py start`，RP 18188、Console 18190；原 18090 演示保留。监控示例、告警和 Dashboard 位于 [observability](deploy/observability/README.md)。

Agent 上线协作的场景、架构和分阶段建议见 [调研方案](docs/research/AGENT-COOPERATION-2026-10-16.md)。当前认证核心没有模型依赖。

Agent 身份的并发、持续负载与恢复验收见 [专项报告](docs/implementation/AGENT-ENDURANCE-ACCEPTANCE-2026-10-16.md)：两库 Agent/普通 OAuth 各一小时、1200 万用户恢复通过；其中历史容量尝试失败的记录保留。后续 [容量整改](docs/implementation/CAPACITY-REMEDIATION-2026-10-17.md) 已在本地 Linux 同网络画像完成 PG/MySQL 各一小时、各 1836 万次成功内省，最低分钟均超过 5000 QPS，错误/掉队列/主体错配为 0。使用 24 个分散活跃用户/租户和显式 16384 发生器缓冲；不代表千万同时活跃、密码/签发或 TLS/目标部署容量。旧备份恢复后的 Agent 隔离及重新开放流程见 [运维手册](docs/implementation/OPERATIONS.md)。

### Agent 身份与用户委托

预注册 Agent、用户委托及自主运行、独立授权撤销、资源侧 Go SDK 与 REST/MCP 示例已加入。
见 [完整设计、API 与运行说明](docs/technical-design/14-agent-identities.md)。
`python3 scripts/agent-demo.py start` 启动独立演示（18288 / 18290），不重置原演示数据库。

Authova 自身的只读查询 API、MCP stdio、JSON CLI 和配套 Skill 见 [查询实施说明](docs/implementation/AGENT-QUERY-2026-10-21.md)。使用单独的 query audience/scopes 与 owner 批准的自主 Agent；普通业务 token 不获得后台管理权限，schema 保持 v8。

本轮 outbox 调度、Valkey TLS/ACL 与部署凭据拆分见 [运维说明](docs/implementation/OUTBOX-VALKEY-2026-10-18.md) 和 [验收记录](docs/implementation/OUTBOX-VALKEY-ACCEPTANCE-2026-10-18.md)。当前 schema 已升至 8；八项身份生命周期整改、兼容性和普通身份/Agent 恢复隔离顺序见 [整改与恢复手册](docs/implementation/IDENTITY-REVIEW-REMEDIATION-2026-10-18.md)。升级需要停止旧 writer/worker，禁止混跑。
