# Authova 测试与质量入口

维护者：Authova 项目负责人；任务 `org-project-testing-v1-adoption`。
本文件登记测试合同，不代表产品已部署或本次测试已通过。

## 固定规范与接入状态

遵循组织测试规范 v1.0.0，已精确安装器林 `engineering/project-testing@1.0.0`。
本仓安装副本位于 `.agents/skills/project-testing/`，由官方 CLI 管理；项目运行不依赖 sibling 文件或 Skill 目录存在。
源冻结资料为组织 `Workbench/docs/testing-policy/v1/` 的 POLICY、RESEARCH、ADOPTION 和 PROJECT-TESTING.template。

- release-manifest.json SHA256：`8692da6e7cd0f110df8a023dff209074bc68ce4ea8f3b297be65983f4e4e2e46`。
- POLICY.md SHA256：`4862382b5054b23ba6c3e6a93d73880284020c3484a4f18b9d8ca064a679bcba`。
- 本轮已核 manifest 的 13 项来源；这不是器林 registry artifact digest。
- canonical 已正式发布 `engineering/project-testing@1.0.0`，artifact SHA256：
  `090ea71a804eed82cad2b94f62ed2a3bf70653d29dfb0d976b1cd682f17a5c36`。
  已只读核对发布回执 `Workbench/kitarbor/docs/evidence/project-testing-v1-canonical-release.json`，
  文件 SHA256 `564aac6ded194300a32b9996c6427517032335c72cef4bcb5cdb5ef86a73f46c`。
  回执范围为 canonical 发布/字节回读，不包含本项目安装。
- 本仓精确安装、完整性核验、offline doctor、history committed 和安装后显式读取：**PASS**。
  原 manager `/Users/chenhuanxiang/Workbench/.kitarbor-local-coordination`、target `authova-project-lead-v1` 复用，无 target add。
  实际计划 `op-914a87e24165d17d3dfd88e4`，CAS plan digest
  `303ad4f834960171fa9f772923fb43f27a09db966b314023fe0bee29844e74f7`；不与 artifact digest 混用。
  17 个发布文件的 hash/size/mode 与 canonical 回执逐项一致，另有 1 份 CLI 安装 receipt。
  `local list --target authova-project-lead-v1 --verify --json` 完整性 unchanged、无 findings；
  使用真实 ENTRY_ID 的 `local inspect` 仅核来源/version（其 integrity=not_checked，不能当完整性证明）；
  `doctor --offline --target authova-project-lead-v1 --json` native 0，history 为 committed。
  remote named doctor 未实现，本轮没有调用。
- 已显式读取本仓安装的 `SKILL.md`、`references/docs/testing-policy/v1/POLICY.md` 全文
  （含 §1.1、§3.1–3.3）、`ADOPTION.md` 和 `PROJECT-TESTING.template.md`。
  原 AGENTS/marker 完整 bytes/mode、原 target 和既有 profile-install history 均不变；没有改旧 immutable profile。
  来源、ENTRY_ID、各命令退出与保原证据见 [本仓安装回执](docs/testing-install-v1.json)。
  安装阶段仅执行获授权的 canonical HTTPS CLI 读取与本地安装，不执行产品行为测试。

按规范 §1.1 选择最小充分集合：先查相邻覆盖，用行为断言保护真实风险。
不机械测 getter、纯透传、静态文案或实现文本，不设统一覆盖率目标，不批量删测/改名。
Go 错误 v1.0.1 和 MySQL 优先的更严格规则继续适用，分别见
[错误映射](docs/go-error-handling.md)、[数据库合同](docs/mysql-first.md)。

## 所有者、目录和发现

只有根 `go.mod` 一个自有 Go module：`github.com/carvebloom/authova`。
`console`、`contracts` **同属这个 module**；根 `go test ./...` 包含它们的 Go 包。
`build-all` 只构建 Core、BFF、Query CLI 三个产物，不能代替这些包的测试。

