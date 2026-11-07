# 3. 数据模型、事务与隔离

## 3.1 存储约束

关系事务数据库是身份、固定管理授权、协议一次性凭据、必须持久的安全审计和 outbox 的事实源。正式目标包含 PG/MySQL，通过领域 Repository 与 Unit of Work 适配，一个部署选择一个事实源；具体差异与验收见 [存储抽象](12-portable-storage.md)。Valkey 只存可丢弃/可重建的限流计数、短时流程缓存和公开配置缓存；任何会影响“一次性”“已撤销”“唯一绑定”的最终状态必须在事务数据库判定。高频内省的每次读取不默认同步写审计表，是否保留明细按风险、法规和容量分别制定。用户量 1000 万不是自动分片理由；先用真实分布与大 App 热点压测。

每张 App 所有表都有非空 `app_id`，主键优先 `(app_id, id)`，跨表外键必须包含同一 `app_id`；所有自然唯一性在 App 内约束。全局唯一的 `client_id` 另有唯一索引，入口解析后仍验证所属 App。平台表无 `app_id`，只通过独立 repository 和数据库角色访问。`created_at/updated_at` 用 UTC、`version` 用于乐观并发；删除采用明确状态与保留期，不能将硬删混入鉴权热路径。

## 3.2 逻辑数据字典（首版候选）

| 表 / 聚合 | 关键字段与唯一约束 | 事务不变量 |
| --- | --- | --- |
| `apps` | `id`、不可变 `app_key`、`slug`、`issuer`、`status`、`config_version`；`app_key/issuer/slug` 分别唯一 | issuer 发布后不可原地更改；状态变更有审计 |
| `platform_operators` / `platform_operator_roles` | 平台主体、MFA 状态、管理角色、恢复凭据 | 独立于任何 App User；敏感管理需近期 MFA |
| `clients` | `(app_id,id)`、全局唯一 `client_id`、`type`、`status`、`allowed_grants`、`redirect_uris`、`allowed_audiences`、`allowed_introspection_audiences`、`version` | redirect 逐条精确验证；客户端秘钥单独密文版本 |
| `users` | `(app_id,id)`、`status`、`profile_version`、`session_epoch` | ID 永不复用；禁用提升 epoch 并撤销在线状态 |
| `user_identifiers` | `(app_id,id)`、`user_id`、`kind`、`lookup_digest`、加密值、`verified_at`、`status`；对 active 已验证标识唯一 `(app_id,kind,lookup_digest)` | 标识仅经验证后占用；转移要事务保护唯一性 |
| `credentials` | `(app_id,id)`、`user_id`、`kind`、算法/参数、hash 或密文引用、`version` | 密码 hash 永不通过普通用户 API 暴露 |
| `provider_connections` | `(app_id,id)`、`issuer`、Secret 引用、状态、映射版本 | App 自有，上游连接禁用后不能新建绑定 |
| `external_identities` | `(app_id,id)`、`user_id`、`connection_id`、上游 issuer/subject；唯一 `(app_id,connection_id,upstream_issuer,upstream_sub)` | 绑定主体必须在同 App；解绑保留审计 |
| `auth_flows` / `challenges` | `(app_id,id)`、Client/步骤/绑定信息、摘要、尝试计数、过期、状态 | 一次性消费，状态转移原子化；可按时间清理 |
| `sessions` | `(app_id,id)`、`user_id`、handle 摘要、`auth_time`、强度、过期/撤销、版本 | Cookie handle 不可跨 App；退出在线生效 |
| `oauth_grants` / `auth_codes` | `(app_id,id)`、Client/User/session/scope/audience、code 摘要、绑定与过期 | code 一次性；grant 不扩大同意范围 |
| `refresh_families` / `refresh_tokens` | `(app_id,id)`、Client/User/session、代次、摘要、状态、到期 | 轮换在一个 family 事务内串行；重用撤销家族 |
| `app_admin_grants` | `(app_id,user_id,role)`，固定 owner/admin/auditor | 同 App User；管理请求读取当前授权，最后可用 owner 受事务保护 |
| `audit_events` / `outbox_events` | `app_id` 或平台 scope、event_id、主体/动作/目标/结果、时间、投递状态 | 必须审计的操作与其事件同事务提交 |
| `idempotency_records` | scope、principal、operation、key、request_digest、response pointer、expiry | 同 key 同请求重试返回同一结果；异请求冲突 |

以上是领域字典，不是直接可执行 DDL。尤其 `user_identifiers` 的 active 唯一约束、历史记录及删除保留需先以查询/迁移用例写出完整 DDL，再由迁移验证。对敏感标识保存加密原文用于通知、显示；查找用规范化后的带密钥 HMAC 摘要，避免直接明文索引。HMAC 密钥按 App/用途派生且版本化；轮换须支持双读/重算和碰撞/唯一性验证，不能简单更换 key 导致旧账号找不到。电话号码/邮箱的可搜索加密、数据导出和删除要求还需隐私评审。

### 复合外键示意（PostgreSQL 语法，MySQL 用独立 DDL）

