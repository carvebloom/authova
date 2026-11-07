> 历史来源实施记录：保留当时的 schema v8 与迁移描述；当前未发布仓库已合并为 [v1 基线](../migration/VERSIONING.md)，请勿按本文旧版本步骤初始化当前实例。

# 只读 Agent 查询入口

实现跨 Core/Contracts；不新增服务边界、组织模型或数据库迁移。schema 保持 v8。

## 核心边界

- 使用现有 owner 审批的 confidential/autonomous Agent 与 installation，不给运维 Agent 人类管理员凭据。
- 单一专用 audience：`urn:authova:query:APP_ID`。八个读取 scope，另有独立 contacts 能力。
- Core 固定 GET `/api/v1/apps/{app_id}/query/...`；签名、primary token liveness、绑定、autonomous 模式、App/audience/scope 均需匹配。
- 与内省共用 `tokenStateMatches`，沿用 App 作用域只读事务；Agent/grant/installation 撤销和版本变更立即影响之后开始的请求，没有 allow 缓存。
- 租户凭据只允许 context、强制 tenant 的 installation/grant 列表及详情；没有业务成员目录时不能把 tenant 凭据扩大到 App 用户/会话/配置/审计。
- 用户联系方式默认掩码，完整联系人需额外 `authova.query.contacts`；配置 DTO 不含上游 client ID/secret/key；审计只含结构化元数据，排除自由文本 reason。
- 分页默认20、最大100；拒绝重复/未知查询参数和所有写方法。查询 token 不能调用现有管理端点，普通/Console/委托 token 不能替代查询身份。

## 交付物

- Core：`domain/query.go`、`protocol/query.go`、`application/queries.go`、`transport/query.go`。
- Contracts：独立 `openapi/v1/query.yaml`、`sdk/query`、`mcpquery`、`cmd/authova-query`。
- 可分发 Skill：`authova-contracts/skills/authova-query/SKILL.md`。
- 安装、owner 审批示例、配置、scope 表和错误语义见 [查询接入手册](../../../authova-contracts/docs/agent-query.md)。

## 本轮验收

- PG/MySQL 各完成迁移、完整 Core race 与 Core/Contracts 组合：442 个叶子用例通过、16 个显式专项夹具跳过、0 失败；其中新增查询边界18个叶子通过。
- 使用实际编译的 `authova-query -race` 子进程，通过官方 Go SDK MCP 客户端完成 stdio 工具发现、真实配置/用户/接入/授权/审计查询；两库均验证跨 App 用户拒绝和同一 MCP 进程在授权撤销后拒绝后续调用。JSON CLI 也连接实际 Core/数据库。
- 查询回归覆盖所有12条读取路径、用户与连接分页、联系方式额外授权、无秘密投影、普通/委托/平台管理凭据不得代替查询身份、跨租户列表/对象拒绝、写方法与重复参数拒绝，以及依赖错误保留503。
- Contracts `make check`、OpenAPI 确定性生成、零错误/零警告 lint、SDK/MCP race、Go vet/格式检查通过。另构建正常 `-trimpath` CLI，二进制位于 Contracts 的 `bin/authova-query`。
- Skill 通过 `skill-creator` 格式校验；提供可分发文件，没有自动安装全局 Skill，也没有把格式校验视为模型行为认证。

源码与原始日志摘要见 [机器验收记录](../../evidence/query-mcp-2026-10-21/acceptance.json)。测试均使用脚本新建并销毁的隔离数据库；未切换演示服务或演示库，未 commit/push 本轮改动。当前不额外证明持续容量、HA/PITR、真实供应商或第三方 MCP 产品互操作。