| 所属范围 | 实际发现与可见性 |
| --- | --- |
| `cmd/`、`internal/` | Go 同目录 `*_test.go`；领域/用例、存储、协议、传输与 Worker。跨包真实集成位于 `internal/integration` |
| `console/cmd/`、`console/internal/` | BFF、会话/CSRF/代理与 TLS 配置；不接触 Core DB/主密钥 |
| `contracts/{sdk,mcpquery,cmd,examples}/` | SDK wire、Agent/Query、编译 CLI 与 MCP；不通过导入服务端 internal 绕过公共合同 |
| `console/web/src/` | Vitest 显式 include `src/**/*.test.{ts,tsx}`，jsdom + `src/test/setup.ts`；源码旁放测试 |
| `tests/e2e/` | 根 Playwright `testDir`，`*.spec.ts`；不会被 Console Vitest 重复发现 |
| `scripts/tests/` | `make test-python`（unittest discover，pattern=`test_*.py`）；新增脚本回归自动进入 CI |

现有 Go 测试均为同包测试，可直接验证未导出状态/错误；以后可按公开行为选同目录
`package foo_test`，其可见性由 package 声明决定，不由文件名决定。
不为测试导出生产函数，不用 unsafe/反射/go:linkname 绕过边界。
跨目录同名 package 不是同一包。helper 优先保留在使用它的 `_test.go`，
跨文件 fixture 如 `internal/integration/core_test.go` 保留，不新增通用 testkit。
生成的 `internal/ent`、Proto、SDK 生成代码用生成一致性和消费者行为覆盖；不逐实体补机械测试。
第三方依赖、node_modules、vendor、dist、归档和私有运行输出不纳入自有发现。

唯一特殊 Go 标签是 `contractcombo`，3 个文件/3 个顶层用例，
由 `make contract-combination` 显式运行；普通 Go profile 不假称包含它。
组合入口新增非空/三消费者完成核验，沿用未指定 dialect 时选 both 的旧行为。
没有新增 build tag 或全库重命名。当前无平台专用测试后缀；目标 CI 为 Linux，
其他 OS/架构的 Go 编译/执行、race 支持另行核验。
Python 的 `scripts/tests/go-errors.py` 是启动真实 DB 的 **runner**，不是 discover 测试文件；
旧 `*_test.py` 若未来出现必须单独登记，不能假定 `test_*.py` 会发现。

### 静态盘点（2026-11-13，不是 runner PASS）

基线 `55484837ca46998fab9c99ee56f2f0d548ba25ef`：130 个 Go 测试文件，32 个含测试包；
词法发现 240 个非 contractcombo 顶层 Test，另 3 个 tagged Test。
本次未增删 Go 用例。Console Web 的 33 个文件、Playwright 的 4 个文件在改造前后相同。

新增 `scripts/test_profiles.py --list` 的词法选集：

- unit Go：11 包 / 54 个顶层 Test，另含 2 个 Python 纯测试文件/7 用例，无外部服务。
- component Go：19 包 / 85 个顶层 Test；排除 12 个包限定的外部 Valkey 用例
  （11 个不同名称，`TestValkeyTLSACL` 在 Core/Console 各一处）。
- database：4 包 / 85 个顶层 Test。包含 `internal/integration`、`internal/storage/sqlstore`，
  并保留既有 CI 的 delivery/runtimehttp 门禁；其中 10 个组件用例是有意的已有 CI 重叠。
  14 个容量/备份恢复/浏览器 campaign 不在这个默认 DB profile，名称随 `--list` 公布。
- Python：旧 CI 点名 1 文件/1 用例；本提交后目录发现包含 2 个跟踪文件/7 用例。
  工作区另有旧任务未提交的 `test_real_login_local.py`（16 用例），也符合发现规则，
  但**不纳入本提交、不把它当 CI 已有成果**。

此盘点使用标准库词法/AST，不解析 Go 平台约束，不替代 Go/Vitest/Playwright 的实际收集。
新增/移动 Go 用例后先 `make test-discover`，再在获准窗口执行对应 profile；
profile 核对真实 Go JSON 完成事件。新增 package 默认归 component，若需要外部依赖必须先更新登记。
源码签名/标签变更造成选集不符应修登记，不能以空选集或加 Skip 兜底。

