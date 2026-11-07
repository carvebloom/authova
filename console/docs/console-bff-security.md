# Console HTTP 与会话边界

本文件约束正式 Console 的 HTTP/BFF 实现，前端信息架构见 `console-architecture.md`。

## 所有权

- `main.go`：部署配置、加密共享会话、登录/当前主体/退出以及请求入口。
- `app_oidc.go`：App 管理员 OIDC + PKCE、nonce/at_hash 与刷新协调。
- `security.go`：页面、静态资源和 BFF 的统一安全响应头。
- `proxy.go`：受限的管理 API 转发、上游响应完整性与结果分类。
- `session_rejection.go`：有效上游 401 的 generation 比较与密文 CAS 失效处理。
- `internal/coreapi/client.go`：从相邻 Contracts 的 `sdk/client.go` 生成；修改源文件并运行 `scripts/generate-bindings.py`，不得手改生成文件。

Console 不持有业务数据库连接。Core 执行当前主体、固定角色、App 数据范围、配置版本以及最后 owner 等规则；隐藏或禁用前端按钮不构成授权。

## 会话与浏览器

管理 token/refresh token 仅在服务端 Valkey 中以 AES-GCM 加密保存。浏览器只得到 HttpOnly 的随机会话 Cookie 和用于同源写请求的内存 CSRF token。生产 Cookie 必须 Secure，生产 Console/Core origin 必须 HTTPS。退出和刷新使用既有共享状态协调，刷新不重放、退出不能复活会话。

所有响应 `Cache-Control: no-store`。CSP 限定同源代码、样式和连接，并拒绝 object/frame/base 注入；不允许 unsafe-inline/unsafe-eval。Referrer-Policy 为 no-referrer，CORP 为 same-origin；禁用本控制台无用途的摄像头、麦克风、地理位置、支付和 USB。生产模式发送 HSTS（不扩大至子域）。本地 development 模式仍使用 loopback HTTP。

## 请求与错误

每个入口生成服务端 `console_` 请求 ID，不信任传入的 X-Request-ID。本地 Problem JSON 与响应头的 request_id 一致。合法 Core 响应保留其 HTTP 状态、JSON body、请求 ID；409/403/401 不能改成成功或吞掉。

管理代理仅接收 `/bff/apps`、`/bff/apps/...`、`/bff/platform/operators` 和其子路径，拒绝相似前缀。允许 GET/POST/PATCH/PUT/DELETE，资源级方法和授权由 Core 校验。请求体上限 64KiB，上游响应上限 4MiB；返回必须为有效 JSON 或 204，拒绝 HTML/重定向/截断 JSON。

- GET 上游不可读：503 dependency_unavailable，界面可明确重新读取。
- 写操作上游不可读：503 outcome_unknown，不能假定失败或直接自动重试。
- Core 的有效拒绝响应：原样保留，按明确错误处理。

## 禁止传输层重放写操作

Go 的 HTTP Transport 可在复用连接丢失响应时，把带 Idempotency-Key 且具有可重建 body 的请求视为可重放。只有部分创建 API 实现幂等，因此添加该 header 不能把所有操作都变成安全重试。

SDK 的非 GET/HEAD body 使用不可重建的 Reader，保留显式键和内容长度，关闭这种不确定结果后的自动重放。调用方收到错误后自行 read-back；只有正式契约支持的幂等操作才可由用户明确使用原键重复。BFF 本身不运行重试循环。

实际回归测试先预热 HTTP 长连接，再由服务器消费 POST/PATCH/PUT/DELETE body 并立即断开，不返回响应；旧代码每次产生 2 次提交，修复后各 1 次并返回错误。该测试证明这条本地 HTTP/1 场景，不将其推广为所有代理、HTTP/2 或供应商链路的完整故障认证。

## 后续扩展

新增功能先在 Core/Contracts 明确 API 和 App 范围，再扩展 BFF route family（若确有必要）和 feature module。不要在 BFF 聚合未经授权的跨 App 数据，或把 secret 读接口添加到 UI。统一安全头和错误分类独立于页面风格，新增路由沿用这些边界。

## 上游拒绝后的会话失效

真实浏览器走查发现：管理写操作要求近期 MFA，长时间只读会话可能仍可通过 GET /me，但首次写返回 401。仅由前端清状态时，页面 reload 又会用 BFF 缓存恢复原会话，形成反复失效的操作体验。

有效 Core 401 会按被拒绝的 token/CSRF/RefreshPending generation 清掉对应 BFF 缓存：先读取与解密当前值，只有与本请求相同的凭据才执行密文 compare-and-delete。若并发刷新已换成新 token 或认领刷新，不删除新状态。不发送清 Cookie 响应，避免迟到的旧请求覆盖另一轮新登录的浏览器 Cookie。实时 Valkey 回归测试覆盖旧凭据失效、新 token 和进行中的刷新认领保留。保持 Core 近期 MFA 策略，不延长或关闭验证要求。

## 实际 JSON 与秘密日志

本轮真实客户端编辑发现 Core 将空 `allowed_introspection_audiences` 返回为 null，违反非空数组契约。修复放在 Core Client JSON 序列化层，所有空数组返回 []，SecretDigest 继续不导出；前端不依赖虚构的 mock 响应掩盖差异。

React 全局错误边界仅展示通用信息，createRoot 错误日志不输出原始对象；BFF 清理错误不输出 token 或密文。E2E 禁用自动 trace/video/screenshot，凭据读取和输入失败诊断脱敏，手动截图在秘密面板关闭后生成。证据打包只收白名单公开截图/日志，排除 storageState、bootstrap、原始供应商凭据与 Kimi 完整日志。

## Valkey 传输与角色隔离（2026-10-18）

生产配置强制 TLS（最低1.2、正常CA/SAN验证）及命名 ACL 用户；支持独立密码文件与可选客户端证书，不提供跳过验证或明文 fallback。Console 角色只允许 `authova:console:*` 的 GET/GETDEL/SET/DEL/EVAL 与握手/只读拓扑子命令；跨 Core 前缀、Lua 越权、管理命令均须拒绝。Core 和 BFF 分别使用 env/secret，只给 BFF 挂载会话密钥、自己的密码/客户端私钥和公共 CA。

实际 TLS/ACL runner 覆盖证书/认证负例、共享会话/刷新/CSRF、真实停止/重启后的 fail-closed 与重连。丢失缓存不能复活旧会话，必须重新认证；本地进程检测不能替代目标 Valkey HA、持久化及灾备验收。轮换需要滚动重启加载新文件；应急停用凭据还需独立运维账号清除既有连接，应用角色没有这类管理权限。
