# Authova Console Web

Authova 平台与应用管理控制台前端。Vite + React 19 + strict TypeScript（含 `noUncheckedIndexedAccess`）。

- 只调用同源 Go BFF `/bff`，接口契约冻结于 `../../docs/implementation/console-api-v1.md`。
- 会话为 HttpOnly Cookie + 内存 `X-CSRF-Token`；管理 token 仅保存在 BFF 的加密服务端会话。
  唯一例外：`authova-theme` 界面主题偏好（非敏感）。
- 路由使用 `createHashRouter`，无需 SPA fallback；当前 BFF/API 仅验收 origin 根路径部署。

## 开发

```bash
npm ci
npm run dev        # 开发服务器，/bff 代理到 http://127.0.0.1:18090
```

## 质量

```bash
npm run typecheck  # tsc -b
npm run lint       # oxlint
npm run format:check # 固定 Prettier 格式；npm run format 可修正
npm test           # vitest run（API/竞态/表单/角色门控/可访问性行为测试）
npm run build      # tsc -b && vite build → dist/，并刷新 web SBOM
```

## 结构

- `src/api/` 冻结契约类型与全部 `/bff` 调用（唯一事实来源）；`generated/` 为生成代码，禁止手改
- `src/app/` 路由表与导航注册表（`navigation.ts`）
- `src/layout/` 工作台外壳（侧栏/移动抽屉/主题）、应用选择器
- `src/features/` 按领域划分：`session`（会话与角色能力矩阵）、`auth`（登录）、`apps`、`clients`、`users`、`connections`、`audit`、`operators`
- `src/shared/` 设计系统组件、`useAsync`/`usePagedList`（查询身份隔离 + 竞态防护）、格式化与幂等工具
- `src/styles/` 分层样式：`tokens.css`（亮/暗设计变量）→ `base.css` → `shell.css` → `components.css`

架构（信息架构/查询与写操作状态机/扩展约定/集成需求）见 `../docs/console-architecture.md`；
视觉与交互约定见 `../docs/console-design-system.md`；验收矩阵见 `../docs/console-redesign-acceptance.md`。
