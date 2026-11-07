# 首版契约待定决定

这些问题会改变公开 API 或 SDK 行为，发布 `v1` 前必须定稿。当前 OpenAPI 文件只是评审草案。

| 决定 | 当前倾向 | 定稿所需证据 |
| --- | --- | --- |
| App ID 与 slug | 内部稳定 UUID；slug 用于人类可读配置，但不作为权限依据 | App 重命名、迁移和 issuer 稳定性的设计 |
| 每 App issuer URL | 同一个域名下 `/a/{immutable_app_key}`；Client ID 全部署唯一 | OIDC Discovery 兼容测试、反向代理与自定义域名部署验证 |
| 平台管理员如何取得令牌 | 平台 issuer Code+PKCE、Console Go BFF、首次部署 CLI 引导与 MFA；App admin 从本 App issuer 登录 | 管理 audience、管理员恢复和 BFF 会话安全测试 |
| `Idempotency-Key` 保留期 | 普通创建 24h；含 Client Secret 的加密重试响应 5 分钟 | 数据库存储方案、重试与并发故障测试 |
| App 状态及暂停行为 | `active` / `suspended` | 暂停时已有会话、刷新令牌和管理 API 的预期行为 |
| Go SDK 的生成器与发布节奏 | 从批准的 OpenAPI 生成核心类型和调用代码 | 工具兼容性、跨版本测试和可维护性评估 |
| App 级用户、会话和固定管理 API | `docs/api-v1-detailed.md` 与 `openapi/drafts/app-core-v1.yaml` 已给字段级评审稿，未发布 | 固定角色矩阵、最后 owner 保护、用户隐私、撤销和大规模查询评审 |
| 内省状态一致性 | 主库新快照，无 positive active 缓存；独立 `allowed_introspection_audiences` 限制调用 Client | 退出/封号/暂停后可见、服务 Token 分支、跨 audience、依赖失败不假定 active；PG/MySQL 分别验收 |
| 多数据库外部一致语义 | PG/MySQL 共用 API 与存储 conformance；物理 schema、额外隔离能力分开 | 唯一性/一次性/事务/提交未知/快照；不把 PG RLS 能力归给 MySQL |
