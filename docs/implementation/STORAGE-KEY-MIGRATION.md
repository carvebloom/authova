# 存储格式升级与加密密钥轮换

适用于本地候选版本 2026-10-15。支持 PostgreSQL/MySQL 的**维护窗口迁移**；不是在线零停机迁移、KMS/HSM 集成或千万行规模验收。SQL schema 仍为 clean v4，未修改已应用 migration checksum。

## 1. 两种密钥生命周期

| 用途 | 配置与规则 |
| --- | --- |
| 查找/协议根密钥 | `AUTHOVA_MASTER_KEY_FILE`，32 字节原始随机值。保持现值。派生标识、外部 subject、session/token/client secret 摘要以及现有协议加密 key。更换它不属于本工具的能力 |
| 存储加密根密钥 | `AUTHOVA_ENCRYPTION_KEYRING_FILE` 指定 JSON，包含 active ID、至多 16 个 ID→原始 32 字节文件路径，以及可选 legacy_key_file。读旧 key、只写 active key |
| Console 根密钥 | Console 独立管理；本工具不迁移 BFF Valkey 会话 |
| App 签名私钥 | SQL payload 内受存储加密保护；App JWT 签名轮换/应急撤销仍是独立功能 |

原部署一把根密钥同时承担查找和加密。首次过渡必须保留该根密钥作为查找根，即使从加密 keyring 移除了它。**这不等于旧根密钥被密码学擦除，也不能用来处理旧查找根已泄露的所有后果。** `external_identities` 没有可离线重算的原始 subject；查找根泄露/更换仍需另行设计登录迁移或重新绑定和协议/session 撤销。

ID 一经使用不得换成另一把值；启用新密钥使用新 ID。keyring 和密钥文件应由 secret mount 提供、限制文件权限，密钥及数据库备份分开保存。ID 和路径不是 secret，原始 key 是。

```json
{
  "active": "storage_2026_10",
  "keys": {
    "initial": "/run/secrets/authova-old-root",
    "storage_2026_10": "/run/secrets/authova-storage-2026-10"
  },
  "legacy_key_file": "/run/secrets/authova-old-root"
}
```

不配置 keyring 时，软件将已有根作为 `initial` 加密 key，并能读原无 envelope 密文。新写入使用带 ID 的 envelope。因此**旧二进制不能读取所有新写入，禁止混用旧、新 writer**。

## 2. 两层版本和兼容规则

- 外层：`AUTHOVA\0\2` + ID 长度 + ID + nonce + AES-256-GCM ciphertext/tag。App/用途和完整 header 一起进入 AAD；替换用途、ID 或密文都会校验失败。未知 ID/损坏格式失败关闭，不枚举尝试所有 key。
- 原无 header 的 nonce+ciphertext 只通过显式 legacy key 读取。
- 内层：`AUTHOVAP\0\1` + kind 长度/kind + gob。`codec_v1.go` 保存独立、冻结的持久化 DTO，包含凭据和安全版本。领域和 HTTP JSON tag 不再直接决定落盘 schema。
- 旧 gob 经冻结 DTO 读取；新类型字段演进必须审查转换，破坏兼容时增加 codec 版本，不能重生成/改写 v1 来掩盖旧布局。
- 四个固定 legacy golden 样本覆盖 User/Flow/Client/SigningKey 的完整安全事实。另有全 payload 表的真实数据库迁移演练。
- 原 SQL 状态/epoch 列仍是当前撤销事实，loader 继续覆盖 payload 内副本；迁移只更新 payload，不写回旧状态列。
- 旧重置 flow 缺少绑定用户/epoch 时不能继续修改密码，需重新申请验证码。这是有意失败关闭。

## 3. 执行流程

1. 记录候选二进制及源 hash，做数据库和所有相关 key 的备份并验证可恢复。停止 Core、worker、清理任务及所有其他写入者，排空流量。Console 可展示维护状态。
2. 所有后续命令使用新二进制、同一 keyring 和稳定查找根。先保留旧加密 key，启动检查必须仍通过 schema/运行账号检查。无需使用 migrator 权限执行 payload 重加密。
3. 枚举平台及全部 App。平台表：operators、management_sessions、platform_idempotency、audit_events。每个 App：clients、users、sessions、flows、refresh_tokens、access_tokens、challenges、connections、signing_keys、audit_events、outbox、idempotency、oauth_consents。Apps 自身及无 payload 的索引/授权/令牌关系表不重加密。
4. 对每个作用域/表从空 cursor 执行 verify，确认所有现有数据可解密/解码。默认 verify=true，不写入。

```sh
# 环境已由部署 secret/config 注入；命令参数不包含凭据。
bin/authova rewrap --app "$TASK_APP_ID" --table users --limit 100 --verify
# 平台：省略 --app；audit_events 以空 App 执行时仅处理平台审计。
```

5. 完整预检通过后执行同一分页，显式 `--verify=false`。记录 stdout 的扫描/更新数、key_counts 和 next。每页 1–500 行，同一事务、行锁和 payload CAS；停止/进程崩溃不要求从头重写成功页。

```sh
bin/authova rewrap --app "$TASK_APP_ID" --table users --limit 100 --verify=false
bin/authova rewrap --app "$TASK_APP_ID" --table users --limit 100 --verify=false --after '["last-user-id"]'
# oauth_consents 的 cursor 为 [user_id,client_id]；platform_idempotency 为 [operator_id,idem_key]。
```

6. 仅在命令 exit 0 后保存返回 cursor。`scanned=0` 表示该表本次扫描完成。失败或 commit outcome unknown 时不输出成功 cursor；保持原 cursor，用 verify 读回并人工/部署编排确认状态。不能把未知提交当作明确失败自动重试业务写入。
7. 对**所有作用域、所有页**重新 verify，要求 `needs_rewrite=0`、key_counts 仅 active ID；保留汇总及源/二进制 hash。单页、单 App 或抽样不能证明可退休旧 key。
8. 从加密 keyring 删除旧 key 和 legacy_key_file，在旧加密 key 不可用的进程中再次全量 verify；保留稳定查找根。验证通过后统一启动新 Core/worker，并检查登录、刷新、撤销、通知与管理流程。
9. 回退旧二进制必须恢复成套迁移前备份和对应 key，或提供经单独验收的反向迁移；本工具不支持旧二进制直接读新 codec/密文。不删除唯一恢复备份。

## 4. 故障语义与验收边界

- 任一页损坏数据/缺失 key/未知 codec 导致整页回滚，不跳过坏行。工具不打印 payload、SQL 参数或 key。
- verify 会真正认证和解码，单独查看 key ID 不算验证。
- 两库演练使用独立新数据库和 19 条合成 payload，覆盖 17 个作用域/表组合，1 行分页续跑、后续行损坏导致前面更新回滚、移除旧加密 key、标识/外部身份/client secret 索引保持，以及全部 codec 读回。
- 这不是 1000 万行迁移时长、在线并发迁移、全实例密钥分发/备份恢复或旧根泄露处置的验收。正式部署应先在自身脱敏数据规模上测出扫描时间、IO/WAL/锁影响和维护窗口。