```sql
CREATE TABLE users (
  app_id uuid NOT NULL REFERENCES apps(id),
  id uuid NOT NULL,
  status text NOT NULL,
  version bigint NOT NULL DEFAULT 1,
  PRIMARY KEY (app_id, id)
);

CREATE TABLE sessions (
  app_id uuid NOT NULL,
  id uuid NOT NULL,
  user_id uuid NOT NULL,
  handle_digest bytea NOT NULL,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  PRIMARY KEY (app_id, id),
  FOREIGN KEY (app_id, user_id) REFERENCES users(app_id, id),
  UNIQUE (app_id, handle_digest)
);
```

真实 DDL 还需状态 CHECK、索引、数据类型长度、更新约束与迁移。仅靠应用层 `WHERE app_id = ?` 不足以防止插入一个 App2 的 `user_id` 到 App1 Session；复合 FK 是必要防线。

## 3.3 隔离执行路径

1. **入口**解析并验证 AppContext。对未知 App/Client 不做用户数据查询。管理员跨 App 列表由平台权限专用接口处理，不借用普通 App repository。
2. **查询**必须以 `app_id` 为第一范围，并对关系资源做复合 FK 或显式归属 JOIN。代码生成器可以生成 SQL，但评审关注实际 SQL 是否带租户条件；禁止隐式的 `SELECT ... WHERE id`。
3. **数据库角色与引擎额外防线**：各后端分离 App、路由、平台与迁移访问。PG 生产画像额外强制 RLS，每个 App 事务先 `SET LOCAL authova.app_id`；运行账号不得是表 owner、superuser 或 `BYPASSRLS`。[官方说明](https://www.postgresql.org/docs/current/ddl-rowsecurity.html)。PG 策略见 [rls-v1.sql](rls-v1.sql)。MySQL 共享表没有这套行策略，依靠作用域查询、复合 FK、参数化/最小权限及反例测试；不具备“漏条件读也由数据库拦住”的同等防线。部署能力区分见 [12.5](12-portable-storage.md#125-隔离能力必须如实区分)。PG RLS 使用普通事务变量，也不能单独抵御任意 SQL 注入。
4. **缓存**键形式至少含环境、App ID、对象类型、对象 ID、版本。不得缓存裸邮箱查找或跨 App 共用身份状态。失效消息即使丢失，TTL/版本也给出上界。
5. **异步任务**入队记录 App、事件 ID、资源归属、幂等键；worker 重新查库验证状态和 App，不能直接信任负载里的邮箱或认证结果。
6. **导出/恢复**以 App 为范围，包含主体引用、会话/绑定关系、审计边界与加密密钥的交付规范。不要承诺可把一个 App 的数据直接导入另一部署并保持 issuer/sub/Client ID 不变；这是单独的迁移产品能力。

## 3.4 核心事务与并发

- 注册：规范化标识 → 写挑战/限速状态 → 验证挑战 → 单事务建立 User、active identifier、凭据和审计。两个同时验证相同 App 标识时，由唯一索引决定一个成功；另一个返回稳定冲突，不产生游离账号。
- 外部绑定：锁定上游唯一键与目标 User，检查近期认证；事务插入绑定和审计。相同上游主体不能并发绑定给不同用户；跨 App 绑定互不干扰。
- 授权码兑换：以摘要/状态/过期条件原子标记已用，写 grant/refresh family/审计；一次 code 只有一次成功，进程崩溃后重试语义明确。
- 刷新：锁 family 行，核验 Client/App/scope/session/status 与代次，旧 token 标 `used`、新 token 写入、发新 access token 元数据和审计同事务提交。外部 token 响应在提交后返回；若提交成功而响应丢失，按预定幂等恢复策略处理。
- 管理授权变更：固定角色、审计/outbox 一起提交；敏感管理请求在新主库快照核验授权。
- 平台建 App：唯一 `app_key/slug`、issuer、初始配置和审计在事务内建立；密钥材料的准备/激活分步骤状态机处理，失败不留下可发 token 的半成品 App。

所有 API 写操作明确 idempotency scope 与保留期。幂等记录不能因为 TTL 清理就让同一业务操作重复产生安全作用；安全敏感动作还要有领域唯一约束。

## 3.5 索引、分区、数据生命周期

热查询候选索引：`user_identifiers(app_id,kind,lookup_digest)`、`sessions(app_id,handle_digest)`、`sessions(app_id,user_id,revoked_at,expires_at)`、`external_identities(app_id,connection_id,upstream_issuer,upstream_sub)`、`refresh_tokens(app_id,token_digest)`。最终以 EXPLAIN/真实数据/写入成本评审，避免无用多索引拖慢登录和刷新。

优先对审计、outbox、已过期一次性凭据评估归档；分区按各后端独立设计。核心 User 表是否分区/分片取决于单大 App 热点与索引尺寸。PG 分区表的唯一/主键受分区键限制；MySQL InnoDB 用户分区与外键也有冲突，见 [多数据库差异](12-portable-storage.md#124-必须分别处理的数据库差异)。不能为了分区移除核心复合 FK。数据保留分用户、令牌、审计、供应商回执制定；用户删除需验证审计保留与加密擦除方案，不能简单级联硬删。
