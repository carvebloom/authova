# Console 完整重构与本地验收

日期：2026-10-15。三个仓库均为本地变更，未 commit/push，Apache-2.0。Console 是正式产品实现，当前整个平台仍为 0.1.0-rc.1 候选版本。

## 1. 实施归属

按用户要求，Kimi K3 max 主导设计与前端主体重构。Kimi Code CLI 0.42.0，原生模型 alias `kimi-code/k3`，configured effort=max、thinking 开启，首轮原生请求元数据核验 107 次调用。该核验说明请求配置，不推断供应商内部推理。脱敏摘要在 `evidence/console-redesign-2026-10-15/kimi-authorship.json`。

第二轮复审收到供应商 `403 You've reached your 5-hour usage limit`，回执未确认完成。已向用户说明；没有购买额度或切换供应商。Codex 按协助范围完成剩余交互与安全修复、真实浏览器、BFF/SDK/Core 改造和文档。不能把后半段代码归为 Kimi 原生交付。

## 2. 重构范围

- 原 `pages/components/hooks/session/utils/styles.css` 收敛为 `app/api/layout/features/shared/styles`，删除并行旧实现。领域表单就近维护，路由与导航集中注册，契约生成文件只读。
- 亮/暗/系统主题、SVG 图标、清晰平台/App 上下文、任务式接入概览、技术元数据折叠、表格滚动、375px 导航抽屉、键盘焦点与减少动效。
- 覆盖平台/App 登录、应用、配置、Client、用户/会话/固定角色、OIDC/GitHub/微信/QQ 连接、审计、平台运营账号。Email/SMS 状态与回调移至概览；供应商配置仍为真实 API，界面没有假统计。
- 统一字段/错误/空态/模态组件；一次性密钥只能显式确认关闭，Back/链接/刷新有离开保护。
- 默认 14px 正文，弱文本调整亮/暗对比，移动按钮/输入最小 40px；连接列表保持标识完整，具备可聚焦的横向滚动 region。加入固定 Prettier 本地与 CI 格式门禁；运行时没有新增 UI 依赖，现有 React/Router/Vite 继续使用，远端 CI 尚未运行。

长期维护入口：

- [Console 架构](../../../authova-console/docs/console-architecture.md)
- [设计系统](../../../authova-console/docs/console-design-system.md)
- [29 场景验收矩阵](../../../authova-console/docs/console-redesign-acceptance.md)
- [BFF 安全](../../../authova-console/docs/console-bff-security.md)

## 3. 安全与后端修复

| 实际发现 | 修复 | 证据 |
| --- | --- | --- |
| 带幂等头的可重建 Go request body，在上游消费请求后断连时可能被 Transport 自动重放 | Contracts SDK 禁止非 GET/HEAD 的隐式 body 重放，重新生成 Console 绑定 | HTTP/1 长连接实际复现：旧 POST/PATCH/PUT/DELETE 各 2 次提交，修复后各 1 次且返回错误 |
| BFF 接受上游不完整/HTML 等结果可能误判写失败 | 受限路径/方法、上限、JSON/204 完整性；读失败 dependency_unavailable，写失败 outcome_unknown | Go httptest，Core 明确 4xx 保留 |
| 近期 MFA 写入 401 后，页面刷新可能恢复同一不可写 BFF 会话 | token/CSRF/RefreshPending generation + 密文 CAS 删除；迟到拒绝不覆盖新登录/刷新，不用 Set-Cookie 清新 Cookie | 真实 Valkey race 回归 |
| 旧前端请求/响应可能覆盖新 App 或新登录 | query 与认证 generation、卸载取消、同步分页/提交锁 | Hooks/会话/API 行为测试 |
| 409/未知结果可能被关闭、重开、导航或失败核对绕过 | 独立冻结状态、权威读回、清旧原因/秘密、原键窗口提交时复验、路由级保护 | 表单/ReasonDialog/Protection 测试；秘密 Back 真实 Chrome |
| 平台 admin 前端显示了 Core 仅 owner 可执行的 App 管理员入口 | 对齐能力矩阵，不放宽后端权限 | 能力与用户页测试；新 auditor 的真实写请求 403 |
| 实际 Client GET 返回空 introspection audiences 为 null，编辑崩溃 | Core Client JSON 统一空数组为 []，SecretDigest 继续不导出 | Core domain 回归 + 同一浏览器 Client 编辑 |
| 小屏已加载表格的隐藏表头产生整页横向溢出 | 滚动容器建立定位上下文，辅助文字被正确约束，保留容器内横滚 | 真实 Chrome 375px 失败定位与复验 |

