# Outbox 与 Valkey 本地验收

## 范围与结论

容量阶段已本地提交 `7aea5bc`，没有 push。后续在保留原 App/租户/Agent 授权与主库撤销检查的前提下，完成 outbox App 轮转及 Core/Console 原生 Valkey TLS/ACL。设计、配置、升级与轮换步骤见 [运维说明](OUTBOX-VALKEY-2026-10-18.md)。HTTP API、Contracts 和 UI 未修改。

## 先完成的两库一小时容量

两次实际独立、串行运行，每库背景 12,000,000 用户，24 个分散活跃用户/租户，两个真实 Core 进程，正常 TTL 120 秒的 Agent 和实际 HTTP refresh。开放环 5100 目标 QPS、256 workers、显式发生器队列 16384；包含计划排队和最终排空时间。

| 后端 | 成功内省 | 整段成功 QPS | 最低分钟成功 QPS | HTTP/刷新/错配/丢弃 | 真实刷新次数 |
| --- | ---: | ---: | ---: | --- | ---: |
| PostgreSQL | 18,360,000 | 5099.9992 | 5099.7658 | 全部 0 | 2856 |
| MySQL | 18,360,000 | 5099.9991 | 5099.7729 | 全部 0 | 2856 |

每个实际 60 秒窗口均 >5000；撤销跨实例生效。PG P99 4ms/最大1712ms，MySQL P99 3ms/最大700ms；较大的发生器队列实际使用，不隐藏尾延迟。历史失败和工具启动失败均保留，详见 [容量整改](CAPACITY-REMEDIATION-2026-10-17.md)。该证据属于本轮功能修改前的固定二进制、开发 HTTP/SQL/Valkey 同网络画像，不等于下面 mTLS 新画像或目标生产容量。

## Outbox 实际门禁

- 真实 PG/MySQL：旧 backlog 功能夹具不阻塞 quiet App；新 Store 进程保留轮转；并发领取不重复，已锁 App 被 SKIP LOCKED；单 worker 在途预算与多 worker 扩容语义。
- 原子入队/调度唤醒，跨 App 入队拒绝；密文不可解、payload App 或 ID 不符时隔离故障 App，保留 attempts/密文，健康 App 继续。
- 中途 SQL 失败后无部分租约发布、attempts 回滚；未来时间、backoff、租约过期重领、旧 attempt 拒绝、当前确认及终态不复活。
- 两库均运行真实 SQL queue + Worker：两个 slow App 投递被阻塞时，新 quiet App 能投递并确认，slow App 只占两槽；取消时排空在途 goroutine。
- 真实旧 v1 全迁移及 v6→v7 pending App 回填；v6 的原消息各列、密文、attempts、完成/重试状态精确保持。dirty、checksum、运行权限和身份表 RLS 拒绝门禁保留。

## Valkey 与容器实际门禁

新建测试专用 Valkey 9.1.2、仅 TLS、default user off、独立 Core/Console 账号与键前缀；未修改既有演示 Valkey。

- 两角色正常 mTLS/命名认证；错误 CA、系统根不信任私有 CA、错误 SAN/密码、缺失/不受信客户端证书、默认用户及明文连接全部拒绝，没有 fallback。
- 跨角色键空间、Lua 内跨键/命令访问、FLUSHDB、CONFIG、ACL 拒绝。Core 的两个 limiter 实例并发 100 次真实调用只放行 20 次，其他 App 额度独立。
- 两仓 race；BFF 会话、CSRF、共享刷新及 generation fencing 使用真实 mTLS Valkey。BFF 单元集的 Core 上游为受控 HTTPS fixture，不将它称为全供应商或全浏览器验收。
- 真正停止、重启该 Valkey：已连接 Core limiter 的失败返回 false+error，恢复后可重连；BFF 停机时 503 且不转发，丢失缓存后旧会话 401、重新创建已认证会话后恢复 200。应用没有删除 ACL 或降级限流绕过错误。
- 实际从两个仓库 Dockerfile 构建镜像；production 模式、UID10001、只读根文件系统、移除 capabilities，使用 mTLS 与各自账号。Core readiness、管理未授权拒绝、BFF 无会话及 CSRF 拒绝通过；检查 BFF env 不含 Core DB/SMTP/SMS/master key。生产 Compose 用分开的 Core/Console env 校验通过。
- 本地 OCI 夹具文件处于 0700 父目录，仅精确文件以只读挂载、0444 供不同容器 UID 读取；正式部署按手册设 owner10001/0400。入口 HTTP 与本地 SQL 仍是测试拓扑，不宣称验证了它们的 TLS。

