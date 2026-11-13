# 当前运行与迁移说明

新仓库尚未发布，经用户明确授权，四列安全投递诊断已并入 **v1 / 001_initial.sql**，当前schema版本为1，后续从002连续编号。原v2验收只作历史证据，当前以[本轮验证](../go-errors-validation.md)为准。已运行Lab旧v1的001 checksum不同，新binary会拒绝；不得自动重建、改写版本/历史或替换实例。本轮未操作既有实例。

新服务端首次部署默认 MySQL 8.4/InnoDB，PG 显式兼容；[政策映射与门禁](../mysql-first.md)。不自动改已有DSN或fallback。Core、Worker、Console 分别运行；Console 只通过 Core API 访问身份能力。迁移使用独立凭据，运行账号保持受限。PostgreSQL 保留 App scope、24 张表的 FORCE RLS 和复合约束；MySQL 不宣称具有 RLS。

启动检查与迁移执行器读取同一份连续版本目录，并校验干净版本和全部 checksum。迁移持有部署锁并写入 Authova／Atlas 两套账本。事务或迁移结果未知时不自动重放非幂等操作。

- [隔离实例与启动命令](../migration/RUN.md)
- [v1 基线、旧库保留和后续升级](../migration/VERSIONING.md)
- [双库版本化迁移](../migration/MIGRATIONS.md)
- [审核候选生成与受保护对象](../migration/SCHEMA-PLANNING.md)
- [历史来源运维手册](OPERATIONS-SOURCE-2026-10.md)

旧数据库不会通过修改版本数字变成 v1；旧业务数据、身份、issuer 和密钥的迁移须单独规划。生产入口／数据库 TLS、容量与备份恢复演练的未验证范围见 [验证报告](../migration/VALIDATION.md)。
