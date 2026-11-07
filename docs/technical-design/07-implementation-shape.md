# 7. Go 工程结构、状态机和适配边界

此章给出可指导正式编码的结构提案，不表示目录或接口已存在。首版保持一个 Go module、一个核心 API 二进制（可按运行角色开启 worker）、一个数据库迁移序列；按领域而非每张表建包。避免一开始拆成多个需分布式事务的服务。

## 7.1 建议的仓库结构

```text
authova/
  cmd/authova/              # API/worker 同一版本的运行入口、配置校验
  internal/platform/        # App、平台 operator、引导与恢复
  internal/client/          # OAuth Client 与注册配置
  internal/identity/        # User、标识、凭据、外部绑定
  internal/authn/           # 密码/OTP/MFA/联邦认证步骤
  internal/session/         # 浏览器会话与设备
  internal/protocol/        # OIDC/OAuth 端点及协议库适配
  internal/audit/           # 事务内审计、outbox 与投递
  internal/crypto/          # key provider、用途分离、轮换
  internal/storage/         # PG/MySQL adapter、事务、生成 SQL 映射
  internal/transport/       # HTTP 中间件、问题响应、限流
  migrations/postgres/      # PG 带版本 SQL、索引与 RLS
  migrations/mysql/         # MySQL 带版本 SQL、索引与权限
  login-ui/                 # 随核心发布的默认页面
  docs/                     # 本方案、ADR、运维与兼容资料
```

包所有权按聚合来定：`identity` 拥有用户标识不变量；`protocol` 调用 `identity/authn/session` 提供的用例，不绕过它们直接写用户表；固定管理权限由 `platform` 和 App 管理用例核验；`audit` 负责事务内写入接口，但事件语义由发起模块定义。跨包共享的 `AppContext` 是已验证的作用域类型，构造函数限定在入口；不要让普通请求任意指定 ID 后就得到它。

领域层只依赖 Repository + Unit of Work；adapter 负责各库 SQL/锁/约束/错误映射。平台访问与 App 访问分别组合；注册、refresh、管理授权变更的领域数据、audit/outbox 必须使用同一事务连接，具体契约见 [存储抽象](12-portable-storage.md)。首版范围见 [第 13 章](13-v1-scope.md)。

## 7.2 HTTP 请求处理顺序

协议与自有 API 使用不同路由组，但共用安全基础件。处理顺序应固定并可测试：

```text
TLS/受信代理校验
 -> request ID、大小/超时限制
 -> 路由识别（标准协议 / 平台管理 / App 管理 / Token 内省）
 -> 解析 issuer 或 app path / Client，但不将路径当权限
 -> 验证调用凭据、audience、App 归属和账号/Client/App 状态
 -> 风险与限流预算
 -> 领域用例和单事务/显式读一致性
 -> 响应格式（标准协议错误或 Problem Details）
 -> 结构化指标、审计、脱敏日志
```

对 authorize 初次浏览器请求尚未登录，先由 issuer 与已注册 Client 建立受限 `AppContext`；不能把“未认证浏览器”误当成已授权 App 管理者。对 token 请求要先认证 Client（如其类型要求）并比对 code/refresh 中的 App。管理 token 验证后再访问 App 资源。对内省，先认证资源 Client 并限定可内省 audience，再检查目标 Token。

## 7.3 模块接口形状（概念，不是公开 Go API）

```go
type AppContext struct {
    ID       AppID
    Issuer   string
    Status   AppStatus
    Version  int64
    // Fields are constructed only after trusted routing/verification.
}

type IdentityRepository interface {
    FindVerifiedIdentifier(ctx context.Context, app AppContext, kind IdentifierKind, digest []byte) (UserID, error)
    GetUser(ctx context.Context, app AppContext, id UserID) (User, error)
}


type KeyProvider interface {
    CurrentSigner(ctx context.Context, app AppContext, purpose KeyPurpose) (Signer, error)
    VerificationKeys(ctx context.Context, app AppContext, purpose KeyPurpose) ([]PublicKey, error)
}
```