## 真实入口与 CI

所有下列命令 cwd 为仓库根，除明确标注外。依赖必须先在获准环境准备，默认入口不安装工具。
Go 按 `go.mod` 固定 1.27.3；CI setup-go 读取该文件。Node CI 固定 26，依赖由各 lockfile
决定，Console Vitest 5.0.4，根 Playwright 1.64.0；Python 标准库入口要求 3.9+。
这些是声明/配置版本，本轮没有启动工具核实际版本。

| 逻辑层级 | 实际命令 | 依赖/范围/预期 | CI 对应 |
| --- | --- | --- | --- |
| source discovery | `make test-discover` | 只读 4 个 Go profile；不是行为测试 | checks |
| unit | `make test-unit` | 上述 11 包 + Python 纯测试（`PYTHON_UNIT_FILES`）；进程内 fake、自有临时文件，无 listener/DB/容器 | checks 的旧完整 Go race + Python discover 覆盖同一范围 |
| component | `make test-component` | 19 个 Go 包 + Python discover + Console Vitest；自有 httptest/SMTP listener、短 CLI/MCP 子进程，不启动 DB/Docker | checks + console-and-generation |
| Python 单独 | `make test-python` | 全部 `scripts/tests/test_*.py`；纯逻辑与受控短子进程混合，保守归 component | checks，替代固定单文件命令 |
| Web 单独 | `make test-web` | 33 文件；maxWorkers=2，未配 passWithNoTests | console-and-generation |
| integration | `make test-integration` | 已准备的独立 MySQL 8.4 + Valkey + runtime/migrator/key；4 包真实 race，native/完成/Skip 校验 | mysql-primary |
| PG 兼容 | `make test-integration TEST_DATABASE_DIALECT=both` | PG + MySQL 独立受限账号；两库必需，绝非 PG 默认 | postgres-compatibility，在 mysql-primary 后 |
| Valkey TLS/ACL | `python3 scripts/valkey-tls-gates.py --evidence .migration/tls-UNIQUE` | 显式独立 Docker/OpenSSL/mTLS/双 ACL/停复测；新目录必须不存在 | 独立发布门禁，当前无常设 CI job |
| SDK/CLI/MCP 组合 | `make contract-combination` | 3 个 `contractcombo` 用例/所选方言叶子必须完成 + 固定契约生成检查；真实 DB 必需，构建 CLI/MCP | 两个 DB job |
| race | `make test-race` | 兼容委托旧 `make test` 的全 module race/count=1；不是全 DB 通过证明 | checks 的 make check；DB job 另有真实 race |
| e2e | `AUTHOVA_E2E_OWNED_FIXTURE=1 make test-e2e` | 根 Playwright，4 个 legacy spec，专用 .dev 夹具/Chrome/真实服务必需，见下方限制 | 无常设 browser job |
| check | `make check` | 旧 gofmt(cmd/internal)、vet 全 module、全 Go race；不含 UI/Python/生成；旧语义保留 | checks |
| types/static | `npm --prefix console/web run typecheck`、`run lint`、`run format:check` | 与 Vitest 分别通过；另有 CI govulncheck1.9.0、staticcheck0.8.2 | checks / console-and-generation |
| generate | `make generate-check` | api-check + ent-check + Console contract check，固定工具预备；无差异才通过 | 现有 generation steps + Ent git diff |

`make integration` / `quality-local` 仍调用 `scripts/tests/go-errors.py`：创建**自有随机** MySQL+Valkey，
构建/迁移/复迁/受限角色/全 module race/契约组合并清理；`--dialect both` 为显式兼容。
`migration-test`、`schema-plan-test`、`migration-upgrade-test` 保持原专用语义和证据合同。
`make test` 仍是 `go test -race -count=1 ./...`，`make verify` 仍是既有更重的边界、生成、vet、
DB quality 与 Web 组合，二者均未改成较小集合。Contracts cwd 的 `make test` 仍是仅该子树
vet + race；`make check` 另外 lint/gofmt/generated，不能误认为检查全 module。
Contracts lint 的 `npx --yes @redocly/cli@2.54.3` 会联网，必须显式准备/授权。

