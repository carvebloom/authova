# 当前实现状态

当前数据库采用未发布的 **v1 初始化基线**，对外 API 为 `/api/v1`。
Core／Worker／Console 独立装配，管理和查询接口以 Proto 为权威源；普通数据访问使用 Ent，版本化迁移由 Atlas OSS 执行。

- [当前验证结果](../migration/v1-validation.json)
- [完整验证报告及未验证项](../migration/VALIDATION.md)
- [数据库版本与旧测试实例](../migration/VERSIONING.md)
- [Ent → Atlas 规划流程](../migration/SCHEMA-PLANNING.md)
- [迁移前来源状态快照](STATUS-SOURCE-2026-10.md)

旧报告中的 schema v8 指来源系统的历史迁移次数；不能作为当前版本号或新实例的验收证据。
