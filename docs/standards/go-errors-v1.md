# 琢其章 Go 错误处理规范 v1.0.0

状态：**2026-11-08 冻结实施基准**。范围：Authova、Kitarbor、DataDock、ContextHarbor 的手写 Go 与相关 SDK/CLI/Web 错误边界。行业依据见 [研究](go-errors-research.md)。用户已授权跨项目改造和验证；此文件定义本轮共同要求，不能把尚未完成的要求当已验收成绩。

## 1. 核心决策

统一“错误语义、原因保留、公共投影、安全诊断、验证”，保留各项目既有 wire 格式、状态码、业务 reason、CLI 退出码和状态机。不抽新的跨仓库 SDK，不统一替换已有领域错误，不升级 Kratos/Go，不把实验的409/499回执契约推广到产品。

采用标准库 errors/fmt/context 与项目现有框架。允许必要的项目内部小类型/函数；第三方错误库只有明确不足及兼容、依赖、开销证据才引入。本轮不需要新建遥测平台或强制 OTel。

要能证明：**发生什么、在哪个操作/阶段、如何找到相关记录、可否检查原始原因、公开信息是否安全**。不承诺还原未采集的驱动内部或远程系统栈。

## 2. R01—R04：语义、因果与层次

### R01：以可依赖语义分类

返回类型为 error。需要调用方决策时使用项目 sentinel/typed error/已登记 reason；禁止依赖 err.Error 字符串、字符串前缀或子串作为新代码的常规分支。已存在字符串分类应迁移为兼容的 typed/Is 分类；不用统一字符串替换制造假类型。

使用 errors.Is/As 或版本允许的等价 API 支持包装。errors.Join 是原因树，分类器需覆盖多原因；不能只在 errors.Unwrap 循环中寻找单链，也不能以多原因排列决定优先级。

### R02：保留必要 cause，明确抽象边界

关键依赖调用失败，不得只替换成新的固定字符串而丢失原类型。添加有意义操作时用 %w、WithCause 或实现 Unwrap 的局部类型；无需额外语义时直接 return err。不要每层重复同一句包装。

存储/外部协议属于实现细节。内部可以保留驱动 cause，但业务判断依赖领域语义；公共 SDK 必须说明可检查的原因是否属于兼容契约。故意隐藏的资源存在性、认证细节可转换为领域错误，内部诊断仍应保留失败阶段；不能恢复越权信息给用户。

优先借鉴 Authova sqlstore.Error 的分离：Error()/LogValue 为受控安全摘要，Unwrap 保留底层原因，Is 表示领域分类。safe wrapper 不等于其 Unwrap 的 Error() 也安全。

### R03：取消、超时和多个失败

保留 errors.Is(context.Canceled/DeadlineExceeded)。同一操作同时匹配取消/超时，内部控制默认取消优先；测试 Join 双顺序和嵌套。查询驱动类型、普通 invalid/not-found 等不能使调用方忽略已经取消的操作。

优先级有两个必要例外：

1. **提交/副作用结果未知是独立事实**，不能被取消掩盖成“未执行、可安全重试”。Authova outcome_unknown、DataDock/ContextHarbor uncertain、器林 committed-cleanup 必须保留。
2. cleanup 的独立取消不能覆盖已经确定的主业务结果。分类器区分主失败与次失败；errors.Is(Join(...), Canceled) 不能成为进程忽略所有其他失败的理由。

取消优先首先用于停止继续工作和错误诊断，不授权静默变更现有公共HTTP/协议码。纯正常 shutdown 可成功退出；伴随真实 failure 必须保留安全诊断及原退出约定。

### R04：重试与事务事实优先

可重试分类只作决策输入，必须同时满足幂等、尝试预算、取消与副作用状态。禁止对未知 commit、DDL cutover、远程写已接收但 ack 不明自动重放。认证业务拒绝不等于事务失败；Authova OTP/reuse 的“先落拒绝计数/撤销，再返回拒绝”必须保留。

errgroup 返回首个失败，不是假定完整错误集合。Join 只用于保留确有意义的多个原因；关键 cleanup 错误须保主因，但不得将已提交结果改成未提交。

## 3. R05—R08：公共错误与安全诊断

### R05：在真实终止边界做可信公共投影

HTTP（含raw handler、filter、解码、认证、上传下载、metrics/export）、实际gRPC listener、CLI、MCP 和异步公开结果均需审计。由项目可信构造器或登记映射决定公共 status/reason/message，不因 errors.As 得到 Kratos/gRPC 类型就信任其中任意 message/metadata。

已知 reason 也不能让攻击者或上游同名错误附带的消息被原样公开。适配优先固定的安全文案/允许参数；SQL parser 的合法位置诊断、用户输入校验、文件位置等有意公开详情需列范围和测试，不能把基础设施错误当此例外。

