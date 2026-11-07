# 首版综合 Review 与决策记录

日期：2026-10-13。评审围绕首版身份、会话、令牌、固定管理和交付；无组织、业务 RBAC/PDP 或候选空扩展。以下分开描述修正、可延后和实际证据，不以开发日志代替最终验收。

## 必须修正且已落实

| 问题 | 决策/修正 | 证据 |
| --- | --- | --- |
| 公开 API 不完整 | 已实现管理 API 在独立 OpenAPI 候选版中定义，生成 Go Client 和 TS schema | 27 操作 lint、编译、DTO断言与真实 Console 交互 |
| 平台与 App 管理身份混用 | 平台不透明会话，App 使用专用 public system Client + PKCE/MFA/独立 audience，固定角色从主库读取 | 两库 MFA/当前角色/跨 App 反例 |
| 标准库已知可达漏洞 | 固定 Go 1.27.2，重建 Go/OCI | govulncheck 可达漏洞 0，模块未调用漏洞另列 |
| Go JSON 公开字段可能丢失私有持久化字段 | 持久化单独编码并加密，公开 JSON 的 secret 字段排除 | 真实密码/Client secret/签名/会话流程 |
| MySQL upsert 可冲撞其他 unique key | 主键匹配条件 + 主键复核，冲突明确回滚 | PG 与 MySQL 的差异在自助会话测试中发现，修复后同套 conformance 通过 |
| refresh reuse 被错误回滚 | 安全拒绝与依赖失败分开，事务响应缓冲、拒绝持久化 family 撤销 | 并发 refresh 至多一成功、后续 active false |
| 内省库把依赖错误隐藏成 inactive | Authova 自有内省适配保留明确依赖失败 | 有效 token 预热后故障注入为 503，恢复重新有效 |
| 管理角色/会话撤销缓存旧 allow | 不缓存 positive active/角色；单主库快照 | 禁用、撤权、最后 owner 两库反例 |
| 浏览器策略和代理入口假设错误 | canonical 独立端口、Referrer Policy 与 CSP registered redirect；不放宽 CSRF/null Origin | 真实 Chrome 捕获 Origin:null 与 CSP 重定向失败并修复；最终授权链通过 |
| 自助资料变更与清除未闭环 | 最近认证、地址挑战/独立 purpose、epoch/会话撤销、至少一种登录方式、最后 owner 保护 | 密码/资料清除已有两库证据，其余用例逐项实测 |
| outbox旧 worker覆盖新状态 | attempt fencing、终止与受理状态分开 | 实现和提供方边界测试；SMTP重复投递窗口如实说明 |
| 首次 5500QPS 计划失败 | 单语句主库状态读取、路由/公钥缓存；负载生成器记录完整计划和排队延迟 | 保留失败报告，PG 1200万背景/5100计划/120秒/零drop达5097成功QPS |

## 正式交付边界

- 模块/仓库边界不是多微服务部署：Core 一个领域运行时、Console一个BFF、Contracts独立版本。
- Go stdlib、OIDC/JWT、SQL驱动/事务和生成工具分别锁定。配置/secret、数据库、协议内部类型不作为第三方稳定 ABI。
- PG/MySQL两个真实后端，PG RLS是额外防线，MySQL不宣称等价。保留实际schema/迁移/checksum、升级维护窗口。
- 代码与单测、两库conformance、浏览器、容量、备份恢复、独立安全审查是不同证据层。已写CI模板不等于远端CI运行通过。

## 可延后且不建立占位模块

自定义域名、区域分片、HSM/远程KMS、业务组织/权限、任意插件、额外数据库与其他协议不是首版需求。未来需求另行评审；当前代码没有空的相关服务接口。

## 尚需部署方/发布前关闭的门槛

真正客户环境的 TLS/网络/数据库HA、完整千万规模恢复时长与RPO/RTO、多区域/长期负载、具体短信/IdP供应商凭据接入、漏洞私密联系渠道和独立安全审查。不能把本地单机结果当作这些已完成，也不能从上游库认证推导 Authova 认证。
