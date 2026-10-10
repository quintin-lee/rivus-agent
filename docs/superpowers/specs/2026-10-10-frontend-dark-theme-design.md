# 前端深色主题统一设计文档

- 文档版本：v1.1（2026-10-10 修订：色板切换为深蓝科技风）
- 日期：2026-10-10
- 目标：统一深色设计语言，修复断裂的 shadcn 语义 token，不动布局
- 前置：`docs/superpowers/specs/2026-10-09-frontend-design.md`（页面结构以它为准）

---

## 1. 背景与根因

`frontend/src/components/ui/` 下的 shadcn 组件使用语义 token（`bg-background`、`bg-primary`、`ring-ring` 等），但 `tailwind.config.ts` 的 `theme.extend` 为空，`index.css` 没有任何 CSS 变量定义，token 实际不生效；业务组件则全部硬编码 `zinc-950/900/800`。两套体系混用是视觉不统一的根因。

## 2. 决策

- 方向：视觉样式打磨；重点：整体设计语言；主题：深色精修（不做浅色模式）。
- 方案：Token 化统一（不动布局、不换组件）。品牌主色与字体更换明确延期，不在本次范围。

## 3. 色板（深蓝科技风）

- 背景三级：页面 `222 47% 5.5%` 藏青、卡片 `222 45% 8%`、悬浮 `222 32% 13.5%`。
- 边框带蓝调：`214 45% 75% / 0.12`。
- 主色： vivid 蓝 `212 100% 58%`，焦点环同色；选中文本底 `primary/35`。
- 点睛：标题蓝→青渐变字、顶部径向蓝辉光、Rocket 图标用主色、滚动条 `primary/25`。
- 语义色：成功 emerald、失败 red、等待 amber，只统一透明度与边框用法，不引入新色。

## 4. 替换范围

改三类文件：

1. `frontend/src/index.css`：定义深色主题 CSS 变量（HSL）。
2. `frontend/tailwind.config.ts`：映射 `background/foreground/primary/muted/border/ring` 等语义色。
3. 业务组件（`App/Header/SessionPanel/RunForm/RunDetail/EventStream/ApprovalCard/ActionBar`）：`zinc-*` 硬编码换成语义类。

`ui/` 下 shadcn 组件不动。

## 5. 对比度与细节

- 正文三档：`zinc-100` 正文、`zinc-400` 次要、`zinc-500` 辅助，不再混用。
- 边框统一 `white/10` 系，卡片圆角统一 `rounded-lg`。
- 焦点环保留现有 `ring-ring`（token 修复后自动生效）。
- 滚动条加细窄深色样式。

## 6. 验收标准

- `vite build` 通过，无新增 TS/ESLint 错误。
- 无布局位移（改前改后截图对比）。
- 无残留硬编码色值与浅色类（grep 抽查 `zinc-*` 仅出现在 token 定义处）。

## 7. 非目标

- 浅色模式与主题切换。
- 品牌主色更换、字体引入（Inter/Geist/JetBrains Mono）。
- 布局调整与交互流程改动。