### 必需数据库 profile

`test-integration` **不创建/迁移/销毁服务**。调用者先通过既有 runner/CI 创建自有 fixture，提供：

- `AUTHOVA_TEST_DISPOSABLE_DATABASE=authova_quality_<12位随机小写hex>`；只传本次 schema，
  不能借已有个人、Lab、迁移或 staging 实例。仅配置 marker 不构成资源归属证明。
- 每个选定方言的 `AUTHOVA_TEST_MYSQL_DSN` / `AUTHOVA_TEST_POSTGRES_DSN`，以及对应
  `AUTHOVA_TEST_MIGRATOR_MYSQL_DSN` / `AUTHOVA_TEST_MIGRATOR_POSTGRES_DSN`。
  runtime 必须受限；PG NOSUPERUSER/NOBYPASSRLS + FORCE RLS；MySQL 不宣称 RLS。
- `AUTHOVA_TEST_VALKEY_ADDRESS`、本次新密钥 `AUTHOVA_MASTER_KEY_FILE`；配置不输出值。
  production mTLS/ACL 另跑上表 TLS 门禁，普通明文 Valkey DB fixture 不能代替它。

未选方言仍存在 DSN 时拒绝，需明确选 both 或由 owner 构造隔离环境；不静默 fallback。
缺配置在运行 Go 前失败。实际连接/角色/表结构仍由真实用例验证。
保持原 CI 两阶段顺序：先 integration 包完成，再 storage/delivery/runtimehttp，
防止两个包并发操作同一 schema。Go -p=2 不等于数据库隔离。

`scripts/test_profiles.py` 要求 native exit=0、每个包完成、所有选定顶层用例完成、
没有 fail 事件。选定方言的 Databases/ProductionTLS/ReadOnlyAgentQueries/EntBoundTransactions/AgentConcurrentRefreshReplay/
AgentRecoveryQuarantine/CredentialWriteConcurrency/IdentityRecoveryLifecycle（脚本 `INTEGRATION_CASES`），以及 Ent 映射、持久投递分类、
公平/Worker 隔离、maintenance、Atlas 升级、key rotation 子用例还必须明确 PASS。
仅允许 sqlstore 缺席方言的 8 个**精确叶子名称** Skip；单库 `TestAtlasPlanningCLI`
可 Skip（双库 job 必需）；PG-only 的 `TestOutboxMySQLIdentifierCase` 可 Skip。
完整名单在脚本 `OTHER_DIALECT_CASES` / `allowed_skip`，禁止通配环境 Skip。
unit/component/contract profile 不接受 Skip；Python 必须非空、无 Skip/expected failure。14 个 opt-in campaign 是**未选中、尚未验收**，不算 PASS。
旧 `go test ./...` 的可选 DB/Valkey Skip 行为保留，不能用其退出 0 认领必需真实层成绩。

## 关键行为与最小充分集合

- `TestDatabases` 覆盖 App 作用域/原子性、真实 OIDC 授权码/撤销、refresh 竞争/复用、最后 owner、
  OTP 单次消费、持久失败预算及管理 MFA/实时角色；不为每个 helper 复制同样断言。
- `TestReadOnlyAgentQueries`、Agent grant/tenant/actor/revoke/concurrent refresh、Passkey HTTP、
  identity recovery、credential concurrency 和 `TestSensitiveWritesRejectSingleSessionRevocation`
  保留跨身份拒绝、撤销与无副作用。WebAuthn HTTP ceremony 不等于真实浏览器/硬件验证。
- `TestProductionTLS` 真实 HTTPS chain/issuer/code/cookie/Origin，协议包的 CORS/JOSE 负例、
  SDK wire 与 Proto JSON presence/false/null/enum/secret 兼容按各自真实边界验证。
  fixture 外部身份提供方不冒充真实厂商认证。
- sqlstore 的 unknown commit、不重放、回滚/cleanup 主次、Ent 同事务、复合约束、
  outbox 至少一次/租约 fencing/持久失败分类；delivery 的已接收 ack 未知与发送未知保留区别。
