# Console 重构验收矩阵

更新：2026-10-15。对应工作区源码快照，未 push，未对外发布稳定版本。完整报告和脱敏证据在相邻 Core 的 `docs/implementation/CONSOLE-REDESIGN-2026-10-15.md`。

## 执行归属

Kimi Code CLI 原生 `kimi-code/k3`、max effort、thinking 开启，完成首轮主体 UI/模块重构；原生回执核验 107 次请求。第二轮安全复审因供应商 5 小时额度耗尽中断，不能称该轮 Kimi 完成。Codex 完成剩余前端安全修复、BFF/Contracts/Core 配套改造和统一验收。没有切换到其他供应商或购买额度。

## 门禁与证据类型

- 前端：26 个测试文件、170 项行为断言；TypeScript、oxlint、Prettier、构建。组件测试不是线上供应商认证。
- Go BFF：vet、race，配置了真实 Valkey；未配置共享会话依赖时的 skip 不算通过。
- Contracts：OpenAPI lint、生成检查、Go vet/race/format；真实 HTTP 连接丢失回归证明写请求不会被 Transport 隐式重放。
- Core：domain/application/transport race 回归；空 Client 数组真实 JSON 修复。
- 浏览器：已安装真实 Chrome，实际 BFF/Core/PG/Valkey/SMTP/SMS 沙箱。成功链路不用 stubFetch。
- 最终 `npx playwright test` 一次全量 **5/5 通过（2.4 分钟，exit 0）**，包括新 Console 与既有手机/双 App/OIDC 流程；之前失败记录仍保留。
- 历史负载、OCI、PG/MySQL conformance 属于之前的源码快照，不被本次 UI 验收自动重新认证。

以下 **29 个选定场景** 是本轮检查分母；每行明确证据层级，不能将其转换为全产品生产覆盖率。最终命令结果、失败保留与截图以 Core 报告为准。

| ID | 场景 | 主要证据层级 |
| --- | --- | --- |
| 01 | 平台 owner 与新 auditor 的密码/TOTP 登录 | Chrome/BFF/Core；登录/会话组件测试 |
| 02 | 从正式界面创建独立 App | Chrome/BFF/Core；App 创建幂等测试 |
| 03 | 概览读取真实配置、区分业务 Client 与系统 Client | Chrome；实际 HTTP 读取；无假统计代码审查 |
| 04 | App 名称修改、权威读回并恢复 | Chrome/BFF/Core；配置版本行为测试 |
| 05 | 机密 Client 创建与一次性 secret 确认 | Chrome；幂等/窗口组件测试 |
| 06 | Client 编辑与禁用 | Chrome/BFF/Core；失败核对保持冻结测试 |
| 07 | 系统 Client 不提供编辑入口 | Chrome；能力/系统保护组件测试；Core 既有保护规则 |
| 08 | OIDC/GitHub/微信/QQ 配置创建并禁用 | Chrome；假供应商凭据 fixture，非真实供应商登录 |
| 09 | GitHub 连接停启用、secret 轮换、版本号显示、响应无 secret | 既有 Chrome 用例适配新界面；连接组件测试 |
| 10 | 审计动作、详情与原因可查看 | Chrome；审计组件测试 |
| 11 | 完整邮箱精确搜索当前 App 新用户 | Chrome/BFF/Core；无全量客户端筛选 |
| 12 | 设置/撤销 App 固定管理员角色 | Chrome/BFF/Core；能力矩阵与冲突测试 |
| 13 | 撤销用户会话 | Chrome/BFF/Core；ReasonDialog 状态机 |
| 14 | 禁用测试用户并读取已禁用状态 | Chrome/BFF/Core |
| 15 | owner 创建新平台账号、确认一次性 TOTP、禁用 fixture | Chrome/BFF/Core；平台账号组件测试 |
| 16 | auditor 隐藏写入口，持合法会话/CSRF 的真实写请求返回 403 | Chrome 真实 BFF 拒绝；能力矩阵测试 |
| 17 | 切换 App 不残留上一 App 标题与数据 | Chrome；AppLayout/query generation 竞态测试 |
| 18 | 375px 概览/表格不使整页横向溢出，抽屉/Esc | Chrome 截图、宽度断言；模态测试 |
| 19 | light/dark/system 主题及弱文本统一 tokens | Chrome 亮/暗截图；代码审查；非 WCAG 全站认证 |
| 20 | reduced-motion 时数值时长 ≤0.01ms | Chrome computed style，兼容科学计数法 |
| 21 | 浏览器无管理 token、可读会话 Cookie、秘密持久化 | Chrome storage/cookie 断言；API/BFF 代码边界 |
| 22 | 秘密面板不因 Back/遮罩/Esc 消失，显式确认关闭 | Chrome Back；组件与路由/beforeUnload 测试 |
| 23 | 焦点恢复、StrictMode、顶层模态、hidden/disabled 项过滤 | Dialog 行为测试；Chrome 抽屉键盘检查 |
| 24 | query key 切换、卸载、刷新、分页互斥和旧响应丢弃 | Hooks/AppLayout 组件故障注入测试 |
| 25 | 双提交、409/未知结果、核对失败不解冻、重新编辑清旧原因/秘密 | 表单/ReasonDialog/幂等/角色行为测试；非全部异常浏览器认证 |
| 26 | CSRF、安全响应头、JSON/截断/HTML/重定向分类、代理路径限制 | Go httptest + 既有 TLS/会话测试 |
| 27 | 消费写 body 后丢失响应，旧 SDK 2 次提交、修复后 1 次 | 本地真实 HTTP/1 连接故障测试；POST/PATCH/PUT/DELETE |
| 28 | 迟到 401 不覆盖新登录/刷新；被拒绝旧会话确实失效 | 前端认证 generation 测试 + 真实 Valkey CAS 测试 |
| 29 | 空 Client 数组返回 []，SecretDigest 不导出，编辑不崩溃 | Core JSON 测试 + 同一实际浏览器编辑链路 |

## 验收边界

没有声称每个页面的每种状态、全部浏览器/辅助技术、HTTP/2 故障、真实微信/QQ/GitHub/短信厂商联调都已验证。新异常路径有组件故障注入证据；正常链路和权限关键边界有真实浏览器证据。

整页强制关闭仍可丢失内存秘密或未确认提交，beforeUnload 只能提示。供应商配置为本地禁用 fixture。平台 App 管理员 OIDC 登录组件与既有服务端机制保留；本轮没有新增所有外部 IdP 的实机验证。

千万用户和持续 5000QPS 的原平台容量门槛见 Core `PERFORMANCE.md`，本轮 UI 重构不改变历史未达标结论。当前是可运行、可测试的正式候选实现。

## 复现

```bash
# Core 仓库：启动真实本地服务，保持运行
make demo
npx playwright test

# Console 仓库：前端 + BFF，启用共享会话回归
AUTHOVA_TEST_VALKEY_ADDRESS=127.0.0.1:56379 make check

# Contracts 仓库
make check
```

E2E 从 `.dev/bootstrap.json` 私密读取本地 operator 凭据，等待新 TOTP 窗口；禁用自动 trace/video/screenshot，手动截图在秘密关闭之后。测试 App 每轮通过 UI 创建，不依赖硬编码 App UUID；创建的假供应商连接与专用 auditor 在成功链路结束时禁用。
