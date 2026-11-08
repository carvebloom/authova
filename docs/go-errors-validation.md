# Go 错误处理与当前 v1 基线验收 — 2026-11-08

当前规范为组织 **v1.0.1**，SHA256 `10e29e102ee1e7c7fc9d61ffad0e1b9458990f765be3c49491fc7a46399e6d05`；冻结来源未改。[实施及R01—R12映射](go-error-handling.md)。用户明确授权在首次正式发布前将四列投递诊断并回 `001_initial.sql`，当前 schema 为 **1**，无产品002，后续从002编号。这是本次上线前迁移例外，不改变冻结规范。

## 当前实际检查

- `python3 scripts/tests/go-errors.py --focused`：新随机 Compose project，PG17/MySQL8.4/Valkey、随机loopback端口、随机凭据及新数据卷。两库均由当前binary在空库初始化v1；插入省略诊断列的合成outbox记录，重复migrate确认版本1、记录保留及unknown/unknown/unknown/0默认值；再次migrate通过。受限runtime账号运行聚焦race，55个测试pass事件（含父子测试），零fail、零skip；涵盖Ent物理映射/Atlas、持久分类重开读回、fencing/公平预算、维护/事务和真实HTTP提交顺序。Core/SDK/编译CLI/MCP契约组合通过。
- `go test -race -count=1` 覆盖diagnostic、delivery、transport、protocol、migrations、sqlstore、管理/查询SDK及Console diagnostic；无DSN时的DB/TLS用例有Skip，不能作为双库验收，双库以上述独立fixture为准。
- `go vet ./...`、Ent生成一致性、API/Proto/Go HTTP/OpenAPI/TS/Console SDK生成一致性、架构边界检查通过。Ent schema和生成实体已含四列，无需改变生成结果；SQL/Atlas目录回到单个001。迁移工具从目录计算LatestVersion=1和下一个002，无硬编码改版本。下一版本演练脚本改为现场构建当前binary，避免误用遗留bin/authova；只做Python语法检查，本轮不连接其旧固定迁移实例执行。
- git diff检查、冻结规范及来源摘要核对；Git身份rwasayc、GitHub活动账号rwasayc、精确公开目标carvebloom/authova核验，fetch确认远端无独有提交。增量提交保留c014110，不rewrite、不强推、不默认合并main。

具体结果/日志摘要和逐项Skip见[机器报告](go-errors-validation.json)。当前证据为Git忽略的 `.migration/go-errors-v1-focused/`、`.migration/go-errors-v1-checks/`。只删除核验项目标签的自有容器/命名卷；没有操作Lab、旧迁移实例、原卷/用户/密钥、系统信任或真实OIDC登录。

## 历史与未覆盖

此前c014110使用临时v2的完整双库729 pass事件、升级/重复migrate、TLS四门禁、Console246测试及构建，保留在[历史报告](go-errors-v2-historical.md)和[历史机器报告](go-errors-v2-historical.json)。这些不是本次归并后v1的现行验收。本轮未重跑无改动的UI/TLS及完整双库全包；未执行容量/soak/恢复快照/浏览器/外部厂商/生产部署或物理DB重启，不宣称补齐这些成绩。新Store重开读回不是物理重启或生产备份恢复。

公共Problem/HTTP状态/OAuth机器码/CLI stdout与退出语义继续保留；不可信人类消息按安全投影规范化。commit_unknown优先于取消、主次cleanup事实、受控panic与ErrAbort、至少一次投递的预算/fencing约束保持。

**既有Lab旧v1与当前001的checksum不同。新binary会拒绝；本轮没有也不得自动重建、重编号、修改history或升级这些实例。** 后续切换必须独立评估实例与数据方案，不能直接将当前基线视为旧v1的无损升级；旧v2同样不提供自动降级。

## 远端 CI 跟进

远端 run42450777972 在 service 初始化时因 health-cmd 单引号被 runner
拆分而失败；8ed71f5 已将三个命令改用双引号。后续 run42452530625 实际
运行双库集成后，仅 `TestIdentityRecoveryLifecycle` 的两个数据库分支被
安全保护拒绝：旧 CI 固定库名 `authova_quality_ci` 不满足自有随机库名约束。
不能将这两次失败记为验收通过。

本次只修 CI 配置：每个 job 使用 `secrets.token_hex(6)` 生成
`authova_quality_<12hex>`，显式创建 PG/MySQL 新库，通过 GITHUB_ENV 将
同一精确库名、受限 runtime DSN 与 migrator DSN 交给后续步骤。
恢复测试的所有权校验和破坏性操作保护不变，未关闭、跳过或放宽测试。
固定 service 初始库仅用于创建新库及健康检查。配置生成/名称与 DSN 匹配、
YAML 和 shell 语法、恢复保护单测在本地验证；完整数据库执行结果以新
Actions 为准。未连接本机现有数据库、Lab 或旧迁移实例。

run42453575807 在随机双库上通过完整 integration 及 sqlstore/delivery/
runtimehttp race；checks 和 Console/生成作业也成功。但最后契约组合脚本
在调用生成校验时发现数据库 job 未安装 protoc，因此整次 run 仍失败。
现在数据库 job 自己安装与独立生成作业一致的 protoc36.2，执行已有
`generate-api.py --install-tools --check` 安装并校验固定插件，Node也在此
步骤前准备好。没有依赖另一个job的文件，也没有跳过组合脚本的生成检查。
工具准备移至数据库初始化之前，以便配置缺失立即失败；本地同命令和YAML/
shell检查通过。最终全部作业结论以新Actions为准，原失败保留。
