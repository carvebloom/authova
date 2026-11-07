# 部署包

`compose.dev.yaml` 仅为本地开发 fixture。`compose.production.yaml` 启动 Core、独立 worker 和 Console，使用外部数据库/Valkey/SMTP及已有 TLS 入口。它不自动配置数据库 HA、DNS、证书或云账号。

## 准备

1. 构建两个仓库的 Dockerfile，标签 `authova/core:0.1.0-rc.1` 和 `authova/console:0.1.0-rc.1`。正式环境用自己的 registry digest 固定镜像。当前镜像均为 UID/GID 10001，容器只读、移除 capability。
2. 在仓库之外建立仅管理员可访问的私有目录。分别生成 32 字节二进制 `master.key`、`console.key`，另准备 CA、Core/Console 各自的 Valkey 客户端证书/私钥和不同的密码文件，两个 32 字节密钥文件不能写为 Base64 文本。把文件所有者设为 10001:10001、权限 0400，或使用能按该 UID 注入文件的 secret manager。数据库备份和密钥副本分别保管。
3. 分别复制 `production.env.example` 和 `console.env.example` 至两个私有 env 文件（0600）；Console 文件不得放 Core 的 DB、SMTP、主密钥和 SMS 凭据。Valkey 生产强制验证证书的 TLS 和命名 ACL，模板启用 mTLS；参照 `valkey.conf.example`/`valkey.acl.example` 配置服务端，给两个角色不同密码与最小命令/键空间权限。不可原样使用 ACL hash 占位符。细节及轮换见 [运维说明](../docs/implementation/OUTBOX-VALKEY-2026-10-18.md)。生产不能使用开发示例密码。
4. 配置 TLS 入口：identity 域名转发 Core 8080，console 域名转发 Console 8090；Console 必须能访问 identity 的 HTTPS 地址。默认宿主端口仅绑定 loopback。不要公开 `/metrics`；不要依赖未受信转发头识别用户 IP。
5. 单独创建 migrator 与 runtime 数据库账号。PostgreSQL runtime 不是表 owner/superuser/BYPASSRLS，授 schema usage 与表 DML/default privilege；MySQL runtime 仅 DML。数据库 TLS 配置和迁移备份依部署验证。

## 迁移、引导和运行

先在持有主密钥的受控主机执行已构建二进制 `authova migrate`。只有该进程注入 `AUTHOVA_MIGRATION_DATABASE_URL`，运行容器的 env 文件不得包含 migrator 凭据。迁移结束后使用 runtime DSN 执行 `authova bootstrap < private-bootstrap.json`。JSON 字段 `email/password/totp_secret`，密码至少 15 字符，TOTP 是至少 160 位未填充 Base32。首次成功后删除临时密码载体，并安全备份恢复材料。

```bash
export AUTHOVA_DEPLOY_ENV=/absolute/private/authova.env
export AUTHOVA_CONSOLE_DEPLOY_ENV=/absolute/private/authova-console.env
export AUTHOVA_SECRET_DIR=/absolute/private/keys
docker compose -f deploy/compose.production.yaml config --quiet
docker compose -f deploy/compose.production.yaml up -d
```

读 `/healthz` 只验证 HTTP 进程；`/readyz` 验证数据库连通性，不能代表邮件/Valkey/所有业务依赖正常。监控 worker 错误、outbox 延迟、数据库/Valkey和接口失败率。清理操作、停机升级、撤销窗口、恢复步骤见 `docs/implementation/OPERATIONS.md`。

## 升级/回滚

备份数据库与密钥 → 在维护窗口暂停写入 → migrator 执行迁移 → runtime 检查 clean/schema version → 升级 Core/worker/Console → 验证真实 Code/PKCE/refresh/内省和管理授权 → 恢复流量。当前 schema 8：暂停所有旧 Core/worker/cleanup，升级全部写入者后才恢复流量；schema 6→7 回填 outbox 调度元数据，7→8 增加恢复隔离状态，不支持新旧 writer 混跑。身份恢复须按[恢复隔离步骤](../docs/implementation/IDENTITY-REVIEW-REMEDIATION-2026-10-18.md)执行。变更 schema 不兼容旧运行时；失败时恢复迁移前备份及其兼容二进制，不改版本标记伪装回滚。首次版本的 gob 密文结构是内部格式，后续字段/格式变更必须附迁移及旧密文兼容测试。

此部署包通过 Compose 结构和容器运行验证。客户 TLS/HA/灾备 RPO/RTO、目标画像持续容量、供应商短信与独立安全评审尚无完成证明；本地小时压力与两库千万全量恢复已有逐项证据，详见实施报告。