未知错误使用项目既有 INTERNAL/通用不可用响应。Authova 的既有503与OAuth/OIDC协议错误、ContextHarbor的{code,reason,message}、DataDock ErrorInfo/SQLCheck findings、Kitarbor LOCAL_ERROR/退出码都先保持；不强制全部改RFC9457。若实际代码与生成契约不一致，应修生成来源并重现，不改数值code含义。

**Kratos FromError/DefaultErrorEncoder 不是脱敏器。Error.Error 包含 cause/metadata；gRPC FromError 对包装status可复制完整 Error()。** 不能将这些结果未经投影用于公共响应。

### R06：诊断记录采用允许字段，禁止原始文本兜底

默认禁止将任意 Error()、%+v、slog.Any(err)、HTTP响应体、请求对象、SQL、DSN、URL查询、Cookie/Token/密码/密钥、记忆或对话正文写入日志、trace、公开DB失败字段、CLI输出。

本项目明确实现安全 LogValuer/formatter 的类型可输出其审计过的字段；不要包装后重新退回原始错误字符串。诊断至少包含：

- `operation`：受控操作名；`phase`：如 connect/query/commit/send/ack/apply/cleanup。
- `kind`/稳定失败码：domain、cancelled、deadline、dependency、internal、outcome_unknown 等。
- 请求或任务关联：`request_id`/`diagnostic_id`，异步为既有 job/task/operation ID。
- 安全原因信息：允许的驱动名、SQLSTATE/数值码、原因类型名、明确的 retryability/结果未知标识。
- 关键意外错误的安全来源信息，见R08。

所有动态字段有长度/数量上限。仅通过正则擦密码不能证明安全；默认应从固定类型/允许字段构建。error tree遍历需要节点/深度上限，避免不规范Unwrap循环或巨大Join拖垮错误处理。无法识别时输出unknown和类型，不退回Error()。

### R07：一次诊断归属与关联

内部返回链补上下文，终止边界记录一次诊断。访问统计、业务审计及不同重试尝试可以独立记录，但不要逐层重复同一失败的完整日志。降级后终止传播的操作拥有自己的诊断。

HTTP由服务器生成或严格校验有界不可注入的关联ID，响应头和诊断一致；已有X-Request-ID优先兼容。客户端先保存响应头，再解析body；body损坏仍保留安全ID。BFF本地ID与上游ID可受控关联，不读取原始失败正文到UI。

有现成有效trace时关联trace_id；没有时不伪造trace，不为本轮部署新后端。OTel记录安全分类/受控事件，失败状态明确；RecordError不会自动SetStatus，直接传原error可能泄露。默认每个相关span一次适当事件，不为一次请求堆重复异常。

### R08：安全来源与panic

%w、WithCause、slog source和trace不自动提供原始失败栈。关键依赖首次被应用捕获时，应保留操作及安全来源（受控 file/function/line 或现有错误栈）；若只有边界处采集，要标记为 recorded_at，不能称原始栈。

普通意外失败可用现有runtime.Callers等有界采集，原则上捕获一次；不要求为每个预期404/业务拒绝采集完整栈。框架源文件/函数/行号可以保留，宿主绝对用户路径应归一化，禁止参数和请求值进入栈表示。验收至少一个真实失败路径能定位首次应用接触点。

panic边界必须覆盖实际HTTP raw/filter/service以及各独立goroutine职责。**不能只给Kratos默认Recovery添加WithHandler，因为默认实现可在handler前打印完整请求及panic值。** 替换为受控恢复或在其前安全拦截；默认不输出任意panic值/请求，只记录类型、受控frames与关联ID。

已经写出的响应不能再伪造完整JSON；记录并按现有连接语义终止。worker panic按项目监督策略结束该工作单元或进程，不允许静默失去worker而HTTP仍显示健康；安全不变量不能被恢复成成功。CLI stdout/MCP stdio不放诊断。

## 4. R09—R12：异步、cleanup、兼容与检查

### R09：异步失败可跨重启辨认

复用现有 task/state/error_code/ledger，不为短期只读扫描创建新队列。关键持久操作的失败至少可从既有ID得知阶段及稳定原因类别，保存有界安全分类而非原始 Error()。如已有字段可表达，先复用；缺字段则用向后兼容、增量迁移。

finish/ack/落失败记录本身失败时同时保留首因与持久化失败，记录可关联诊断；零匹配行要明确lease/fencing丢失，不能报成功。已有uncertain、quarantine、pending保护、committed-cleanup事实、attempt fencing和backoff继续生效。provider accepted/ack unknown需与发送前失败区分。

### R10：关键cleanup不丢，合理忽略明确

检查会影响数据/发布完整性的commit、flush、writer.Close、Sync、rollback异常、释放租约/锁、安全撤销补偿和shutdown flush。保留主因与次因并明确操作结果，cleanup使用有界上下文，不从已取消请求无限等待。

正常ErrTxDone、已不存在、某些只读Close、固定安全常量序列化可有有依据的忽略。禁止机械修所有defer/errcheck、把次错误当可重试主失败、吞Join中的非取消错误。

### R11：兼容与实施