TLS runner 的早期失败保留在私有目录：一项 Lua 拒绝错误类型的测试断言过严、Docker 随机端口重启变化、BFF mock 缺失 JSON Content-Type。修复的是夹具与断言，没有放宽 ACL 或生产 JSON 校验。

## 新版本 mTLS 双库容量回归

使用冻结的新版本，PG 与 MySQL 串行独立运行各 300 秒，明确开启 mTLS/命名 ACL。源码（Go、嵌入 SQL、go.mod/go.sum）、两次实际 server/generator 二进制 SHA256 全部一致；现存源码与已测试快照再次核对一致。每库仍为 1200 万背景用户、24 个活跃样本、两个真实 Core 进程，256 workers、发生器队列 16384。

| 后端 | 成功内省 | 整段 QPS | 最低分钟 QPS | P99 / 最大延迟 | 真实刷新 | 错误/错配/丢弃 |
| --- | ---: | ---: | ---: | --- | ---: | --- |
| PostgreSQL | 1,530,000 | 5099.9890 | 5099.9494 | 3 / 211ms | 216 | 全部 0 |
| MySQL | 1,530,000 | 5099.9901 | 5099.8900 | 3 / 73ms | 216 | 全部 0 |

两库均实际验证跨实例撤销。每个分钟窗口 >5000，未停测/丢任务，已计入排队和排空时间；PG/MySQL 最大排队分别 312/61。`profile_passed=true`，`sustained_5000_qps_passed=false` 是预期：**此处只跑五分钟，不能替代新 mTLS 画像的一小时 gate**。前述一小时结论仍属于修改前的独立固定画像。

全量 Core 双库 race 345 叶/125 顶层通过，12 个 opt-in 跳过保留；后加的真实 SQL+worker 与迁移精确断言由最终源码专项再验证（4 叶通过），不伪装成最早全量编译已包含它们。TLS 专项覆盖其 opt-in 测试；BFF TLS 全集 46 叶通过，单独真实停机恢复门禁通过。Core/Contracts 组合、两仓 vet/gofmt、Console 生成契约检查通过。

最终 runner 的成功路径再次实跑；另外注入 Docker start 失败，确认失败退出且清除已知归属的半启动容器。原始 fixture 失败原因与日志摘要一并保留。

## 机器证据与复跑

机器摘要、源代码/二进制 hash、逐分钟成功吞吐和 gate 名称在 `evidence/outbox-valkey-2026-10-18/`。仅发布白名单字段及原始日志 SHA256；密钥、env、数据库凭据、PKI 私钥和原始 fixture 不发布。

```bash
python3 scripts/local-quality.py --evidence .dev/<new-quality-run>
python3 scripts/valkey-tls-gates.py --evidence .dev/<new-tls-run> --keep-fixture
# 同时保留该 owned TLS fixture，完成下列专项后按 summary 精确删除自有容器。
python3 scripts/container-smoke.py --agent-directory .dev/<owned-agent-run> --valkey-fixture .dev/<new-tls-run> --evidence .dev/<smoke>.json
python3 scripts/agent-capacity-linux.py --directory .dev/<owned-agent-run> --output .dev/<new-pg-run> --driver postgres --duration 5m --queue-capacity 16384 --valkey-fixture .dev/<new-tls-run>
# PG 结束后，再用 --driver mysql 和新的 --output 独立运行。
```

## 发布边界

代码和测试本地完成，不 push、不运行远端 CI、不发布。schema v7 必须在维护窗口停止旧 writer/worker 后迁移；不支持混合写入版本。开发 Demo 未切换数据库或重置数据。

这里验证本地可运行行为及指定画像；不外推为千万同时活跃、密码/签发 5000 QPS、跨主机 HA/灾备、外部入口/SQL TLS、Valkey Cluster/Sentinel 故障转移或独立安全审计。既有真实供应商和浏览器证据保持各自历史范围。