- Console 真实共享会话/权限/CSRF/refresh fencing 需要外部 Valkey；组件 profile 排除的
  12 个用例由 TLS runner 的完整 Console/指定 Core gates 承接，不能据无服务组件绿灯跳过。
- UI 的表单、scope、异步迟到/拒绝、secret 单次响应等使用已有 33 个 Vitest 文件，
  不对静态 class/文案新增重复测试。

共享错误/序列化/身份/schema 变化时不能只挑自己的 unit：至少补直接消费者、
`contract-combination`、真实 DB 和受影响 BFF/Web。选集失效回到已登记的完整相关层，
不得回退到零测试；本机重型执行仍需资源窗口。

## 资源、时间、证据与产品边界

profile 将顶层 Go 限于 GOMAXPROCS=2、-p=2、-parallel=2、-count=1，单测试二进制 timeout=10m
（保持 Go 旧默认上限）。unit/component 子进程移除 AUTHOVA_/COMPOSE_ 输入，
避免自动接到调用者实例；GOWORK=off、GOTOOLCHAIN=local、GOPROXY=off。
先显式准备固定 Go 依赖；CI 增加具名 `go mod download` 联网步骤。
现有 integration helper 会调用 `scripts/migration/go.sh`，其本机缓存代理可能回退公网，
因此 **GOPROXY=off 不是完整集成离线保证**；需要离线证明时由受审外层固定材料/网络另验。
npm ci、浏览器安装、API --install-tools 和 OCI pull/build 均是独立授权动作，入口不暗装。

CI job deadline 为 checks/console 30m、DB 各 40m，覆盖准备与多个 Go 包；它们是防挂边界，
不是已测性能承诺。profile 不另创建 fixture，也不代管父 job 的绝对 deadline/资源回收。
本机使用既有受审外层原始窗口合同，不能反复启动子窗延长授权。
Go JSON/原始 stderr 每次留在新 `.migration/testing/<profile>-*` 0700 目录、0600 文件，
仅打印计数与相对证据位置，不上传 raw/全环境。失败不自动重试，保留第一次原件。
响应/令牌/口令/真实账号内容不能进入公开报告/fixture；脱敏合成 golden 保留其许可。
fixture 的 listener/事务/goroutine/子进程/容器仍由现有用例/runner 持有原 handle 并清理，
cleanup 失败与主失败分别报告；不得清理长期 staging 或其他 run 的资源。
不批量删 Sleep：内存竞争优先 barrier，真实 I/O 有界 readiness，TOTP 必须遵守真实时间窗。

历史 Playwright specs 读取 `.dev/bootstrap.json` 等文件且固定 18080/18090 等旧 demo 地址，
`AUTHOVA_E2E_OWNED_FIXTURE=1` 只是显式意图确认，**不是这些实例归属证明**。
运行前必须逐项核对专有 fixture/端口/身份；不能拿旧 demo/个人服务来凑 PASS。
默认不自动运行 demo 脚本、不读 .dev 凭据、不启动浏览器或 staging。
独立新 Console 密码/TOTP + 普通 RP 授权码验收仍是另一稳定任务；Console App 管理员 OIDC、
canonical issuer 装配以及实际部署/登录尚未因此完成。没有 end_session 的提供方不能声称 global logout。

Python 本地与 CI 使用同一 discovery，命名匹配但没有 TestCase 也失败；不依赖 unittest 默认的空集合退出 0。
`test-unit` 只选已审核的 2 个纯 Python 文件，新增脚本测试先由全量 `test-python` 发现，审核依赖后可登记为 unit。
`test-component` 是组合入口，其中 Python 全量含纯逻辑和短子进程，不要求开发时重复跑所有入口。

