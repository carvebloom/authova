# Authova Console 设计系统

> 视觉与交互的唯一事实来源。目标：Linear 式现代开发者工具的克制工作台——浅色中性表面、精确排版、单一强调色、真实 SVG 图标、细分割线、足够留白；信息密度适中。中文默认，保留必要行业英文术语（Client ID、Issuer、TOTP、scope 等）。

## 1. 设计基调

- **视觉论点**：安静的工具，不是海报。内容即层级：用排版、间距、分割线表达结构，不用卡片拼盘与装饰渐变。
- **一个强调色**：`--accent`（靛蓝）只用于主要动作、选中态、链接与焦点环。语义色（ok/warn/bad）只出现在状态徽标与通知。
- **无广告式 hero、无大面积渐变、无重复 header 堆叠**。品牌标记为简洁几何 SVG（盾形 + 钥匙孔），出现在登录页与侧栏。

## 2. Design Tokens（`styles/tokens.css`）

| 组 | 变量 | 说明 |
|---|---|---|
| 表面 | `--bg` `--surface` `--surface-2` `--surface-3` | 页面/面板/悬浮/凹陷四层中性灰阶 |
| 文本 | `--text` `--text-2` `--text-3` | 主/次/弱三级 |
| 线条 | `--border` `--border-strong` | 细分割线 / 输入框边 |
| 强调 | `--accent` `--accent-hover` `--accent-soft` `--on-accent` | 唯一品牌强调色 |
| 语义 | `--ok/-soft` `--warn/-soft` `--bad/-soft` | 状态与通知 |
| 排版 | `--font-sans` `--font-mono`；字号 12/13/14/16/20/24；行高固定 | 中文系统栈 + 等宽（ID/密钥/URI） |
| 尺度 | 4px 基距 `--space-1..8`；`--radius-s/m/l`；`--shadow-m/l` | 统一间距/圆角/阴影 |
| 动效 | `--dur-fast` 120ms `--dur-med` 180ms；ease-out | 短、克制；见 §6 |

暗色：`[data-theme="dark"]` 覆盖同名 token（对比度不降）；默认跟随 `prefers-color-scheme`，用户可在侧栏切换（light/dark/system，存 `authova-theme`，非敏感）。暗色不牺牲任何范围完整度：同一套组件，仅 token 换值。

## 3. 排版与密度

- 页面标题 20px/600，版块标题 15px/600，正文 14px，辅助 12.5px，表头 12px。
- ID/Issuer/密钥/URI 用 `--font-mono`；超长值 `overflow-wrap:anywhere` 或截断 + `title`，不撑破容器。
- 行高约 44px 的表格密度；内容区最大宽度 1080px，居左对齐（工作台不居中漂浮）。

## 4. 组件契约（`shared/components/`）

| 组件 | 契约要点 |
|---|---|
| `Button` | variant: primary/secondary/danger/ghost；size sm/md；`loading` 置 spinner 且禁用；danger 仅用于真实危险动作 |
| `IconButton` | 图标按钮必有 `aria-label` |
| `TextField/PasswordField/SelectField/TextAreaField` | label htmlFor 关联；`error` 以 `role=alert` 关联 `aria-describedby`；hint 同关联；PasswordField 带「显示/隐藏」切换 |
| `Dialog` | portal；`aria-modal` + 标题 id 关联；焦点陷阱（Tab 循环）；背景 `inert`；滚动锁；Esc/遮罩关闭可由 `dismissible={false}` 禁用（提交中）；关闭后焦点恢复触发元素 |
| `Drawer` | 移动端导航抽屉，同 Dialog 的焦点/Esc 规则 |
| 表格约定 | `<table>` 语义 + 容器内横向滚动（`.table-scroll`）；表头 `scope=col`；行内主要动作放末列；当前没有名为 DataTable 的抽象组件 |
| `StatusBadge` | 点 + 中文标签；未知状态原样展示且 tone=muted |
| `CopyButton` | 复制成功/失败变更按钮文案，`aria-live=polite` 播报 |
| `EmptyState/Loading/ErrorNotice/ForbiddenState` | 按读取或写操作提供适用状态；403 展示无权限；具体场景证据见验收矩阵，不能仅凭组件存在宣称每页每态通过 |
| `OutcomeUnknownNotice` | 结果未知警示：陈述风险 + 引导核对，绝不提供“直接重试” |
| `SecretPanel` | 一次性秘密（client_secret/TOTP）：复制 + 显式「我已保存，关闭」，不自动消失、不被遮罩误关 |
| `ReasonDialog` | 危险/审计写操作的统一确认（reason 必填，随请求入审计） |
| `Icon` | 内联 SVG（stroke 1.5、currentColor），名称枚举，禁止 emoji 图标 |

## 5. 交互规则

- 主要动作每屏一个（primary 唯一）；危险操作分区（概览页底部「危险操作」区）并需原因确认。
- 表格带「刷新」与游标「加载更多」；加载中有骨架/spinner 文案；空态给下一步动作。
- 表单错误关联到字段（不是只有顶部 toast）；本控制台**不使用 toast** 作为任何结果的唯一载体——结果在上下文内联展示。
- 快捷键不阻断输入；Esc 仅关闭顶层浮层。
- 键盘：全部控件可 Tab 到达，`:focus-visible` 统一焦点环（accent 2px outline-offset 2px）。

## 6. 动效

- 仅反馈型微动效：hover/active 背景、Dialog 淡入上移 8px、Drawer 滑入；时长 ≤180ms，ease-out。
- `@media (prefers-reduced-motion: reduce)`：过渡和动画缩至 0.01ms，动画单次运行；加载文案保留。浏览器可能把时长表示成 `1e-05s`，验收按数值判断。

## 7. 响应式

- 断点 860px：侧栏收起为顶栏 + 菜单按钮（Drawer 导航，键盘可用）。
- 375px：整页不横向溢出；表格在 `.table-scroll` 容器内滚动；Dialog 边距 12px 近全宽；按钮与输入框最小高度 40px。概览任务状态另起一行，避免挤窄说明文字。
- 连接列表保持足够的列宽，Issuer/Client ID 不拆成逐字多行；带名称的滚动 region 可通过 Tab 聚焦，再横向查看配置和操作。滚动容器建立定位上下文，约束隐藏的辅助表头文字。

## 9. 可访问性验收边界

原生字段关联 label、hint 与错误；顶层模态捕获焦点、隐藏背景、锁滚动，只响应顶层 Esc/Tab，关闭恢复触发元素。过滤 disabled、负 tabindex、hidden input、CSS 不可见与收起 details，React StrictMode 有回归测试。弱文本色在亮/暗主题中统一调整，焦点环可见。

当前证据为组件行为测试与真实 Chrome 的键盘/抽屉/小屏/减少动效检查，未完成全站 WCAG 认证或所有屏幕阅读器组合测试。

## 8. 品牌

- 标记：盾形内含钥匙孔的几何 SVG（`shared/components/Icon.tsx` 中 `logo`），字标「Authova」+ 弱级「控制台」。
- 强调色 `#4f46e5`（亮）/ `#818cf8`（暗）。真实 SVG 图标，不用 emoji 或占位图。
