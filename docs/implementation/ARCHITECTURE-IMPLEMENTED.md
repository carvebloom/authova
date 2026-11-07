# 实施结构、存储与替换边界

日期：2026-10-13。实际实现位于 internal，历史 32 表设计并非运行 schema。运行 schema 由嵌入 migrations/postgres 和 migrations/mysql 的序列及 checksum 决定，当前为版本 2。

## 依赖与模块

HTTP/默认页面 → application 用例 → ports → SQL adapter；protocol 适配 ZITADEL oidc/v3，JOSE 使用 go-jose。OIDC 的库验证不代替 Authova 的租户/事务/撤销验证。密码使用 Argon2id、有限 KDF 并发；提供方网络 I/O 在事务外。标准库修复后固定 Go 1.27.2。

核心领域没有 SQL/driver 类型。ports 的 App transaction 绑定不可为空的作用域；所有方法在一条事务连接使用 App 条件，PG 另 SET LOCAL/FORCE RLS。MySQL 没有等价 RLS。适配器共用少量标准 SQL，私有 helper 处理 placeholders、upsert、错误和迁移差异；未用 ORM/动态表查询暴露给领域。MySQL upsert 遇其他唯一索引冲突不能更新另一主键，这一差异有真实测试。

管理 API 与标准协议分开错误格式。平台会话是不透明管理凭据，App manager 使用受保护系统 Client 的 OIDC/PKCE/MFA。固定角色逐请求从主库确认。业务 Client 无权请求管理 audience，角色不从 JWT 快照得到。普通资源权限由业务服务决定。

## 物理存储

关键过滤/唯一/FK/失效事实为类型化列；敏感和扩展资料是按 App/用途分离的 AES-GCM 密文 payload。内部 payload 使用 Go gob，公开 DTO 用 JSON 且凭据字段 json:"-"；因此持久化编码与公开序列化不能混用。字段重命名、类型更改和删除需要迁移/兼容测试，不承诺外部直接读取 gob 或数据库。

UUID 使用 V7 字符串，两后端用 byte-wise collation/显式 digest，时间热列用 UTC Unix 秒。表和索引按实际主要访问路径：标识 HMAC 精确查询、用户/App 主键、Session/Token、family 锁、队列领取。大量用户不意味着所有请求读全用户 payload；内省关联热状态列并只返回 Token payload。

事务 callback 默认一次，不自动重做未知提交。响应缓冲到提交成功；错误分类包含未知结果。OTP 错误计数和 refresh reuse 撤销在确定的拒绝场景中持久化，不能被错误响应的回滚清除。outbox 是至少一次提供方投递，attempt fencing 阻止旧 worker 回写覆盖。

## 热路径缓存

只在内省热路缓存 App 的不可变路由/issuer，以及 RSA 公钥与正常退休元数据（1 分钟、2048 项上限）。Client 权限、App/User/Session/family 状态不缓存 positive allow/active，仍使用主库新快照；MFA策略由该快照取得。正常 signer 退休窗口 24 小时，缓存不得据此宣称应急私钥吊销立即生效；暂停/在线状态与离线 JWT 的边界见运维文档。

## 独立项目

Core 与 Console 可独立构建和部署。Console Go BFF 的薄 API transport 从 contracts/sdk 源生成，TypeScript schema 从 OpenAPI 生成并用编译断言检查 UI DTO。无需运行时加载 sibling 项目，也不共享数据库。SDK 与 OpenAPI 是公开兼容边界，internal 包/物理 schema不是第三方稳定 ABI。

替换邮件/短信端点、OIDC连接、Console或主题是有实际消费接口的更改；不建立空业务扩展系统。更换数据库需要数据/密钥/issuer迁移与各后端验收，不能只改 DSN。Core 继续是一套运行时，组织和业务 PDP 均不在首版。
