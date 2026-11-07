# Core 能力缺口管理 UI 验收（core-gaps-ui）

更新：2026-10-15。范围：六个已批准 Core 能力缺口的管理端 UI（Console `web/src`），契约以 `../../contracts/openapi/v1/management.yaml` 与 `web/src/api/generated/management.ts` 为准（生成文件由协调方维护，本阶段未手改）。

## 证据层级声明

- 本阶段证据为 **组件/单元行为测试 + TypeScript/oxlint/Prettier/构建门禁**：29 个测试文件、195 项断言全绿。
- **本阶段未做浏览器验收**：未启动真实 BFF/Core/浏览器链路，不断言截图级结论。Core/Contracts/BFF 由协调方并行实现，联调验收另行登记。

## 缺口映射

| # | 缺口 | UI 落点 | 关键语义 |
| --- | --- | --- | --- |
| 1 | 登录策略读写 | 配置页「登录策略」版块（`LoginPolicySection`） | 完整提交六开关 + `config_version` + `reason`；OTP 开关仅为 OTP 登录，不代表通道就绪或联系方式已验证；注册关闭即拒绝一切新用户；`external_registration` 在其上额外约束第三方首登创建；部署就绪状态独立只读展示；策略仅约束终端用户，系统管理客户端密码+MFA 不受限；写权限仅平台 owner/admin 或 App owner（`canManageLoginPolicy`）；409/未知冻结并权威读回 |
| 2 | 恢复已禁用用户 | 用户详情「恢复用户」 | 仅 `disabled` 状态可见入口；`deleted` 终态无入口；确认框展示并携带当前 `session_epoch`；成功后读回；旧会话保持撤销；409/未知冻结读回 |
| 3 | 机密客户端密钥轮换 | 客户端列表「轮换密钥 / 撤销旧密钥」 | 打开对话框即 GET 权威读回版本；`grace_seconds` 0–86400，0 有立即切换凭证警示；新密钥仅一次性 SecretPanel（复制+显式确认、路由/刷新保护、不进存储）；无幂等自动重放；未知结果冻结，读回版本上升则明确提示密钥不可找回；宽限期内轮换被阻止并引导撤销/等待；撤销走 `revoke-previous`；系统客户端与公开客户端无入口 |
| 4 | 签名密钥列表与紧急吊销 | 概览「签名密钥」版块 | 常规轮换文案修正为「旧验证密钥保留 24 小时」，不再宣称立即失效；列表仅元数据（kid/时间/状态徽标：当前签名/验签保留至/已退役/已吊销）；紧急吊销立即从 JWKS 移除、吊销当前密钥时提示生成继任者；外部离线 JWT 缓存须自行刷新/拒绝旧 kid、不承诺立即生效；携带 GET /keys 的 `config_version`，409/未知经 GET /keys 核对；写权限 `canRotateKeys`（平台 owner/admin 或 App owner） |
| 5 | 终端用户 MFA 生命周期 | 不建管理端绕过 | 属 Core 终端用户账号 UI，由协调方实现；本阶段未新增任何管理员重置因子端点或入口 |
| 6 | 结构化认证失败安全事件 | 不建管理端 API | 审计页底部注明边界：该事件由 Core 有界 JSON 日志（含丢弃计数）输出，不并入操作审计视图 |

## 行为测试清单（新增/更新）

- `features/apps/LoginPolicySection.test.tsx`：策略更新完整提交、原因必填、OTP 通道未配置内联警示、部署就绪独立展示、409 冻结→读回→新版本重提交、未知冻结读回、读失败重试、只读无写入口。
- `features/apps/AppSettingsTab.test.tsx`：既有名称/MFA 用例适配 auth-config 读取桩；只读角色用例改为「无任何写请求 + 策略只读」。
- `features/apps/OverviewKeys.test.tsx`：密钥列表与状态徽标、常规轮换文案修正、紧急吊销确认+原因+版本+继任提示+读回、吊销 409 冻结并经 GET /keys 核对、auditor 只读。
- `features/clients/ClientSecretRotation.test.tsx`：轮换成功一次性密钥（含 grace=0 警示、确认后清除、不进 Web 存储）、未知结果冻结+版本漂移提示+不自动重试、409 冻结+新版本重提交、宽限期阻止轮换+撤销旧密钥携带版本与原因、auditor 与系统客户端无入口。
- `features/clients/ClientSafety.test.tsx` / `ClientsTab.test.tsx` / `AppScoped.test.tsx`：Client 夹具补 `previous_secret_expires_at`。
- `features/users/UserDetailPage.test.tsx`：恢复用户（纪元展示/提交体/成功读回）、409 冻结+读回后以新纪元重提交、deleted 终态无入口、auditor 无入口。
- `api/endpoints.test.ts`：enable、authentication-config PATCH、secret rotate/revoke-previous、keys GET/revoke 的路径、方法与请求体。
- `features/session/capabilities.test.ts`：`canManageLoginPolicy` 矩阵（平台 owner/admin、App owner 可写；App admin/auditor/未知角色只读）。
- `shared/utils/format.test.ts`：`formatUnixSeconds`（Unix 秒；0/缺省占位）。

## 门禁

```bash
cd web
npx tsc -b          # 通过
npx oxlint          # 0 警告/错误
npx prettier --check src   # 通过
npx vitest run      # 29 文件 / 195 断言全绿
npm run build       # 通过（含 sbom-web）
```

## 边界与待办

- 未做浏览器/真实后端验收；移动端 375px、键盘/读屏、暗色截图属下一阶段。
- `docs/console-architecture.md` 的信息架构与能力矩阵表未在本阶段更新（不在本任务可写范围），建议协调方统一登记：登录策略写（owner 级）、密钥吊销、用户恢复三个新入口。
- 新增写操作的审计动作中文词典（`features/audit/auditLabels.ts`）未扩充；未知 action 按既有约定原样展示。

## 协调方复审与最终集成

本阶段 Kimi 的 195 项行为测试记录保留。协调方补充密钥表单读取失败禁止提交、空宽限期校验及异常成功响应分类，并加了关键确认字段保护；最终 30 文件/205 项通过，TS/lint/Prettier/build 和 Go/真实 Valkey race 通过。StrictMode 初始读取通过，未发现该项缺陷。

架构能力矩阵和审计动作中文词典已同步。真实管理策略、用户恢复、Client secret 重叠及撤销、常规 key 轮换/应急吊销、375px/暗色和用户 MFA 操作已由协调方验收；范围、失败修复记录和原始证据见相邻 Core 的 `docs/implementation/SECURITY-LIFECYCLE-ACCEPTANCE-2026-10-15.md`。此段更新上述 Kimi 阶段的待办状态，不把组件故障注入当作真实代理故障或供应商互操作。
