# 对外接口清单（草案）

此清单说明最终需要哪些契约，不表示接口已实现。标准协议与 Authova 自有接口分开验收。

| 接口面 | 主要调用者 | 契约来源 | 当前状态 |
| --- | --- | --- | --- |
| App 级 OIDC 授权、令牌、发现文档、JWKS | 业务 App 的 Client | OIDC/OAuth 标准 + `docs/protocol-v1.md` | issuer URL 与端点已提案，待互操作验证 |
| 平台管理：App 创建、列表、读取、修改与暂停/恢复 | 平台管理员 | `openapi/drafts/platform-v1.yaml` | OpenAPI 草案，未发布 |
| 平台管理：App 迁移、密钥轮换 | 平台管理员 | 后续 Authova OpenAPI | 具体操作与失败语义待定 |
| App 管理：Client 与外部身份提供方配置 | App 管理员 | `docs/api-v1-detailed.md` + `openapi/drafts/app-core-v1.yaml` | Client 关键接口已提案；Provider OpenAPI 待补 |
| App 用户：查询、禁用、身份绑定、会话撤销 | App 管理员及授权服务 | Authova OpenAPI | 隐私、审计、撤权时效待定 |
| Token 在线内省 | 已注册机密资源 Client | OAuth Introspection 标准 + `docs/protocol-v1.md` | 当前 active/撤销读取语义已提案，待故障和容量验证 |
| 固定管理员授权 | 平台/App owner | 管理权限矩阵 + Authova OpenAPI | 固定角色与最后 owner 约束需补齐端点契约 |
| 登录页面内部流程 | Authova 默认登录界面 | 核心服务内部契约 | 不作为首批公开 API |
| 邮件、短信和非标准身份提供方适配 | 部署方扩展进程 | `docs/adapter-v1.md` | 流程、错误和信任边界已提案，待版本化测试向量 |
| 审计查询与导出 | 平台及 App 管理员 | Authova OpenAPI | 保留期和隐私范围待定 |

平台 App 契约与 App 级 Client、User、Session 的关键接口已有独立 OpenAPI 草案；其余接口先在 `docs/api-v1-detailed.md` 做字段级评审。所有草案都不能视为已发布契约，须通过领域语义、跨 App 反例、OpenAPI 校验与服务端兼容测试后再移入 `openapi/v1/`。