当前没有 FuzzXxx 目标，故主动 fuzz **不适用现状**，不添加空命令假通过。
将来若外部解析风险需要，先登记具体 target/语料/上界/资源，再在独立窗口
`go test <包> -run '^$' -fuzz '^Fuzz目标$' -fuzztime=30s -parallel=2`；普通 go test 不算主动 fuzz。
唯一 Go benchmark 是 `BenchmarkTokenProfileCPU`，显式命令：
`GOMAXPROCS=2 go test ./internal/protocol -run '^$' -bench '^BenchmarkTokenProfileCPU$' -benchmem -count=5 -p=2`。
不在快速集合运行，不据一次结果承诺容量。当前 coverage、warm/cold p50/p95、构建峰值均 UNKNOWN；
保留构建缓存、不得借覆盖率目标新增低价值测试或沿用旧吞吐承诺。

## 本轮验收、调度与例外

项目 owner 的源码阶段只执行标准库源码/AST/JSON/入口发现及 Git whitespace 检查，
没有 Go/Node/npm、生成器、网络、DB/OCI、HTTP、浏览器或现有服务操作。
随后 Root 在本仓串行执行了以下有限 Python 行为回归，owner 没有重跑：

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -B -m unittest discover -s scripts/tests -p 'test_test_profiles.py' -v
```

执行方 Root 回传：**6 tests / 0 skip / native exit 0**，工具 chunk `c3dece`；
运行来源为源码提交 `6b2c308f4afdb072c8f7fc1eafc32f046d48b9b7`，仅新增结果 gate 的 6 个方法。
本记录是执行方的有限回传，不是 owner 独立复跑或新 Go/Node/DB/CI 证据。
新增结果 gate 的 6 个回归方法保护 native 失败被父 0 覆盖、空选集/缺包/缺必需 case、
selected Skip 被父 PASS 掩盖、tagged 消费者遗漏、Python 空发现、方言精确 allowlist、缺失/错 fixture 配置；这 6 个方法由 Root 执行通过，其他集合仍为 **NOT_RUN**。
数据库/Console CI 配置改造未推送、未运行，旧 55484837 的 CI success 仅是旧工程证据。

建议组织串行排程（估算，不是已测峰值/时长）：

1. 最窄新增 gate 已由 Root 执行通过，见上方精确命令/producer；无相关源码变化无需重跑。
2. `make test-python`：提交态 7 个方法；工作区可能另发现旧任务 16 个方法，需先核来源。
   不启动产品服务，预算 60s / 256MiB。
3. 已有固定工具链与依赖后：`make test-unit`，再 `python3 -B scripts/test_profiles.py component`，
   Go 限并行 2；各提案 10m 外层/2GiB，cold 未知，不保证该预算内结束。
4. `make test-web` + typecheck，既有 node_modules 后单独窗口，提案 3m/1GiB。
5. 真实 MySQL/PG、mTLS/ACL、生成、构建产物/浏览器与远端 CI 单独排程，
   不将本轮静态盘点升级为这些运行成果。

| 例外/未覆盖 | 替代与范围 | owner / 复核点 |
| --- | --- | --- |
| 14 个 opt-in 恢复/容量/soak/browser campaign 不在默认 DB profile | 保留原用例、runner 和历史失败，变更相关能力时单独选中真实资源验收 | Authova owner；相关发布前，最晚 2026-12-13 复核 |
| 单库 Atlas CLI 和缺席库叶子允许 Skip | both 兼容 job 必须执行全部；未知/selected Skip 失败 | Authova owner；每次 DB/CI 变更 |
| TLS/浏览器无常设 CI job | 保留独立真实门禁；普通 CI 不声称 production TLS/产品 E2E 全通过 | Authova owner；相关发布前，最晚 2026-12-13 复核 |
| legacy check 的 gofmt 只查 cmd/internal | 保留旧合同；console/contracts 静态与生成分别登记，不推断已全量格式检查 | Authova owner；触及对应子树时 |

本地回退只 revert 本任务提交，恢复原 CI/Make/Vitest 的入口；无 schema/依赖/业务数据变化。
这不回退或改写既有 MySQL/Go-error/登录任务成果，不删除失败证据或受管规则。
Git revert 不会卸载本地 Skill；受管安装回退必须另用官方 plan/CAS，不能手删目录或 marker。
安装前的 NOT_RUN 状态保留在历史提交 `db58b35`；当前安装已完成，其他未验证集合不因此变绿。