实际实现需区分读事务/写事务、调用主体、错误类型、deadline 和审计上下文。`AppContext` 字段不能全公开然后声称“不可伪造”；上例只是调用形状，代码中应通过受限构造和包边界保证。不要用 `context.Context` 携带唯一的 App 信息并在缺失时默认为平台范围。内部接口需要可在单元、集成和契约测试中替换，但生产环境不做任意 Go 插件动态加载。

## 7.4 关键状态机

状态变化写在领域层，数据库有相应 CHECK/唯一约束，所有异常和超时可重试性明确。

| 对象 | 允许的状态/转移 | 拒绝的情况 |
| --- | --- | --- |
| App | `provisioning -> active -> suspended -> active`；`active/suspended -> deleting -> deleted`（删除另议） | 无签名 key 就 active；deleted 后重用 issuer |
| 授权码 | `issued -> consumed` 或 `issued -> expired` | consumed 再兑；跨 Client/App；redirect/PKCE 不符 |
| 标识验证挑战 | `issued -> consumed/locked`；`expires_at` 到时逻辑失效，后续清理 | 超次、重复验证、目的地址更改后复用 |
| Session | `active -> revoked/expired` | revoked 恢复；另一个 App 复用 handle |
| Refresh family | `active -> revoked/expired`；代次 `n -> n+1` 原子轮换 | 旧代次重用而 family 仍 active |
| Provider connection | `draft -> active -> disabled` | 未通过上游配置校验就 active |

App `provisioning/deleting/deleted` 是内部生命周期提案；当前 `authova-contracts` 公共 `App.status` 草案只列 `active/suspended`。在公开契约冻结前，决定是仅在完成 provision 后返回 App，还是公开更多状态；不能让服务实现返回未声明枚举。若内部初始化失败，要能清理或重试，且任何时候都不能签发无效 App token。

## 7.5 事务、错误与重试约定

- Repository 不自行提交跨聚合业务事务；用例层显式传入事务，统一写领域对象、审计和 outbox。外部供应商调用绝不放在持有数据库锁的事务中；先建立带 TTL 的流程，再调用供应商，再依据回执和用户证明推进。
- 错误分为输入错误、未认证、禁止、未找到、冲突、限流、外部依赖不可用、内部状态冲突。领域错误不直接泄露给 HTTP；协议层映射到标准 OAuth 错误，自有 API 映射到 RFC 9457 与稳定 `code`。调用方看到的 `5xx` 不保证写入未发生，应使用幂等键/read-back 恢复。
- Worker 使用 outbox `event_id` 去重、指数退避和死信，邮件/短信供应商调用可能“供应商已接受但本地超时”，需要可查询回执或有界重复发送语义。Webhook 至少一次投递，订阅方必须按 event ID 幂等。
- `Idempotency-Key` 记录调用主体、App/操作、请求摘要和结果引用。并发相同 key 只能有一个进行，返回相同结果或“处理中”；不同请求复用 key 返回冲突。响应保存应屏蔽一次性明文 Secret/token 的泄露风险，敏感结果需要单独重试设计。

## 7.6 测试层次与工程门槛

| 层次 | 价值 | 代表性测试 |
| --- | --- | --- |
| 纯领域单元测试 | 验证状态转换与错误语义 | refresh 代次、固定管理角色范围、标识规范化规则版本 |
| PG/MySQL 存储 conformance | 在真实后端验证同一事务、唯一约束和隔离语义 | 两个 App 同邮箱、复合 FK、并发 code/refresh、各库迁移；PG 另测 RLS |
| 协议/契约测试 | 验证外部互操作 | OIDC Discovery、PKCE、nonce、JWT profile、OpenAPI 响应/错误、SDK 兼容 |
| 安全反例测试 | 阻止边界回归 | App1 token 用于 App2、混淆的 provider 回调、回调 URI 变体、错误 audience |
| 负载/故障测试 | 验证产品容量与恢复 | 1200 万用户、5000 内省 QPS、热点 App、各数据库/Valkey/KMS 故障、滚动升级 |

正式版本发布 gate：契约 lint、迁移测试、集成/安全用例、依赖扫描与许可证清单、实际规模性能报告、备份/恢复与 key 轮换演练。只有与目标版本和数据集绑定的测试结果可作为声明依据。
