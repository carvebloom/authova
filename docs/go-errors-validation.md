# 错误处理改造验证 — 2026-11-08

采用组织 **v1.0.1**，SHA256 `10e29e102ee1e7c7fc9d61ffad0e1b9458990f765be3c49491fc7a46399e6d05`。从 `8f4f01e17477deeb1e982eb8202e8988162e4804` 开始，任务分支 `fix/go-errors-v1-authova`。本地交付；未推送、未部署、未重启Lab或原迁移服务、未操作真实Agent设置/系统证书信任、未重复真实OIDC登录。

## 已实际执行

- 独立随机 Compose project 的 PG17/MySQL8.4/Valkey：构建基线v1 binary初始化，再用新binary升级v2，旧outbox行默认unknown/unknown/unknown/0，重复迁移均通过；旧001内容/checksum不变。全包真实双库 `go test -race -count=1 -json ./...` 无失败，729个测试pass事件（包括父测试/子测试，不当作729个独立业务场景）；之后真实Core/SDK/编译CLI/MCP契约组合通过。
- 收尾后再次执行聚焦双库门禁，54个pass事件、零skip；再执行最终 delivery-only 门禁，10个pass事件、零skip：真实空库初始化、v1升级、Ent物理映射、驱动分类、commit_unknown＋取消、持久诊断新Store重开读回、stale ack fencing和 provider_missing/provider_rejected/transport 数据库读回通过。这里的恢复是新连接/新Store重开读回，不冒充生产备份恢复或物理数据库重启演练。
- 最新受影响包race回归、`make check`（gofmt/vet/全包race）、固定staticcheck v0.8.2通过。常规make check不带外部DSN；DB成绩来自上述独立fixture，不把常规Skip作DB通过。生成检查为Ent、Proto/Go HTTP/OpenAPI/TS、Console的pinned SDK镜像与架构边界。
- 新的独立Valkey mTLS/ACL fixture四门禁通过：Core ACL、Console sessions/ACL、Core outage/recovery、Console outage/recovery。只停止/启动该脚本新建的随机命名容器。TLS canary和BFF最新安全错误投影（含 `/bff/me`）均在该fixture覆盖，未使用Lab CA或共享网络。
- Console typecheck、lint、format check、33个测试文件/246测试、production build通过。生成SDK从源及更新pin重现；依赖未升级。构建后SBOM只有serial/timestamp变化，已排除该无关变动。
- 可执行反例：多层Is/As、Join两序/嵌套/类型nil/循环、主失败＋cleanup取消、主取消＋cleanup实错、已提交＋cleanup失败；实际storageError commit wrapper，并通过真实database/sql和Store.transaction注入commit取消/超时/Join失败，均保留outcome_unknown且回调/提交各一次；伪造Kratos/gRPC/同类型storage字段/OAuth/BFF消息；真实OIDC库WriteError的parent/request/日志与公共投影；真实Kratos raw handler和Console panic/partial-response/ErrAbortHandler；MCP callback panic和编译CLI stdout/stderr/exit1；SDK截断body及Login返回error保header ID、BFF本地/上游关联；真实SMTP DATA已接受后QUIT断连仍标accepted并按既有至少一次策略retry；Worker ack主次、panic监督、持久类别和严格fencing；解锁失败强制丢弃物理连接。

安全断言同时要求合成SQL/DSN/token/口令/body标记不出现在HTTP/log/stderr/trace/持久分类，并有operation/phase、关联、原因类别/类型及安全来源。没有用“什么都不记”满足canary检查。CLI未知参数不再由flag默认错误输出回显，help/exit契约保留。

## Skip、失败历史与边界

完整双库执行中出现的4项TLS相关skip，已在独立TLS门禁中实际补跑。其余Skip为专用capacity/soak/恢复快照/浏览器夹具，完整名单见[机器报告](go-errors-validation.json)。本轮不宣称完成这些高成本或需独立快照输入的门禁；没有gRPC listener，不编造gRPC端到端成绩，伪造status安全映射有单测。

失败历史保留：第一轮旧库行检查错误地按空白拆分psql默认管道输出（断言失败，该轮不列为通过），已修；下一轮因fixture数据库名不满足既有offline-recovery安全guard、PG新增列Ent类型未显式长度、维护测试恢复版本写死1而失败。没有放宽guard或改v1历史，修为随机受控数据库名/显式双方言类型/按当前schemaVersion恢复测试状态后重新跑通过。中间编译、单测、lint问题均修复后重跑，不列为成功。历史失败日志不是最终成绩。

新的最终fixture使用project-scoped命名数据库卷、cache tmpfs，清理时验证项目标签和前缀后只删除自己的卷，不用down -v/prune。较早轮次未记录镜像隐式匿名卷归属，未按时间猜测删除任何可能属于别人的卷；没有宣称自动清除了无法验证归属的匿名卷。旧数据卷/服务一律不动。

不可承诺：远端/驱动未采集的原始栈；完整浏览器/外部身份厂商/生产压测；最新v2在真实Lab/生产的部署和生产数据库TLS/HA；所有长尾demo/benchmark都统一了错误方式。局部source快照与终止边界recorded_at分开标记。用户输入校验文案与协议state回传属有意公共详情；任何cause的Error()仍不是安全日志接口。

兼容说明：Problem/标准OAuth code、HTTP状态、CLI输出数据与退出码保持；不新增499。人类error_description、未知CLI错误和不可信BFF消息做安全规范化，调用方应使用机器码。公开SDK的context cause可检查，驱动/网络具体类型不承诺长期兼容。v2需要显式迁移；旧binary不直接降级启动v2。原FinishMessage兼容no-op接口保留，正式Worker使用strict诊断接口。至少一次重投的次数/过期/App预算/fencing仍有效，不宣称恰好一次。

## 证据位置

Git忽略的 `.migration/go-errors/` 为完整双库；`go-errors-focused/` 为聚焦双库；`go-errors-delivery/` 为最终持久化；`go-errors-final/` 为最终静态/生成/单测记录；`go-errors-v1-tls-final/` 为独立TLS四门禁。失败轮次在 `go-errors-attempt-2/`。真实运行秘密只留fixture临时目录或受限证据目录，不进本报告。机器报告仅含结果与日志摘要，不含DSN/密码/原始数据。