统一响应头包括 no-store、严格同源 CSP、no-referrer、CORP、Permissions-Policy 与 production HSTS；本地仍为 loopback development HTTP。

## 4. 验收结果

- 前端 26 文件/170 测试；TypeScript、oxlint、Prettier、build 通过。生产 bundle 约 128KiB gzip，准确构建输出随证据保存。
- Console Go vet 与启用真实 Valkey 的完整 race 测试通过；Core domain/application/transport race 通过；Contracts 正式/历史 OpenAPI lint、vet、race、format 通过。
- 真实 Chrome 成功链路：创建 App、名称保存/恢复、机密 Client 创建/secret 确认/Back 保护/禁用、系统 Client 入口保护、四类连接创建/禁用、审计、App 切换、亮/暗/375px/抽屉/Esc/reduced-motion、存储无管理 token。
- 平台 owner 创建 auditor 并传达一次性 TOTP；auditor 登录只读，持合法会话和 CSRF 的真实 BFF 写返回 403；成功验收 fixture 随后禁用。
- 既有真实业务回归覆盖手机注册/换绑/验证码登录、双 App 同邮箱独立注册、SMTP、PKCE、refresh/内省、本地 OIDC；并由 Console 精确查找新用户、设置/撤销固定角色、撤销会话、禁用用户。

最终 `npx playwright test` exit 0，**5/5 端到端流程通过，耗时 2.4 分钟**。原始输出为 `evidence/console-redesign-2026-10-15/browser-full.log`；这是本轮最后源码构建上的一次全量成功记录。

### 失败保留

最初 reduced-motion 正则不接受科学计数法；改为数值判断。Client 表单缺少必填受众和默认 scopes/grants 后失败，补足明确提示与正确默认值；真实 Client 空数组问题已修 Core。

首次全量轮次 2/5 通过：auditor 登录停留提交态超过原 5 秒断言，手机 flow 返回真实 dependency_unavailable 503，连接编辑遗漏当前版本展示。版本展示恢复；等待阈值设为 15 秒，保留失败日志。依赖恢复复验中 auditor 和手机通过，原 Console 流程发现新的小屏隐藏表头溢出，再修复并全量复验。没有通过自动重试写操作掩盖这些失败。

两轮 BFF 检查出现共享依赖超时，最后一次为 Valkey 初始化连接的 5 秒 deadline；原失败日志均保留，随后启用真实 Valkey 的完整 make check/race 通过。Valkey 当时检查仅 9 个连接、无 blocked/paused，并未证实具体宿主机根因；因此只记录依赖超时事实，不把资源竞争推测作为失败转通过的依据。生产超时配置没有为测试放宽。

## 5. 证据与复现

白名单证据包在 `evidence/console-redesign-2026-10-15/`：命令日志、失败记录、关闭秘密后的截图、Kimi 脱敏元数据、源码与证据 SHA-256 manifest。连接列表的小屏截图采用实际 375×812 视口，避免长页截图拼接/粘性元素重复造成误读；DOM 中 shell/topbar 各一个并经 Chrome 检查。排除 `.dev`、bootstrap、session storageState、供应商秘密和 Kimi 完整对话日志。

```bash
# 在 authova 启动完整本地正式服务与 Demo
make demo
npx playwright test

# 在 authova-console
AUTHOVA_TEST_VALKEY_ADDRESS=127.0.0.1:56379 make check

# 在 authova-contracts
make check
```

Console 地址：`http://127.0.0.1:18090/`，Core `18080`、RP `18088`、SMS 沙箱 `18089`。Demo 数据库与容量库分开。E2E 动态通过 UI 创建 App；秘密从本地私密 bootstrap 读取，不把 UUID/密钥硬编码进公开测试。

## 6. 未扩大验收结论

本轮是 Console 重构与相关安全集成的本地验收。未进行远端 CI、OCI 镜像重新签发、所有页面异常状态的真实浏览器组合、全部辅助技术/WCAG 认证、多机 HA 或第三方供应商实机验收。微信/QQ/GitHub 配置用禁用 fixture；手机号用本地 SMS 沙箱，本地 OIDC 不等于所有 IdP。

beforeUnload 只能提示；强制刷新/关闭仍可丢失内存秘密或未确认记录，须人工核对服务端。本次 UI 不会将组织/业务 RBAC 纳入首版。

千万用户的两库历史 COUNT/恢复证据保留；持续 5000QPS 容量 gate 仍未通过，见 [PERFORMANCE.md](PERFORMANCE.md) 和 [当前整体状态](STATUS.md)。当前不称整个平台已完成生产认证。