保留现有HTTP/JSON/OAuth/gRPC ErrorInfo、SDK异常类型、CLI stdout/stderr/退出码。补充诊断字段允许以可选增量形式加入；status/reason重命名或状态机/重试变化必须列出兼容策略，不能趁本轮统一悄悄改变。

新增DB字段必须新的Atlas/项目迁移，不改历史checksum/旧schema；测试空库与旧schema升级、旧记录默认unknown、双数据库（若项目支持）。本地state升级须备份、兼容版本拒绝和恢复。生成文件必须由来源生成；ContextHarbor无HEAD时以受控文件快照验证，不能用空Git diff证明一致。

先收紧公共投影和日志，再补cause/诊断。改真实缺陷与关键路径，不对vendor/research/生成代码机械替换。本轮默认不推送、不部署、不重启Lab，不改变真实全局Agent设置。

### R12：可执行验收与规则落实

必测矩阵：

| 验收 | 通过条件 |
| --- | --- |
| 链与分类 | 多层包装Is/As原始类型可达；Join取消双顺序；主/次错误优先；unknown-commit不可重放 |
| 公共安全 | 正常已知契约不变；未知/伪造Kratos/gRPC/code/metadata/upstream错误不回显canary |
| 日志与遥测 | HTTP/log/stderr/trace/worker公开结果均无合成秘密；仍有op/phase/关联及原因类型/来源 |
| panic | 带合成密码的请求、任意panic值不进日志；受控栈存在，覆盖真实入口，不依赖默认泄密recovery |
| 关联 | 一次失败的header/body可用ID、服务器诊断及SDK/UI关联一致，截断body不丢headerID |
| 异步 | 首因和finish失败不互相覆盖；安全分类/既有ID可读回；lease丢失不误报成功 |
| cleanup/兼容 | 次错误保留且结果不变；正常关闭不误报；新迁移/生成/旧客户端兼容证据 |

测试注入合成DSN、token、SQL、密码、响应正文标记，不使用真实秘密。允许字段可见且canary不可见两项同时断言；不能以完全不记录任何信息通过安全测试。

各项目运行受影响单测、race/vet及既有必要集成/生成/前端检查。依赖真实DB的测试被Skip必须明确，必要时创建独立测试实例，不使用现有Lab容器/卷。只做静态扫描不算修复；历史浏览器/OIDC验收不替代本轮。

lint可针对新增/修改路径使用errorlint/errcheck/staticcheck或小型定向检查，但应复用项目工具，不新增大规模忽略列表或纯字符串镜像测试。规则需要能检测真实反例；自由文本/语义脱敏依靠针对性测试与审查。

## 5. 本轮项目落实清单

| 项目 | 必保留 | 本轮重点 |
| --- | --- | --- |
| Authova | 安全sqlstore.Error/LogValuer、领域Is、固定Problem、commit unknown、OTP/reuse提交语义、BFF代际fencing | 关键丢链、HTTP/协议/Console一次安全诊断、request关联、受控panic、SDK取消、worker监督及关键cleanup；持久失败phase/code如需增量迁移 |
| Kitarbor | OSS cause、固定版本、Named/SQLite事务与CAS、committed-cleanup | normalize/local apiError/CLI任意body、raw上传下载丢链、OIDC/scan安全诊断、panic、typed revision、ledger失败分类与旧state兼容 |
| DataDock | TargetReadError、精确SQL parser诊断、DDL/DML journal、uncertain不重放、导出完成顺序 | 默认panic泄密、raw metrics绕过投影、任意Kratos信任、关键Open/Query/worker原因、通知吞错、持久安全分类/关联与cleanup |
| ContextHarbor | 隐藏owner存在性、安全未知响应、现有operation状态/error_code、immutable/fencing、不重放 | wrapped公共映射、engine/database/auth保链、requestID与安全panic、worker首因/finish/lease、shutdown/补偿、生成错误schema与客户端保留安全字段 |

范围外：Lab无Go主模块，本轮不改其Python错误协议；NookAmble不在当前四核心项目的改造清单。若之后纳入需另做自己的边界审计，不因目录中有代码就全局替换。

## 6. 版本、分发与例外

本文件是v1.0.0共同源；各仓库本地规范记录版本、来源与摘要，并列自己的公开契约映射/诊断字段/例外。实现报告逐条映射R01—R12、改动、实际检查、Skip/未验证项及提交。

器林提供分发就绪的go-error-handling Skill：描述覆盖Go错误传播、公共投影、诊断、worker/cleanup；正文为短路由与检查，研究/完整规范作按需参考。不得每轮读取完整研究，不默认触发多阶段维护。生成/复制内容记录来源，禁止手工改副本造成多份规范分叉；不自动安装用户全局Skill或向生产registry发布。

项目AGENTS/CONTRIBUTING仅增加短入口和本地适用文件指针；已有内容保留。新增repo入口属于此次用户授权的规范落地，不更改工具权限/模型/个人配置。改变本规范须新版本及明确差异，不能在项目实现中隐式重定义取消、隐私或提交未知语义。
