# 原版桌面 HTTP / WS / native 完整静态索引

更新：2026-10-06。实施任务见[完整待办 D01–D35](go-desktop-parity-todo.md)，原始记录见[结构化盘点 JSON](go-desktop-full-api-inventory.json)。

本轮重新静态扫描并核对当前源码；没有执行复现或功能实现。CLI/TUI、Slash、worker、终端动作与 SDK 控制的详细工作见[CLI/TUI 索引](go-cli-tui-work-checklist.md)。

此索引来自当前源码静态读取，不代表运行通过。H 编号标识调用点，包含同一接口的重复调用；动态通用调用保留表达式与实际方法分支。原版 handler 中未被前端直接调用的选择器、条件和专用 Server 路径也保留。参数、body/schema、query、错误和服务行为实施时继续对照原 handler/types；不能只依据下列显示路径实现一个简化接口。

544 个桌面生产文件；240 个 HTTP 调用点（237 个路径可解析，3 个参数化 Blob 调用）；23 组顶层缺失影响 131 处调用，子接口缺失影响 31 处，public-access 注册缺失影响 6 处，合计 168 处确定缺少入口。**调用点含重复调用，不能当作唯一接口数或完成百分比。** Task 的五处调用已接入真实磁盘服务，组件/原版读取对照通过，完整桌面验收仍未关闭。

## 1. 桌面页面覆盖

| 完整 Electron Settings 页 | 入口 | 任务 |
| --- | --- | --- |
| `about` | [desktop/src/pages/Settings.tsx:88](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:88) | D29、D33、D35 |
| `diagnostics` | [desktop/src/pages/Settings.tsx:85](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:85) | D22、D23、D33 |
| `trace` | [desktop/src/pages/Settings.tsx:84](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:84) | D21、D27、D34 |
| `computerUse` | [desktop/src/pages/Settings.tsx:82](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:82) | D19、D31、D33 |
| `pets` | [desktop/src/pages/Settings.tsx:81](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:81) | D29、D33、D34 |
| `plugins` | [desktop/src/pages/Settings.tsx:80](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:80) | D03、D04、D05、D32、D34 |
| `memory` | [desktop/src/pages/Settings.tsx:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:79) | D14、D32、D34 |
| `skills` | [desktop/src/pages/Settings.tsx:78](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:78) | D02、D04、D32 |
| `agents` | [desktop/src/pages/Settings.tsx:77](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:77) | D06、D31、D32 |
| `mcp` | [desktop/src/pages/Settings.tsx:76](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:76) | D05、D31、D32 |
| `terminal` | [desktop/src/pages/Settings.tsx:75](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:75) | D29、D32、D33 |
| `adapters` | [desktop/src/pages/Settings.tsx:74](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:74) | D18、D33、D34 |
| `h5Access` | [desktop/src/pages/Settings.tsx:73](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:73) | D30、D33、D34 |
| `voice` | [desktop/src/pages/Settings.tsx:72](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:72) | D20、D33 |
| `general` | [desktop/src/pages/Settings.tsx:71](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:71) | D29、D31、D33、D34 |
| `activity` | [desktop/src/pages/Settings.tsx:83](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:83) | D24、D34 |
| `providers` | [desktop/src/pages/Settings.tsx:70](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/pages/Settings.tsx:70) | D15、D16、D17、D29、D34 |

Settings 会按 host 分流；browserHost 的 H5 Settings 与 Electron 完整设置页是不同覆盖范围。

内容路由包含：`connectors`, `market`, `scheduled`, `session`, `settings`, `subagent`, `team`, `team-member`, `terminal`, `trace`, `traces`, `workbench`。其中原 workbench 分支是旧状态兼容重定向，需要保留迁移，不能误当成第二套待重写工作台。

## 2. 35 组原版核心资源

| 资源 | 原版路由 | 当前 Go 分类 | 工作包 |
| --- | --- | --- | --- |
| `session-collaboration` | [src/server/router.ts:112](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:112) | 整组缺失（501） | D12 |
| `sessions` | [src/server/router.ts:114](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:114) | 已有资源路由；仍须逐子接口/分支验收 | D26–D28 |
| `conversations` | [src/server/router.ts:123](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:123) | 整组缺失（501） | D11 |
| `settings` | [src/server/router.ts:126](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:126) | 已有资源路由；仍须逐子接口/分支验收 | D29 |
| `models` | [src/server/router.ts:129](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:129) | 已有资源路由；仍须逐子接口/分支验收 | D29 |
| `effort` | [src/server/router.ts:130](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:130) | 已有资源路由；仍须逐子接口/分支验收 | D29 |
| `permissions` | [src/server/router.ts:133](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:133) | 已有资源路由；仍须逐子接口/分支验收 | D29/D31 |
| `scheduled-tasks` | [src/server/router.ts:136](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:136) | 整组缺失（501） | D08 |
| `search` | [src/server/router.ts:139](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:139) | 整组缺失（501） | D13 |
| `agents` | [src/server/router.ts:142](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:142) | 整组缺失（501） | D06 |
| `tasks` | [src/server/router.ts:143](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:143) | 已有资源路由；仍须逐子接口/分支验收 | D07 |
| `status` | [src/server/router.ts:146](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:146) | 已有资源路由；仍须逐子接口/分支验收 | D29 |
| `teams` | [src/server/router.ts:149](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:149) | 整组缺失（501） | D09 |
| `workflows` | [src/server/router.ts:152](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:152) | 整组缺失（501） | D10 |
| `providers` | [src/server/router.ts:155](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:155) | 已有资源路由；仍须逐子接口/分支验收 | D29 |
| `haha-oauth` | [src/server/router.ts:158](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:158) | 整组缺失（501） | D15 |
| `haha-openai-oauth` | [src/server/router.ts:161](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:161) | 整组缺失（501） | D16 |
| `haha-grok-oauth` | [src/server/router.ts:164](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:164) | 整组缺失（501） | D17 |
| `adapters` | [src/server/router.ts:167](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:167) | 整组缺失（501） | D18 |
| `skills` | [src/server/router.ts:172](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:172) | 列表/详情已接；mentions 仍 501，运行时待接 | D02 |
| `market` | [src/server/router.ts:175](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:175) | 整组缺失（501） | D03 |
| `mcp` | [src/server/router.ts:178](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:178) | 整组缺失（501） | D05 |
| `connectors` | [src/server/router.ts:181](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:181) | 整组缺失（501） | D01 |
| `plugins` | [src/server/router.ts:184](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:184) | 整组缺失（501） | D04 |
| `computer-use` | [src/server/router.ts:187](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:187) | 整组缺失（501） | D19 |
| `diagnostics` | [src/server/router.ts:190](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:190) | 整组缺失（501） | D22 |
| `doctor` | [src/server/router.ts:193](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:193) | 整组缺失（501） | D23 |
| `h5-access` | [src/server/router.ts:196](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:196) | 已有资源路由；仍须逐子接口/分支验收 | D30 |
| `activity-stats` | [src/server/router.ts:199](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:199) | 整组缺失（501） | D24 |
| `open-targets` | [src/server/router.ts:202](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:202) | 整组缺失（501） | D25 |
| `memory` | [src/server/router.ts:205](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:205) | 整组缺失（501） | D14 |
| `desktop-ui` | [src/server/router.ts:208](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:208) | 已有资源路由；仍须逐子接口/分支验收 | D29/D33 |
| `voice` | [src/server/router.ts:211](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:211) | 整组缺失（501） | D20 |
| `traces` | [src/server/router.ts:214](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:214) | 整组缺失（501） | D21 |
| `filesystem` | [src/server/router.ts:217](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/router.ts:217) | 已有资源路由；仍须逐子接口/分支验收 | D28 |

## 3. 全部 240 个前端 HTTP 调用点

### activity-stats · D24

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H001 | GET | `/api/activity-stats` | response | 整组缺失（501） | [desktop/src/api/activityStats.ts:81](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/activityStats.ts:81) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### adapters · D18

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H002 | GET | `/api/adapters` | getConfig | 整组缺失（501） | [desktop/src/api/adapters.ts:73](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:73) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H003 | PUT | `/api/adapters` | updateConfig | 整组缺失（501） | [desktop/src/api/adapters.ts:77](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:77) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H004 | POST | `/api/adapters/wechat/login/start` | startWechatLogin | 整组缺失（501） | [desktop/src/api/adapters.ts:81](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:81) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H005 | POST | `/api/adapters/wechat/login/poll` | pollWechatLogin | 整组缺失（501） | [desktop/src/api/adapters.ts:85](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:85) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H006 | POST | `/api/adapters/wechat/unbind` | unbindWechat | 整组缺失（501） | [desktop/src/api/adapters.ts:92](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:92) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H007 | POST | `/api/adapters/dingtalk/unbind` | unbindDingtalk | 整组缺失（501） | [desktop/src/api/adapters.ts:96](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:96) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H008 | POST | `/api/adapters/dingtalk/registration/begin` | beginDingtalkRegistration | 整组缺失（501） | [desktop/src/api/adapters.ts:100](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:100) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H009 | POST | `/api/adapters/dingtalk/registration/poll` | pollDingtalkRegistration | 整组缺失（501） | [desktop/src/api/adapters.ts:104](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:104) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H010 | POST | `/api/adapters/whatsapp/login/start` | startWhatsAppLogin | 整组缺失（501） | [desktop/src/api/adapters.ts:108](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:108) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H011 | POST | `/api/adapters/whatsapp/login/poll` | pollWhatsAppLogin | 整组缺失（501） | [desktop/src/api/adapters.ts:112](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:112) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H012 | POST | `/api/adapters/whatsapp/unbind` | unbindWhatsApp | 整组缺失（501） | [desktop/src/api/adapters.ts:116](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:116) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H013 | POST | `/api/adapters/feishu/registration/begin` | beginFeishuRegistration | 整组缺失（501） | [desktop/src/api/adapters.ts:120](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:120) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H014 | POST | `/api/adapters/feishu/registration/poll` | pollFeishuRegistration | 整组缺失（501） | [desktop/src/api/adapters.ts:124](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:124) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H015 | POST | `/api/adapters/feishu/registration/cancel` | cancelFeishuRegistration | 整组缺失（501） | [desktop/src/api/adapters.ts:128](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:128) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H016 | POST | `/api/adapters/feishu/unbind` | unbindFeishu | 整组缺失（501） | [desktop/src/api/adapters.ts:132](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:132) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H017 | POST | `/api/adapters/wecom/login/start` | startWecomLogin | 整组缺失（501） | [desktop/src/api/adapters.ts:136](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:136) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H018 | POST | `/api/adapters/wecom/login/poll` | pollWecomLogin | 整组缺失（501） | [desktop/src/api/adapters.ts:140](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:140) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H019 | POST | `/api/adapters/wecom/unbind` | unbindWecom | 整组缺失（501） | [desktop/src/api/adapters.ts:144](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:144) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H020 | POST | `/api/adapters/qq/login/start` | startQqLogin | 整组缺失（501） | [desktop/src/api/adapters.ts:148](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:148) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H021 | POST | `/api/adapters/qq/login/poll` | pollQqLogin | 整组缺失（501） | [desktop/src/api/adapters.ts:152](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:152) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H022 | POST | `/api/adapters/qq/unbind` | unbindQq | 整组缺失（501） | [desktop/src/api/adapters.ts:156](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:156) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H023 | GET | `/api/adapters/slack/manifest` | getSlackManifest | 整组缺失（501） | [desktop/src/api/adapters.ts:160](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:160) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H024 | POST | `/api/adapters/slack/unbind` | unbindSlack | 整组缺失（501） | [desktop/src/api/adapters.ts:164](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/adapters.ts:164) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### agents · D06

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H025 | GET | `/api/agents` | list | 整组缺失（501） | [desktop/src/api/agents.ts:90](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/agents.ts:90) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H026 | POST | `/api/agents` | create | 整组缺失（501） | [desktop/src/api/agents.ts:93](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/agents.ts:93) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H027 | PUT | `/api/agents/{name}` | update | 整组缺失（501） | [desktop/src/api/agents.ts:95](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/agents.ts:95) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H028 | DELETE | `/api/agents/{name}` | delete | 整组缺失（501） | [desktop/src/api/agents.ts:100](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/agents.ts:100) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H029 | PUT | `/api/agents/{name}/override` | setOverride | 整组缺失（501） | [desktop/src/api/agents.ts:103](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/agents.ts:103) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H030 | DELETE | `/api/agents/{name}/override` | clearOverride | 整组缺失（501） | [desktop/src/api/agents.ts:111](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/agents.ts:111) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H031 | POST | `/api/agents/reload` | reload | 整组缺失（501） | [desktop/src/api/agents.ts:116](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/agents.ts:116) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### computer-use · D19

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H039 | GET | `/api/computer-use/app-icon` | blob | 整组缺失（501） | [desktop/src/api/computerUse.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/computerUse.ts:42) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H040 | GET | `/api/computer-use/status` | getStatus | 整组缺失（501） | [desktop/src/api/computerUse.ts:169](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/computerUse.ts:169) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H041 | POST | `/api/computer-use/setup` | runSetup | 整组缺失（501） | [desktop/src/api/computerUse.ts:172](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/computerUse.ts:172) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H042 | GET | `/api/computer-use/apps` | getInstalledApps | 整组缺失（501） | [desktop/src/api/computerUse.ts:175](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/computerUse.ts:175) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H043 | GET | `/api/computer-use/authorized-apps` | getAuthorizedApps | 整组缺失（501） | [desktop/src/api/computerUse.ts:178](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/computerUse.ts:178) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H044 | PUT | `/api/computer-use/authorized-apps` | setAuthorizedApps | 整组缺失（501） | [desktop/src/api/computerUse.ts:191](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/computerUse.ts:191) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H045 | POST | `/api/computer-use/open-settings` | openSettings | 整组缺失（501） | [desktop/src/api/computerUse.ts:194](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/computerUse.ts:194) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H046 | POST | `/api/computer-use/open-permission-card` | openPermissionCard | 整组缺失（501） | [desktop/src/api/computerUse.ts:224](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/computerUse.ts:224) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### connectors · D01

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H047 | GET | `/api/connectors` | list | 整组缺失（501） | [desktop/src/api/connectors.ts:5](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/connectors.ts:5) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H048 | GET | `/api/connectors/{id}` | detail | 整组缺失（501） | [desktop/src/api/connectors.ts:6](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/connectors.ts:6) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H049 | POST | `/api/connectors/{id}/{action}` | action | 整组缺失（501） | [desktop/src/api/connectors.ts:8](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/connectors.ts:8) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### desktop-ui · D29/D33

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H050 | GET | `/api/desktop-ui/preferences` | getPreferences | 资源已路由；逐分支/行为待验收 | [desktop/src/api/desktopUiPreferences.ts:61](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/desktopUiPreferences.ts:61) | 按原 handler 分支核对 |
| H051 | GET | `/api/desktop-ui/preferences/pet` | getPetPreferences | 资源已路由；逐分支/行为待验收 | [desktop/src/api/desktopUiPreferences.ts:65](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/desktopUiPreferences.ts:65) | 按原 handler 分支核对 |
| H052 | PUT | `/api/desktop-ui/preferences/sidebar` | updateSidebarPreferences | 资源已路由；逐分支/行为待验收 | [desktop/src/api/desktopUiPreferences.ts:69](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/desktopUiPreferences.ts:69) | 按原 handler 分支核对 |
| H053 | PUT | `/api/desktop-ui/preferences/project-display-name` | updateProjectDisplayName | 资源已路由；逐分支/行为待验收 | [desktop/src/api/desktopUiPreferences.ts:76](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/desktopUiPreferences.ts:76) | 按原 handler 分支核对 |
| H054 | PUT | `/api/desktop-ui/preferences/profile` | updateProfilePreferences | 资源已路由；逐分支/行为待验收 | [desktop/src/api/desktopUiPreferences.ts:83](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/desktopUiPreferences.ts:83) | 按原 handler 分支核对 |
| H055 | PUT | `/api/desktop-ui/preferences/pet` | updatePetPreferences | 资源已路由；逐分支/行为待验收 | [desktop/src/api/desktopUiPreferences.ts:90](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/desktopUiPreferences.ts:90) | 按原 handler 分支核对 |
| H056 | DELETE | `/api/desktop-ui/preferences/profile/avatar` | deleteProfileAvatar | 资源已路由；逐分支/行为待验收 | [desktop/src/api/desktopUiPreferences.ts:101](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/desktopUiPreferences.ts:101) | 按原 handler 分支核对 |
| H057 | PUT | `/api/desktop-ui/preferences/profile/avatar` | res | 资源已路由；逐分支/行为待验收 | [desktop/src/api/desktopUiPreferences.ts:121](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/desktopUiPreferences.ts:121) | 按原 handler 分支核对 |

### diagnostics · D22

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H037 | POST | `/api/diagnostics/events` | rawRecordDiagnosticEvent | 整组缺失（501） | [desktop/src/api/client.ts:431](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/client.ts:431) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H058 | GET | `/api/diagnostics/status` | getStatus | 整组缺失（501） | [desktop/src/api/diagnostics.ts:62](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/diagnostics.ts:62) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H059 | GET | `/api/diagnostics/local-index` | getLocalIndexStatus | 整组缺失（501） | [desktop/src/api/diagnostics.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/diagnostics.ts:63) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H060 | POST | `/api/diagnostics/local-index/rebuild` | rebuildLocalIndex | 整组缺失（501） | [desktop/src/api/diagnostics.ts:64](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/diagnostics.ts:64) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H061 | GET | `/api/diagnostics/events` | getEvents | 整组缺失（501） | [desktop/src/api/diagnostics.ts:65](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/diagnostics.ts:65) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H062 | GET | `/api/diagnostics/issue-report` | getIssueReport | 整组缺失（501） | [desktop/src/api/diagnostics.ts:66](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/diagnostics.ts:66) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H063 | POST | `/api/diagnostics/events` | recordEvent | 整组缺失（501） | [desktop/src/api/diagnostics.ts:67](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/diagnostics.ts:67) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H064 | POST | `/api/diagnostics/export` | exportBundle | 整组缺失（501） | [desktop/src/api/diagnostics.ts:68](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/diagnostics.ts:68) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H065 | POST | `/api/diagnostics/open-log-dir` | openLogDir | 整组缺失（501） | [desktop/src/api/diagnostics.ts:69](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/diagnostics.ts:69) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H066 | DELETE | `/api/diagnostics` | clear | 整组缺失（501） | [desktop/src/api/diagnostics.ts:70](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/diagnostics.ts:70) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### doctor · D23

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H067 | GET | `/api/doctor/report` | report | 整组缺失（501） | [desktop/src/api/doctor.ts:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/doctor.ts:39) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### effort · D29

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H105 | GET | `/api/effort` | getEffort | 资源已路由；逐分支/行为待验收 | [desktop/src/api/models.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/models.ts:22) | 按原 handler 分支核对 |
| H106 | PUT | `/api/effort` | setEffort | 资源已路由；逐分支/行为待验收 | [desktop/src/api/models.ts:26](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/models.ts:26) | 按原 handler 分支核对 |

### filesystem · D28

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H068 | GET | `/api/filesystem/browse` | browse | 资源已路由；逐分支/行为待验收 | [desktop/src/api/filesystem.ts:23](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/filesystem.ts:23) | 按原 handler 分支核对 |
| H069 | GET | `/api/filesystem/browse` | search | 资源已路由；逐分支/行为待验收 | [desktop/src/api/filesystem.ts:29](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/filesystem.ts:29) | 按原 handler 分支核对 |

### h5-access · D30

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H070 | GET | `/api/h5-access` | get | 资源已路由；逐分支/行为待验收 | [desktop/src/api/h5Access.ts:18](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/h5Access.ts:18) | 按原 handler 分支核对 |
| H071 | POST | `/api/h5-access/enable` | enable | 资源已路由；逐分支/行为待验收 | [desktop/src/api/h5Access.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/h5Access.ts:22) | 按原 handler 分支核对 |
| H072 | POST | `/api/h5-access/disable` | disable | 资源已路由；逐分支/行为待验收 | [desktop/src/api/h5Access.ts:26](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/h5Access.ts:26) | 按原 handler 分支核对 |
| H073 | POST | `/api/h5-access/regenerate` | regenerate | 资源已路由；逐分支/行为待验收 | [desktop/src/api/h5Access.ts:30](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/h5Access.ts:30) | 按原 handler 分支核对 |
| H074 | PUT | `/api/h5-access` | update | 资源已路由；逐分支/行为待验收 | [desktop/src/api/h5Access.ts:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/h5Access.ts:39) | 按原 handler 分支核对 |
| H238 | POST | `/api/h5-access/verify` | verifyH5Access | 资源已路由；逐分支/行为待验收 | [desktop/src/lib/desktopRuntime.ts:328](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopRuntime.ts:328) | 按原 handler 分支核对 |

### haha-grok-oauth · D17

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H075 | POST | `/api/haha-grok-oauth/start` | start | 整组缺失（501） | [desktop/src/api/hahaGrokOAuth.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/hahaGrokOAuth.ts:22) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H076 | GET | `/api/haha-grok-oauth` | status | 整组缺失（501） | [desktop/src/api/hahaGrokOAuth.ts:29](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/hahaGrokOAuth.ts:29) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H077 | DELETE | `/api/haha-grok-oauth` | logout | 整组缺失（501） | [desktop/src/api/hahaGrokOAuth.ts:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/hahaGrokOAuth.ts:37) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### haha-oauth · D15

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H078 | POST | `/api/haha-oauth/start` | start | 整组缺失（501） | [desktop/src/api/hahaOAuth.ts:25](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/hahaOAuth.ts:25) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H079 | GET | `/api/haha-oauth` | status | 整组缺失（501） | [desktop/src/api/hahaOAuth.ts:32](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/hahaOAuth.ts:32) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H080 | DELETE | `/api/haha-oauth` | logout | 整组缺失（501） | [desktop/src/api/hahaOAuth.ts:36](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/hahaOAuth.ts:36) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### haha-openai-oauth · D16

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H081 | POST | `/api/haha-openai-oauth/start` | start | 整组缺失（501） | [desktop/src/api/hahaOpenAIOAuth.ts:25](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/hahaOpenAIOAuth.ts:25) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H082 | GET | `/api/haha-openai-oauth` | status | 整组缺失（501） | [desktop/src/api/hahaOpenAIOAuth.ts:32](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/hahaOpenAIOAuth.ts:32) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H083 | DELETE | `/api/haha-openai-oauth` | logout | 整组缺失（501） | [desktop/src/api/hahaOpenAIOAuth.ts:36](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/hahaOpenAIOAuth.ts:36) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### market · D03

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H084 | GET | `/api/market/skills` | list | 整组缺失（501） | [desktop/src/api/market.ts:33](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/market.ts:33) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H085 | GET | `/api/market/skills/{source}/{slug}` | detail | 整组缺失（501） | [desktop/src/api/market.ts:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/market.ts:37) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H086 | GET | `/api/market/skills/{source}/{slug}/file` | fileContent | 整组缺失（501） | [desktop/src/api/market.ts:43](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/market.ts:43) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H087 | POST | `/api/market/install` | install | 整组缺失（501） | [desktop/src/api/market.ts:49](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/market.ts:49) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H088 | POST | `/api/market/uninstall` | uninstall | 整组缺失（501） | [desktop/src/api/market.ts:56](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/market.ts:56) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H089 | GET | `/api/market/status` | status | 整组缺失（501） | [desktop/src/api/market.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/market.ts:63) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### mcp · D05

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H090 | GET | `/api/mcp` | list | 整组缺失（501） | [desktop/src/api/mcp.ts:7](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/mcp.ts:7) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H091 | GET | `/api/mcp/project-paths` | projectPaths | 整组缺失（501） | [desktop/src/api/mcp.ts:11](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/mcp.ts:11) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H092 | GET | `/api/mcp/{name}/status` | status | 整组缺失（501） | [desktop/src/api/mcp.ts:16](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/mcp.ts:16) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H093 | POST | `/api/mcp` | create | 整组缺失（501） | [desktop/src/api/mcp.ts:20](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/mcp.ts:20) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H094 | PUT | `/api/mcp/{name}` | update | 整组缺失（501） | [desktop/src/api/mcp.ts:28](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/mcp.ts:28) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H095 | DELETE | `/api/mcp/{name}` | remove | 整组缺失（501） | [desktop/src/api/mcp.ts:38](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/mcp.ts:38) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H096 | POST | `/api/mcp/{name}/toggle` | toggle | 整组缺失（501） | [desktop/src/api/mcp.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/mcp.ts:42) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H097 | POST | `/api/mcp/{name}/reconnect` | reconnect | 整组缺失（501） | [desktop/src/api/mcp.ts:52](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/mcp.ts:52) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### memory · D14

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H098 | GET | `/api/memory/projects` | listProjects | 整组缺失（501） | [desktop/src/api/memory.ts:7](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/memory.ts:7) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H099 | GET | `/api/memory/files` | listFiles | 整组缺失（501） | [desktop/src/api/memory.ts:12](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/memory.ts:12) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H100 | GET | `/api/memory/file` | readFile | 整组缺失（501） | [desktop/src/api/memory.ts:17](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/memory.ts:17) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H101 | PUT | `/api/memory/file` | saveFile | 整组缺失（501） | [desktop/src/api/memory.ts:27](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/memory.ts:27) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### models · D29

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H102 | GET | `/api/models` | list | 资源已路由；逐分支/行为待验收 | [desktop/src/api/models.ts:10](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/models.ts:10) | 按原 handler 分支核对 |
| H103 | GET | `/api/models/current` | getCurrent | 资源已路由；逐分支/行为待验收 | [desktop/src/api/models.ts:14](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/models.ts:14) | 按原 handler 分支核对 |
| H104 | PUT | `/api/models/current` | setCurrent | 资源已路由；逐分支/行为待验收 | [desktop/src/api/models.ts:18](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/models.ts:18) | 按原 handler 分支核对 |

### open-targets · D25

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H107 | GET | `/api/open-targets` | list | 整组缺失（501） | [desktop/src/api/openTargets.ts:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/openTargets.ts:34) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H108 | GET | `/api/open-targets` | listForPath | 整组缺失（501） | [desktop/src/api/openTargets.ts:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/openTargets.ts:37) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H109 | POST | `/api/open-targets/open` | open | 整组缺失（501） | [desktop/src/api/openTargets.ts:40](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/openTargets.ts:40) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### permissions · D29/D31

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H186 | GET | `/api/permissions/mode` | getPermissionMode | 资源已路由；逐分支/行为待验收 | [desktop/src/api/settings.ts:50](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/settings.ts:50) | 按原 handler 分支核对 |
| H187 | PUT | `/api/permissions/mode` | setPermissionMode | 资源已路由；逐分支/行为待验收 | [desktop/src/api/settings.ts:54](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/settings.ts:54) | 按原 handler 分支核对 |

### plugins · D04

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H110 | GET | `/api/plugins` | list | 整组缺失（501） | [desktop/src/api/plugins.ts:20](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/plugins.ts:20) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H111 | GET | `/api/plugins/detail` | detail | 整组缺失（501） | [desktop/src/api/plugins.ts:26](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/plugins.ts:26) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H112 | POST | `/api/plugins/enable` | enable | 整组缺失（501） | [desktop/src/api/plugins.ts:30](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/plugins.ts:30) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H113 | POST | `/api/plugins/disable` | disable | 整组缺失（501） | [desktop/src/api/plugins.ts:33](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/plugins.ts:33) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H114 | POST | `/api/plugins/update` | update | 整组缺失（501） | [desktop/src/api/plugins.ts:36](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/plugins.ts:36) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H115 | POST | `/api/plugins/uninstall` | uninstall | 整组缺失（501） | [desktop/src/api/plugins.ts:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/plugins.ts:39) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H116 | POST | `/api/plugins/reload` | reload | 整组缺失（501） | [desktop/src/api/plugins.ts:46](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/plugins.ts:46) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### providers · D29

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H117 | GET | `/api/providers` | list | 资源已路由；逐分支/行为待验收 | [desktop/src/api/providers.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:31) | 按原 handler 分支核对 |
| H118 | GET | `/api/providers/auth-status` | authStatus | 资源已路由；逐分支/行为待验收 | [desktop/src/api/providers.ts:35](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:35) | 按原 handler 分支核对 |
| H119 | GET | `/api/providers/settings` | getSettings | 资源已路由；逐分支/行为待验收 | [desktop/src/api/providers.ts:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:39) | 按原 handler 分支核对 |
| H120 | PUT | `/api/providers/settings` | updateSettings | 资源已路由；逐分支/行为待验收 | [desktop/src/api/providers.ts:43](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:43) | 按原 handler 分支核对 |
| H121 | POST | `/api/providers` | create | 资源已路由；逐分支/行为待验收 | [desktop/src/api/providers.ts:47](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:47) | 按原 handler 分支核对 |
| H122 | PUT | `/api/providers/{id}` | update | 资源已路由；逐分支/行为待验收 | [desktop/src/api/providers.ts:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:51) | 按原 handler 分支核对 |
| H123 | DELETE | `/api/providers/{id}` | delete | 资源已路由；逐分支/行为待验收 | [desktop/src/api/providers.ts:55](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:55) | 按原 handler 分支核对 |
| H124 | POST | `/api/providers/{id}/activate` | activate | 资源已路由；逐分支/行为待验收 | [desktop/src/api/providers.ts:59](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:59) | 按原 handler 分支核对 |
| H125 | POST | `/api/providers/official` | activateOfficial | 资源已路由；逐分支/行为待验收 | [desktop/src/api/providers.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:63) | 按原 handler 分支核对 |
| H126 | GET | `/api/providers/{id}/models` | getOfficialModels | 子接口缺失（显式 501） | [desktop/src/api/providers.ts:67](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:67) | `backend/internal/server/providers.go:111,145` |
| H127 | PUT | `/api/providers/{id}/models` | updateOfficialModels | 子接口缺失（显式 501） | [desktop/src/api/providers.ts:71](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:71) | `backend/internal/server/providers.go:111,145` |
| H128 | PUT | `/api/providers/reorder` | reorder | 资源已路由；逐分支/行为待验收 | [desktop/src/api/providers.ts:75](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:75) | 按原 handler 分支核对 |
| H129 | POST | `/api/providers/{id}/test` | test | 子接口缺失（显式 501） | [desktop/src/api/providers.ts:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:79) | `backend/internal/server/providers.go:111,145` |
| H130 | POST | `/api/providers/test` | testConfig | 子接口缺失（显式 501） | [desktop/src/api/providers.ts:83](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:83) | `backend/internal/server/providers.go:111,145` |
| H131 | GET | `/api/providers/cc-switch/scan` | scanCcSwitch | 子接口缺失（显式 501） | [desktop/src/api/providers.ts:87](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:87) | `backend/internal/server/providers.go:111,145` |
| H132 | POST | `/api/providers/cc-switch/import` | importCcSwitch | 子接口缺失（显式 501） | [desktop/src/api/providers.ts:91](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:91) | `backend/internal/server/providers.go:111,145` |
| H133 | POST | `/api/providers/models` | fetchModels | 子接口缺失（显式 501） | [desktop/src/api/providers.ts:99](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/providers.ts:99) | `backend/internal/server/providers.go:111,145` |

### public-access · D30

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H134 | GET | `/api/public-access` | get | 专用 Server 注册缺失 | [desktop/src/api/publicAccess.ts:13](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/publicAccess.ts:13) | `src/server/index.ts:285; src/server/publicAccess.ts:69; backend/internal/server/server.go:441` |
| H135 | POST | `/api/public-access/pairing` | pairing | 专用 Server 注册缺失 | [desktop/src/api/publicAccess.ts:14](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/publicAccess.ts:14) | `src/server/index.ts:285; src/server/publicAccess.ts:69; backend/internal/server/server.go:441` |
| H136 | POST | `/api/public-access/approve` | approve | 专用 Server 注册缺失 | [desktop/src/api/publicAccess.ts:15](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/publicAccess.ts:15) | `src/server/index.ts:285; src/server/publicAccess.ts:69; backend/internal/server/server.go:441` |
| H137 | POST | `/api/public-access/reject` | reject | 专用 Server 注册缺失 | [desktop/src/api/publicAccess.ts:16](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/publicAccess.ts:16) | `src/server/index.ts:285; src/server/publicAccess.ts:69; backend/internal/server/server.go:441` |
| H138 | POST | `/api/public-access/revoke` | revoke | 专用 Server 注册缺失 | [desktop/src/api/publicAccess.ts:17](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/publicAccess.ts:17) | `src/server/index.ts:285; src/server/publicAccess.ts:69; backend/internal/server/server.go:441` |
| H139 | GET&#124;POST | `/api/public-access/{path}` | response | 专用 Server 注册缺失 | [desktop/src/api/publicAccess.ts:25](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/publicAccess.ts:25) | `src/server/index.ts:285; src/server/publicAccess.ts:69; backend/internal/server/server.go:441` |

### scheduled-tasks · D08

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H197 | GET | `/api/scheduled-tasks` | list | 整组缺失（501） | [desktop/src/api/tasks.ts:36](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/tasks.ts:36) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H198 | POST | `/api/scheduled-tasks` | create | 整组缺失（501） | [desktop/src/api/tasks.ts:40](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/tasks.ts:40) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H199 | PUT | `/api/scheduled-tasks/{id}` | update | 整组缺失（501） | [desktop/src/api/tasks.ts:44](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/tasks.ts:44) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H200 | DELETE | `/api/scheduled-tasks/{id}` | delete | 整组缺失（501） | [desktop/src/api/tasks.ts:48](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/tasks.ts:48) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H201 | POST | `/api/scheduled-tasks/{id}/run` | runTask | 整组缺失（501） | [desktop/src/api/tasks.ts:52](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/tasks.ts:52) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H202 | GET | `/api/scheduled-tasks/runs` | getRecentRuns | 整组缺失（501） | [desktop/src/api/tasks.ts:56](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/tasks.ts:56) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H203 | GET | `/api/scheduled-tasks/{taskId}/runs` | getTaskRuns | 整组缺失（501） | [desktop/src/api/tasks.ts:60](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/tasks.ts:60) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H204 | GET | `/api/scheduled-tasks/runs/{runId}` | getRunDetail | 整组缺失（501） | [desktop/src/api/tasks.ts:64](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/tasks.ts:64) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### search · D13

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H148 | POST | `/api/search` | search | 整组缺失（501） | [desktop/src/api/search.ts:50](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/search.ts:50) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H149 | POST | `/api/search/sessions` | searchSessions | 整组缺失（501） | [desktop/src/api/search.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/search.ts:58) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### session-collaboration · D12

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H150 | GET | `/api/session-collaboration` | list | 整组缺失（501） | [desktop/src/api/sessionCollaboration.ts:12](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessionCollaboration.ts:12) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### sessions · D26–D28

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H140 | GET | `/api/sessions/{sessionId}/review/revision` | getRevision | 子接口缺失（default 501） | [desktop/src/api/review.ts:174](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/review.ts:174) | `backend/internal/server/sessions.go:430` |
| H141 | GET | `/api/sessions/{sessionId}/review` | getStatus | 子接口缺失（default 501） | [desktop/src/api/review.ts:178](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/review.ts:178) | `backend/internal/server/sessions.go:430` |
| H142 | GET | `/api/sessions/{sessionId}/review/diff` | getDiff | 子接口缺失（default 501） | [desktop/src/api/review.ts:191](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/review.ts:191) | `backend/internal/server/sessions.go:430` |
| H143 | POST | `/api/sessions/{sessionId}/review/stage` | stage | 子接口缺失（default 501） | [desktop/src/api/review.ts:195](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/review.ts:195) | `backend/internal/server/sessions.go:430` |
| H144 | POST | `/api/sessions/{sessionId}/review/unstage` | unstage | 子接口缺失（default 501） | [desktop/src/api/review.ts:199](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/review.ts:199) | `backend/internal/server/sessions.go:430` |
| H145 | POST | `/api/sessions/{sessionId}/review/stage-hunk` | stageHunk | 子接口缺失（default 501） | [desktop/src/api/review.ts:203](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/review.ts:203) | `backend/internal/server/sessions.go:430` |
| H146 | POST | `/api/sessions/{sessionId}/review/unstage-hunk` | unstageHunk | 子接口缺失（default 501） | [desktop/src/api/review.ts:207](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/review.ts:207) | `backend/internal/server/sessions.go:430` |
| H147 | POST | `/api/sessions/{sessionId}/review/revert` | revert | 子接口缺失（default 501） | [desktop/src/api/review.ts:216](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/review.ts:216) | `backend/internal/server/sessions.go:430` |
| H151 | GET | `/api/sessions/{sessionId}/git-info` | request | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:403](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:403) | 按原 handler 分支核对 |
| H152 | GET | `/api/sessions` | list | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:448](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:448) | 按原 handler 分支核对 |
| H153 | GET | `/api/sessions/project-history` | listProjectHistory | 子接口缺失（显式 501） | [desktop/src/api/sessions.ts:457](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:457) | `backend/internal/server/sessions.go:389` |
| H154 | GET | `/api/sessions/{sessionId}/messages` | newest | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:463](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:463) | 按原 handler 分支核对 |
| H155 | GET | `/api/sessions/{sessionId}/messages` | older | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:479](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:479) | 按原 handler 分支核对 |
| H156 | GET | `/api/sessions/{sessionId}/messages` | getHistoryPage | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:509](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:509) | 按原 handler 分支核对 |
| H157 | GET | `/api/sessions/{sessionId}/history-recovery` | getHistoryRecovery | 子接口缺失（default 501） | [desktop/src/api/sessions.ts:513](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:513) | `backend/internal/server/sessions.go:430` |
| H158 | GET | `/api/sessions/{sessionId}/summary` | getSummary | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:517](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:517) | 按原 handler 分支核对 |
| H159 | GET | `/api/sessions/{sessionId}/chat/status` | getChatStatus | 子接口缺失（default 501） | [desktop/src/api/sessions.ts:521](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:521) | `backend/internal/server/sessions.go:430` |
| H160 | GET | `/api/sessions/{sessionId}/trace` | getTrace | 子接口缺失（default 501） | [desktop/src/api/sessions.ts:530](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:530) | `backend/internal/server/sessions.go:430` |
| H161 | GET | `/api/sessions/{sessionId}/trace/calls/{callId}` | getTraceCall | 子接口缺失（default 501） | [desktop/src/api/sessions.ts:534](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:534) | `backend/internal/server/sessions.go:430` |
| H162 | POST | `/api/sessions` | create | 部分：repository/worktree 被拒绝 | [desktop/src/api/sessions.ts:541](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:541) | `backend/internal/server/sessions.go:86` |
| H163 | POST | `/api/sessions/{sessionId}/branch` | branch | 子接口缺失（default 501） | [desktop/src/api/sessions.ts:545](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:545) | `backend/internal/server/sessions.go:430` |
| H164 | DELETE | `/api/sessions/{sessionId}` | delete | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:549](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:549) | 按原 handler 分支核对 |
| H165 | POST | `/api/sessions/batch-delete` | batchDelete | 子接口缺失（显式 501） | [desktop/src/api/sessions.ts:553](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:553) | `backend/internal/server/sessions.go:389` |
| H166 | PATCH | `/api/sessions/{sessionId}` | rename | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:557](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:557) | 按原 handler 分支核对 |
| H167 | GET | `/api/sessions/recent-projects` | getRecentProjects | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:565](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:565) | 按原 handler 分支核对 |
| H168 | GET | `/api/sessions/repository-context` | getRepositoryContext | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:570](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:570) | 按原 handler 分支核对 |
| H169 | POST | `/api/sessions/repository-branch` | createRepositoryBranch | 子接口缺失（显式 501） | [desktop/src/api/sessions.ts:574](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:574) | `backend/internal/server/sessions.go:389` |
| H170 | GET | `/api/sessions/{sessionId}/slash-commands` | getSlashCommands | 子接口缺失（default 501） | [desktop/src/api/sessions.ts:580](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:580) | `backend/internal/server/sessions.go:430` |
| H171 | GET | `/api/sessions/{sessionId}/inspection` | getInspection | 子接口缺失（default 501） | [desktop/src/api/sessions.ts:592](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:592) | `backend/internal/server/sessions.go:430` |
| H172 | GET | `/api/sessions/{sessionId}/inspection` | getSessionUsage | 子接口缺失（default 501） | [desktop/src/api/sessions.ts:602](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:602) | `backend/internal/server/sessions.go:430` |
| H173 | GET | `/api/sessions/{sessionId}/workspace/status` | getWorkspaceStatus | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:609](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:609) | 按原 handler 分支核对 |
| H174 | GET | `/api/sessions/{sessionId}/workspace/tree` | getWorkspaceTree | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:613](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:613) | 按原 handler 分支核对 |
| H175 | GET | `/api/sessions/{sessionId}/workspace/search` | searchWorkspace | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:618](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:618) | 按原 handler 分支核对 |
| H176 | GET | `/api/sessions/{sessionId}/workspace/file` | getWorkspaceFile | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:622](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:622) | 按原 handler 分支核对 |
| H177 | GET | `/api/sessions/{sessionId}/workspace/raw` | getWorkspaceRaw | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:631](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:631) | 按原 handler 分支核对 |
| H178 | GET | `/api/sessions/{sessionId}/workspace/diff` | getWorkspaceDiff | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:638](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:638) | 按原 handler 分支核对 |
| H179 | GET | `/api/sessions/{sessionId}/turn-checkpoints` | getTurnCheckpoints | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:642](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:642) | 按原 handler 分支核对 |
| H180 | GET | `/api/sessions/{sessionId}/turn-checkpoints/diff` | getTurnCheckpointDiff | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:662](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:662) | 按原 handler 分支核对 |
| H181 | POST | `/api/sessions/{sessionId}/rewind` | rewind | 资源已路由；逐分支/行为待验收 | [desktop/src/api/sessions.ts:674](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sessions.ts:674) | 按原 handler 分支核对 |
| H190 | POST | `/api/sessions/{parentSessionId}/side-chats` | create | 子接口缺失（default 501） | [desktop/src/api/sideChats.ts:14](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sideChats.ts:14) | `backend/internal/server/sessions.go:430` |
| H191 | DELETE | `/api/sessions/{parentSessionId}/side-chats/{sideChatId}` | discard | 子接口缺失（default 501） | [desktop/src/api/sideChats.ts:17](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/sideChats.ts:17) | `backend/internal/server/sessions.go:430` |
| H194 | GET | `/api/sessions/{sessionId}/subagents/by-agent/{agentId}` | getRunByAgent | 部分：workflow 重建被拒绝 | [desktop/src/api/subagents.ts:78](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/subagents.ts:78) | `backend/internal/server/subagent_runs.go:202` |
| H195 | GET | `/api/sessions/{sessionId}/subagents/by-tool/{toolUseId}` | getRunByTool | 资源已路由；逐分支/行为待验收 | [desktop/src/api/subagents.ts:85](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/subagents.ts:85) | 按原 handler 分支核对 |
| H196 | POST | `/api/sessions/{sessionId}/subagents/by-tool/{toolUseId}/messages` | sendMessage | 子接口缺失（显式 501） | [desktop/src/api/subagents.ts:92](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/subagents.ts:92) | `backend/internal/server/subagent_runs.go:279` |
| H240 | GET | `/api/sessions/{sessionId}/workspace/watch` | response | 资源已路由；逐分支/行为待验收 | [desktop/src/lib/workspace/fileWatch.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/workspace/fileWatch.ts:31) | 按原 handler 分支核对 |

### settings · D29

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H182 | GET | `/api/settings/user` | getUser | 资源已路由；逐分支/行为待验收 | [desktop/src/api/settings.ts:25](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/settings.ts:25) | 按原 handler 分支核对 |
| H183 | PUT | `/api/settings/user` | updateUser | 资源已路由；逐分支/行为待验收 | [desktop/src/api/settings.ts:29](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/settings.ts:29) | 按原 handler 分支核对 |
| H184 | GET | `/api/settings/output-styles` | getOutputStyles | 资源已路由；逐分支/行为待验收 | [desktop/src/api/settings.ts:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/settings.ts:34) | 按原 handler 分支核对 |
| H185 | PUT | `/api/settings/output-style` | setOutputStyle | 资源已路由；逐分支/行为待验收 | [desktop/src/api/settings.ts:38](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/settings.ts:38) | 按原 handler 分支核对 |
| H188 | GET | `/api/settings/cli-launcher` | getCliLauncherStatus | 子接口缺失（显式 501） | [desktop/src/api/settings.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/settings.ts:58) | `backend/internal/server/settings.go:51` |
| H189 | POST | `/api/settings/session-cleanup` | cleanupSessions | 资源已路由；逐分支/行为待验收 | [desktop/src/api/settings.ts:67](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/settings.ts:67) | 按原 handler 分支核对 |

### skills · D02

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H038 | GET | `/api/skills/mentions` | list | 子接口缺失（501） | [desktop/src/api/composerReferences.ts:24](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/composerReferences.ts:24) | [backend/internal/server/skills.go](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/skills.go) |
| H192 | GET | `/api/skills` | list | 列表/详情已接；完整能力待验收 | [desktop/src/api/skills.ts:7](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/skills.ts:7) | [backend/internal/server/skills.go](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/skills.go) |
| H193 | GET | `/api/skills/detail` | detail | 列表/详情已接；完整能力待验收 | [desktop/src/api/skills.ts:17](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/skills.ts:17) | [backend/internal/server/skills.go](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/skills.go) |

### status · D29

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H239 | GET | `/api/status` | response | 资源已路由；逐分支/行为待验收 | [desktop/src/lib/desktopRuntime.ts:332](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopRuntime.ts:332) | 按原 handler 分支核对 |

### tasks · D07

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H032 | GET | `/api/tasks/lists` | listTaskLists | 资源已路由；逐分支/行为待验收 | [desktop/src/api/cliTasks.ts:11](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/cliTasks.ts:11) | 按原 handler 分支核对 |
| H033 | GET | `/api/tasks/lists/{taskListId}` | getTasksForList | 资源已路由；逐分支/行为待验收 | [desktop/src/api/cliTasks.ts:16](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/cliTasks.ts:16) | 按原 handler 分支核对 |
| H034 | GET | `/api/tasks/lists/{taskListId}/{taskId}` | getTask | 资源已路由；逐分支/行为待验收 | [desktop/src/api/cliTasks.ts:21](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/cliTasks.ts:21) | 按原 handler 分支核对 |
| H035 | POST | `/api/tasks/lists/{taskListId}/reset` | resetTaskList | 资源已路由；逐分支/行为待验收 | [desktop/src/api/cliTasks.ts:26](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/cliTasks.ts:26) | 按原 handler 分支核对 |
| H036 | GET | `/api/tasks` | listAll | 资源已路由；逐分支/行为待验收 | [desktop/src/api/cliTasks.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/cliTasks.ts:31) | 按原 handler 分支核对 |

### teams · D09

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H205 | GET | `/api/teams/session/{sessionId}/plan` | get | 整组缺失（501） | [desktop/src/api/teamPlans.ts:18](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/teamPlans.ts:18) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H206 | PATCH | `/api/teams/{plan.teamName}/plan` | save | 整组缺失（501） | [desktop/src/api/teamPlans.ts:21](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/teamPlans.ts:21) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H207 | POST | `/api/teams/{plan.teamName}/plan/{action}` | act | 整组缺失（501） | [desktop/src/api/teamPlans.ts:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/teamPlans.ts:34) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H208 | GET | `/api/teams` | list | 整组缺失（501） | [desktop/src/api/teams.ts:55](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/teams.ts:55) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H209 | GET | `/api/teams/{name}` | get | 整组缺失（501） | [desktop/src/api/teams.ts:59](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/teams.ts:59) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H210 | GET | `/api/teams/{name}/workbench` | getWorkbench | 整组缺失（501） | [desktop/src/api/teams.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/teams.ts:63) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H211 | GET | `/api/teams/session/{sessionId}/workbench` | getWorkbenchForSession | 整组缺失（501） | [desktop/src/api/teams.ts:77](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/teams.ts:77) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H212 | GET | `/api/teams/{teamName}/members/{agentId}/transcript` | getMemberTranscript | 整组缺失（501） | [desktop/src/api/teams.ts:99](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/teams.ts:99) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H213 | POST | `/api/teams/{teamName}/members/{agentId}/messages` | sendMemberMessage | 整组缺失（501） | [desktop/src/api/teams.ts:105](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/teams.ts:105) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H214 | DELETE | `/api/teams/{name}` | delete | 整组缺失（501） | [desktop/src/api/teams.ts:112](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/teams.ts:112) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### traces · D21

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H215 | GET | `/api/traces` | list | 整组缺失（501） | [desktop/src/api/traces.ts:19](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/traces.ts:19) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H216 | GET | `/api/traces/{sessionId}/revision` | getRevision | 整组缺失（501） | [desktop/src/api/traces.ts:27](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/traces.ts:27) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H217 | GET | `/api/traces/settings` | getSettings | 整组缺失（501） | [desktop/src/api/traces.ts:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/traces.ts:34) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H218 | PUT | `/api/traces/settings` | updateSettings | 整组缺失（501） | [desktop/src/api/traces.ts:38](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/traces.ts:38) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H219 | DELETE | `/api/traces/{sessionId}` | deleteSession | 整组缺失（501） | [desktop/src/api/traces.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/traces.ts:42) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### voice · D20

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H220 | GET | `/api/voice/catalog` | catalog | 整组缺失（501） | [desktop/src/api/voice.ts:84](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/voice.ts:84) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H221 | PUT | `/api/voice/preferences` | updatePreferences | 整组缺失（501） | [desktop/src/api/voice.ts:87](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/voice.ts:87) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H222 | POST | `/api/voice/providers/{providerId}/prepare` | prepare | 整组缺失（501） | [desktop/src/api/voice.ts:91](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/voice.ts:91) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H223 | POST | `/api/voice/providers/{providerId}/cancel` | cancelPrepare | 整组缺失（501） | [desktop/src/api/voice.ts:94](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/voice.ts:94) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H224 | GET | `/api/voice/providers/{providerId}/status` | providerStatus | 整组缺失（501） | [desktop/src/api/voice.ts:97](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/voice.ts:97) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H225 | DELETE | `/api/voice/providers/{providerId}/assets` | removeAssets | 整组缺失（501） | [desktop/src/api/voice.ts:101](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/voice.ts:101) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H226 | POST | `/api/voice/transcribe` | res | 整组缺失（501） | [desktop/src/api/voice.ts:112](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/voice.ts:112) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### workflows · D10

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H227 | GET | `/api/workflows` | list | 整组缺失（501） | [desktop/src/api/workflows.ts:20](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/workflows.ts:20) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H228 | GET | `/api/workflows/{name}` | get | 整组缺失（501） | [desktop/src/api/workflows.ts:25](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/workflows.ts:25) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H229 | GET | `/api/workflows/session-runs/{sessionId}` | sessionRuns | 整组缺失（501） | [desktop/src/api/workflows.ts:32](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/workflows.ts:32) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H230 | GET | `/api/workflows/runs` | listRuns | 整组缺失（501） | [desktop/src/api/workflows.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/workflows.ts:42) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H231 | GET | `/api/workflows/runs/{sessionId}/{runId}` | getRun | 整组缺失（501） | [desktop/src/api/workflows.ts:48](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/workflows.ts:48) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H232 | POST | `/api/workflows/validate` | validate | 整组缺失（501） | [desktop/src/api/workflows.ts:54](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/workflows.ts:54) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H233 | POST | `/api/workflows/save` | save | 整组缺失（501） | [desktop/src/api/workflows.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/workflows.ts:63) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |
| H234 | DELETE | `/api/workflows/{name}` | remove | 整组缺失（501） | [desktop/src/api/workflows.ts:72](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/api/workflows.ts:72) | [backend/internal/server/server.go:441](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:441) |

### 参数化图片/图标 · D25/D28

| ID | 方法 | 路径/参数 | 调用所在函数/变量 | 当前状态 | 前端入口 | Go/Source 证据 |
| --- | --- | --- | --- | --- | --- | --- |
| H235 | GET | `参数 iconPath` | blob | 参数化资源调用；跟踪调用者验收 | [desktop/src/components/composite/TargetIcon.tsx:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/components/composite/TargetIcon.tsx:34) | 按原 handler 分支核对 |
| H236 | GET | `参数 path` | blob | 参数化资源调用；跟踪调用者验收 | [desktop/src/lib/authedImage.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/authedImage.ts:22) | 按原 handler 分支核对 |
| H237 | GET | `参数 path` | blob | 参数化资源调用；跟踪调用者验收 | [desktop/src/lib/authedImage.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/authedImage.ts:22) | 按原 handler 分支核对 |

原始路径表达式、query、直接 import 消费者、动态方法表达式和资源分类保留在 JSON 的 frontendHttpCalls。直接消费者来自静态 import，只表示已找到的直接关系，不能冒称运行时调用图已经全部证明。

public-access 的通用 remoteRequest 是一个调用点，实际展开为 GET session、POST pair、POST claim；不把它重复算成三个调用点。两个 authedImage 记录位于同一行，是 bypassCache 条件的两个分支调用；TargetIcon 的 iconPath 是第三个参数化调用。

## 4. 专用 Server / transport 与前端表外接口

| 方法 | 路径 | 原版入口 | 待做内容 | 任务 |
| --- | --- | --- | --- | --- |
| GET | `/health` | Source index.ts:290；Go server.go:375 | 只证明进程就绪，独立于业务准备 | D33/D35 |
| GET | `/api/public-access[/status]` | src/server/publicAccess.ts:207 | Go 无原单独注册 | D30 |
| POST | `/api/public-access/enable, /disable, /pairing, /approve, /reject, /revoke` | src/server/publicAccess.ts:209–231 | Go 无原单独注册；Electron 管理器仍调用控制面 | D30 |
| PUT | `/api/public-access/origin` | src/server/publicAccess.ts:210 | Go 无原单独注册 | D30 |
| GET | `/api/public-access/session` | src/server/publicAccess.ts:260 | 远端 listener 的 session/cookie 信任边界缺失 | D30 |
| POST | `/api/public-access/pair, /claim` | src/server/publicAccess.ts:263–295 | 远端 listener 配对、审批、cookie/撤销缺失 | D30 |
| POST | `/api/session-collaboration/list, /read, /create, /send, /wait` | src/server/sessionCollaborationAuth.ts:1；src/server/index.ts:304 | 独立内部 session credential 入口；仅补普通 REST auth 不等价 | D12 |
| WS | `/sdk/:sessionId` | src/server/index.ts:444 | Go 无原 SDK 注册；与桌面 /ws/:id 不是同一协议 | D31/D32 |
| GET | `/callback` | src/server/index.ts:479 | Go 无原 Claude OAuth callback handler | D15 |
| GET | `/auth/callback, /callback/openai` | src/server/index.ts:483；src/services/openaiAuth/client.ts:17 | Go 无原 OpenAI OAuth callback handler | D16 |
| provider methods | `/proxy/v1/messages, /proxy/providers/:id/*` | src/server/proxy/handler.ts | Go Server 专用 proxy transport 显式拒绝 | D29 |
| GET/HEAD | `/preview-fs/:sessionId/*` | src/server/api/previewFs.ts | Go Server 专用 preview transport 显式拒绝 | D28 |
| GET/HEAD | `/local-file/*` | src/server/api/localFile.ts | Go 未注册原本地文件服务，普通 static 分派不能替代 | D28 |
| GET | `/api/status/diagnostics, /usage, /user` | src/server/api/status.ts:53 | Go status 子接口未实现；root 的业务字段也须对照 | D29 |
| GET | `/api/doctor[/report]` | src/server/api/doctor.ts:12 | 整组缺失；保留报告/保护跳过语义 | D23 |
| POST | `/api/doctor/repair` | src/server/api/doctor.ts:18 | 整组缺失；保留 targetIds/作用域与允许修复边界 | D23 |
| POST | `/api/sessions/:id/side-question` | src/server/api/sideQuestions.ts:47 | Go sessions default 501；与 side-chats 不同 | D27 |
| DELETE | `/api/sessions/:id/side-question/:questionId` | src/server/api/sideQuestions.ts:52 | Go sessions default 501；取消与主回合隔离 | D27 |
| GET | `/api/sessions/:id/trace/raw` | src/server/api/sessions.ts:215 | Go sessions default 501；原始 JSONL 下载 | D21/D27 |
| POST | `/api/sessions/:id/chat[/stop] / /api/conversations/:id[/stop]` | src/server/api/conversations.ts:61–80 | 原 REST 只维护 legacy queued/reset；真实流式执行走 WS | D11 |

此表是对调用点表的补充，不把每个别名或字符串命中混算独立接口。专用路径匹配及回调常量的原始行记录见 JSON.specialTransports；详细请求方法以原 handler 为准。

## 5. 全部 40 个原版 API handler 的方法、选择器与路由条件

包含 helper 文件。methods 是静态方法选择集合；case/if 也可能来自状态、模式或保护检查，不能把每个条件机械转换成新 endpoint。记录这些条件是为了保留子分支和原语义。无选择器记录不代表没有业务，仍需按导出的 handler 及其调用服务实现。

### src/server/api/activityStats.ts

[src/server/api/activityStats.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/activityStats.ts)

方法选择：`GET`。

导出入口：[src/server/api/activityStats.ts:48](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/activityStats.ts:48) `aggregateActivityStatsForMode`, [src/server/api/activityStats.ts:87](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/activityStats.ts:87) `handleActivityStatsApi`。

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/activityStats.ts:93](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/activityStats.ts:93) | `req.method !== 'GET'` |

### src/server/api/adapters.ts

[src/server/api/adapters.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts)

方法选择：`POST`, `GET`, `PUT`。

导出入口：[src/server/api/adapters.ts:87](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:87) `cleanupStaleWhatsAppLoginDirectories`, [src/server/api/adapters.ts:504](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:504) `handleAdaptersApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/adapters.ts:511](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:511) | `tail[0]` | `wechat` |
| [src/server/api/adapters.ts:514](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:514) | `tail[0]` | `whatsapp` |
| [src/server/api/adapters.ts:517](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:517) | `tail[0]` | `feishu` |
| [src/server/api/adapters.ts:520](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:520) | `tail[0]` | `wecom` |
| [src/server/api/adapters.ts:523](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:523) | `tail[0]` | `qq` |
| [src/server/api/adapters.ts:526](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:526) | `tail[0]` | `slack` |
| [src/server/api/adapters.ts:529](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:529) | `tail[0]` | `dingtalk` |
| [src/server/api/adapters.ts:529](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:529) | `tail[1]` | `unbind` |
| [src/server/api/adapters.ts:541](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:541) | `tail[0]` | `dingtalk` |
| [src/server/api/adapters.ts:541](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:541) | `tail[1]` | `registration` |
| [src/server/api/adapters.ts:542](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:542) | `tail[2]` | `begin` |
| [src/server/api/adapters.ts:545](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:545) | `tail[2]` | `poll` |
| [src/server/api/adapters.ts:597](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:597) | `tail[0]` | `registration` |
| [src/server/api/adapters.ts:597](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:597) | `tail[1]` | `begin` |
| [src/server/api/adapters.ts:608](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:608) | `tail[0]` | `registration` |
| [src/server/api/adapters.ts:608](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:608) | `tail[1]` | `poll` |
| [src/server/api/adapters.ts:636](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:636) | `tail[0]` | `registration` |
| [src/server/api/adapters.ts:636](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:636) | `tail[1]` | `cancel` |
| [src/server/api/adapters.ts:642](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:642) | `tail[0]` | `unbind` |
| [src/server/api/adapters.ts:662](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:662) | `tail[0]` | `login` |
| [src/server/api/adapters.ts:662](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:662) | `tail[1]` | `start` |
| [src/server/api/adapters.ts:670](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:670) | `tail[0]` | `login` |
| [src/server/api/adapters.ts:670](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:670) | `tail[1]` | `poll` |
| [src/server/api/adapters.ts:689](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:689) | `tail[0]` | `login` |
| [src/server/api/adapters.ts:689](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:689) | `tail[1]` | `cancel` |
| [src/server/api/adapters.ts:695](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:695) | `tail[0]` | `unbind` |
| [src/server/api/adapters.ts:712](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:712) | `tail[0]` | `login` |
| [src/server/api/adapters.ts:712](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:712) | `tail[1]` | `start` |
| [src/server/api/adapters.ts:720](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:720) | `tail[0]` | `login` |
| [src/server/api/adapters.ts:720](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:720) | `tail[1]` | `poll` |
| [src/server/api/adapters.ts:743](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:743) | `tail[0]` | `login` |
| [src/server/api/adapters.ts:743](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:743) | `tail[1]` | `cancel` |
| [src/server/api/adapters.ts:749](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:749) | `tail[0]` | `unbind` |
| [src/server/api/adapters.ts:769](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:769) | `tail[0]` | `manifest` |
| [src/server/api/adapters.ts:776](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:776) | `tail[0]` | `unbind` |
| [src/server/api/adapters.ts:792](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:792) | `tail[0]` | `login` |
| [src/server/api/adapters.ts:792](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:792) | `tail[1]` | `start` |
| [src/server/api/adapters.ts:797](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:797) | `tail[0]` | `login` |
| [src/server/api/adapters.ts:797](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:797) | `tail[1]` | `poll` |
| [src/server/api/adapters.ts:820](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:820) | `tail[0]` | `unbind` |
| [src/server/api/adapters.ts:838](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:838) | `tail[0]` | `login` |
| [src/server/api/adapters.ts:838](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:838) | `tail[1]` | `start` |
| [src/server/api/adapters.ts:866](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:866) | `tail[0]` | `login` |
| [src/server/api/adapters.ts:866](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:866) | `tail[1]` | `poll` |
| [src/server/api/adapters.ts:905](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:905) | `tail[0]` | `unbind` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/adapters.ts:511](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:511) | `tail[0] === 'wechat'` |
| [src/server/api/adapters.ts:514](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:514) | `tail[0] === 'whatsapp'` |
| [src/server/api/adapters.ts:517](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:517) | `tail[0] === 'feishu'` |
| [src/server/api/adapters.ts:520](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:520) | `tail[0] === 'wecom'` |
| [src/server/api/adapters.ts:523](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:523) | `tail[0] === 'qq'` |
| [src/server/api/adapters.ts:526](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:526) | `tail[0] === 'slack'` |
| [src/server/api/adapters.ts:529](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:529) | `tail[0] === 'dingtalk' && req.method === 'POST' && tail[1] === 'unbind'` |
| [src/server/api/adapters.ts:541](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:541) | `tail[0] === 'dingtalk' && tail[1] === 'registration'` |
| [src/server/api/adapters.ts:542](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:542) | `req.method === 'POST' && tail[2] === 'begin'` |
| [src/server/api/adapters.ts:545](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:545) | `req.method === 'POST' && tail[2] === 'poll'` |
| [src/server/api/adapters.ts:557](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:557) | `req.method === 'GET'` |
| [src/server/api/adapters.ts:562](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:562) | `req.method === 'PUT'` |
| [src/server/api/adapters.ts:597](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:597) | `req.method === 'POST' && tail[0] === 'registration' && tail[1] === 'begin'` |
| [src/server/api/adapters.ts:608](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:608) | `req.method === 'POST' && tail[0] === 'registration' && tail[1] === 'poll'` |
| [src/server/api/adapters.ts:636](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:636) | `req.method === 'POST' && tail[0] === 'registration' && tail[1] === 'cancel'` |
| [src/server/api/adapters.ts:642](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:642) | `req.method === 'POST' && tail[0] === 'unbind'` |
| [src/server/api/adapters.ts:662](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:662) | `req.method === 'POST' && tail[0] === 'login' && tail[1] === 'start'` |
| [src/server/api/adapters.ts:670](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:670) | `req.method === 'POST' && tail[0] === 'login' && tail[1] === 'poll'` |
| [src/server/api/adapters.ts:689](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:689) | `req.method === 'POST' && tail[0] === 'login' && tail[1] === 'cancel'` |
| [src/server/api/adapters.ts:695](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:695) | `req.method === 'POST' && tail[0] === 'unbind'` |
| [src/server/api/adapters.ts:712](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:712) | `req.method === 'POST' && tail[0] === 'login' && tail[1] === 'start'` |
| [src/server/api/adapters.ts:720](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:720) | `req.method === 'POST' && tail[0] === 'login' && tail[1] === 'poll'` |
| [src/server/api/adapters.ts:743](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:743) | `req.method === 'POST' && tail[0] === 'login' && tail[1] === 'cancel'` |
| [src/server/api/adapters.ts:749](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:749) | `req.method === 'POST' && tail[0] === 'unbind'` |
| [src/server/api/adapters.ts:769](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:769) | `req.method === 'GET' && tail[0] === 'manifest'` |
| [src/server/api/adapters.ts:776](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:776) | `req.method === 'POST' && tail[0] === 'unbind'` |
| [src/server/api/adapters.ts:792](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:792) | `req.method === 'POST' && tail[0] === 'login' && tail[1] === 'start'` |
| [src/server/api/adapters.ts:797](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:797) | `req.method === 'POST' && tail[0] === 'login' && tail[1] === 'poll'` |
| [src/server/api/adapters.ts:820](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:820) | `req.method === 'POST' && tail[0] === 'unbind'` |
| [src/server/api/adapters.ts:838](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:838) | `req.method === 'POST' && tail[0] === 'login' && tail[1] === 'start'` |
| [src/server/api/adapters.ts:866](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:866) | `req.method === 'POST' && tail[0] === 'login' && tail[1] === 'poll'` |
| [src/server/api/adapters.ts:905](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/adapters.ts:905) | `req.method === 'POST' && tail[0] === 'unbind'` |

### src/server/api/agents.ts

[src/server/api/agents.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts)

方法选择：`POST`, `GET`, `PUT`, `DELETE`。

导出入口：[src/server/api/agents.ts:59](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:59) `handleAgentsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/agents.ts:67](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:67) | `resource` | `tasks` |
| [src/server/api/agents.ts:151](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:151) | `segments[3]` | `override` |
| [src/server/api/agents.ts:246](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:246) | `sub` | `lists` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/agents.ts:67](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:67) | `resource === 'tasks'` |
| [src/server/api/agents.ts:88](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:88) | `method === 'POST' && agentName === 'reload'` |
| [src/server/api/agents.ts:90](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:90) | `!sessionId` |
| [src/server/api/agents.ts:100](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:100) | `method === 'GET' && !agentName` |
| [src/server/api/agents.ts:117](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:117) | `method === 'GET' && agentName` |
| [src/server/api/agents.ts:131](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:131) | `method === 'POST' && !agentName` |
| [src/server/api/agents.ts:151](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:151) | `segments[3] === 'override' && agentName` |
| [src/server/api/agents.ts:152](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:152) | `method === 'PUT'` |
| [src/server/api/agents.ts:165](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:165) | `method === 'DELETE'` |
| [src/server/api/agents.ts:185](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:185) | `method === 'PUT' && agentName` |
| [src/server/api/agents.ts:202](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:202) | `method === 'DELETE' && agentName` |
| [src/server/api/agents.ts:246](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:246) | `sub === 'lists'` |
| [src/server/api/agents.ts:250](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:250) | `req.method === 'POST' && taskListId && taskId === 'reset'` |
| [src/server/api/agents.ts:255](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:255) | `req.method !== 'GET'` |
| [src/server/api/agents.ts:281](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/agents.ts:281) | `req.method !== 'GET'` |

### src/server/api/computer-use-python.ts

[src/server/api/computer-use-python.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use-python.ts)

方法选择：由委派 handler / transport 处理，参见文件。

导出入口：[src/server/api/computer-use-python.ts:72](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use-python.ts:72) `isPythonVersionAtLeast`, [src/server/api/computer-use-python.ts:103](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use-python.ts:103) `detectPythonRuntime`。

### src/server/api/computer-use.ts

[src/server/api/computer-use.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts)

方法选择：`GET`, `POST`, `PUT`。

导出入口：[src/server/api/computer-use.ts:56](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:56) `MIN_MACOS_COMPUTER_USE_VERSION`, [src/server/api/computer-use.ts:144](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:144) `runPipInstallWithFallback`, [src/server/api/computer-use.ts:226](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:226) `isVersionAtLeast`, [src/server/api/computer-use.ts:247](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:247) `resolveComputerUseCapability`, [src/server/api/computer-use.ts:340](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:340) `checkCuHelperPermissions`, [src/server/api/computer-use.ts:446](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:446) `resolvePermissionCardCommandResult`, [src/server/api/computer-use.ts:485](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:485) `parsePermissionSnapshot`, [src/server/api/computer-use.ts:527](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:527) `checkStatus`, [src/server/api/computer-use.ts:669](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:669) `getUnsupportedComputerUsePlatformStep`, [src/server/api/computer-use.ts:680](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:680) `getUnsupportedPythonVersionStep`, [src/server/api/computer-use.ts:691](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:691) `installSetupDependencies`, [src/server/api/computer-use.ts:945](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:945) `parseComputerUseConfigPatch`, [src/server/api/computer-use.ts:1017](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1017) `openComputerUseSettings`, [src/server/api/computer-use.ts:1084](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1084) `resolveInstalledAppPath`, [src/server/api/computer-use.ts:1115](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1115) `__resetInstalledAppPathCacheForTests`, [src/server/api/computer-use.ts:1124](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1124) `handleComputerUseApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/computer-use.ts:1131](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1131) | `action` | `status` |
| [src/server/api/computer-use.ts:1136](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1136) | `action` | `setup` |
| [src/server/api/computer-use.ts:1142](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1142) | `action` | `apps` |
| [src/server/api/computer-use.ts:1152](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1152) | `action` | `app-icon` |
| [src/server/api/computer-use.ts:1181](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1181) | `action` | `authorized-apps` |
| [src/server/api/computer-use.ts:1199](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1199) | `action` | `authorized-apps` |
| [src/server/api/computer-use.ts:1242](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1242) | `action` | `open-settings` |
| [src/server/api/computer-use.ts:1289](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1289) | `action` | `permission-card` |
| [src/server/api/computer-use.ts:1289](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1289) | `action` | `open-permission-card` |
| [src/server/api/computer-use.ts:1296](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1296) | `action` | `request-access` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/computer-use.ts:1131](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1131) | `action === 'status' && req.method === 'GET'` |
| [src/server/api/computer-use.ts:1136](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1136) | `action === 'setup' && req.method === 'POST'` |
| [src/server/api/computer-use.ts:1142](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1142) | `action === 'apps' && req.method === 'GET'` |
| [src/server/api/computer-use.ts:1152](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1152) | `action === 'app-icon' && req.method === 'GET'` |
| [src/server/api/computer-use.ts:1181](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1181) | `action === 'authorized-apps' && req.method === 'GET'` |
| [src/server/api/computer-use.ts:1199](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1199) | `action === 'authorized-apps' && req.method === 'PUT'` |
| [src/server/api/computer-use.ts:1242](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1242) | `action === 'open-settings' && req.method === 'POST'` |
| [src/server/api/computer-use.ts:1288](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1288) | `(action === 'permission-card' &#124;&#124; action === 'open-permission-card') &&     req.method === 'POST'` |
| [src/server/api/computer-use.ts:1296](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/computer-use.ts:1296) | `action === 'request-access' && req.method === 'POST'` |

### src/server/api/connectors.ts

[src/server/api/connectors.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/connectors.ts)

方法选择：`GET`, `POST`。

导出入口：[src/server/api/connectors.ts:7](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/connectors.ts:7) `createConnectorsApi`, [src/server/api/connectors.ts:36](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/connectors.ts:36) `handleConnectorsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/connectors.ts:24](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/connectors.ts:24) | `typeof options.sessionId` | `string` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/connectors.ts:12](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/connectors.ts:12) | `segments.length > 4 &#124;&#124; (action && !actions.has(action))` |
| [src/server/api/connectors.ts:13](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/connectors.ts:13) | `req.method === 'GET' && !action` |
| [src/server/api/connectors.ts:17](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/connectors.ts:17) | `req.method !== 'POST' &#124;&#124; !id &#124;&#124; !action` |
| [src/server/api/connectors.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/connectors.ts:22) | `Object.keys(options).some(key => !['acknowledgeSharedCredentials', 'sessionId', 'configuration'].includes(key))` |
| [src/server/api/connectors.ts:24](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/connectors.ts:24) | `'sessionId' in options && (typeof options.sessionId !== 'string' &#124;&#124; !options.sessionId.trim() &#124;&#124; options.sessionId.length > 256)` |
| [src/server/api/connectors.ts:26](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/connectors.ts:26) | `!['prepare', 'authenticate'].includes(action)` |

### src/server/api/conversations.ts

[src/server/api/conversations.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts)

方法选择：`GET`, `POST`。

导出入口：[src/server/api/conversations.ts:26](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:26) `handleConversationsApi`, [src/server/api/conversations.ts:148](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:148) `setSessionChatState`, [src/server/api/conversations.ts:157](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:157) `getSessionChatState`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/conversations.ts:43](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:43) | `segments[1]` | `sessions` |
| [src/server/api/conversations.ts:61](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:61) | `subAction` | `status` |
| [src/server/api/conversations.ts:68](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:68) | `subAction` | `stop` |
| [src/server/api/conversations.ts:132](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:132) | `sessionStates.get(sessionId)` | `idle` |
| [src/server/api/conversations.ts:160](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:160) | `sessionStates.get(sessionId)` | `idle` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/conversations.ts:43](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:43) | `segments[1] === 'sessions'` |
| [src/server/api/conversations.ts:54](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:54) | `!sessionId` |
| [src/server/api/conversations.ts:61](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:61) | `subAction === 'status' && req.method === 'GET'` |
| [src/server/api/conversations.ts:68](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:68) | `subAction === 'stop' && req.method === 'POST'` |
| [src/server/api/conversations.ts:75](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:75) | `!subAction` |
| [src/server/api/conversations.ts:76](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/conversations.ts:76) | `req.method !== 'POST'` |

### src/server/api/desktop-ui.ts

[src/server/api/desktop-ui.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts)

方法选择：`GET`, `PUT`, `DELETE`。

导出入口：[src/server/api/desktop-ui.ts:21](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:21) `handleDesktopUiApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/desktop-ui.ts:32](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:32) | `sub` | `preferences` |
| [src/server/api/desktop-ui.ts:89](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:89) | `action` | `avatar` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/desktop-ui.ts:32](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:32) | `sub !== 'preferences'` |
| [src/server/api/desktop-ui.ts:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:37) | `req.method !== 'GET'` |
| [src/server/api/desktop-ui.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:42) | `req.method !== 'PUT'` |
| [src/server/api/desktop-ui.ts:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:51) | `req.method !== 'PUT'` |
| [src/server/api/desktop-ui.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:58) | `req.method === 'GET'` |
| [src/server/api/desktop-ui.ts:65](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:65) | `req.method !== 'PUT'` |
| [src/server/api/desktop-ui.ts:80](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:80) | `action === undefined` |
| [src/server/api/desktop-ui.ts:81](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:81) | `req.method !== 'PUT'` |
| [src/server/api/desktop-ui.ts:89](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:89) | `action === 'avatar'` |
| [src/server/api/desktop-ui.ts:90](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:90) | `req.method === 'GET'` |
| [src/server/api/desktop-ui.ts:103](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:103) | `req.method === 'PUT'` |
| [src/server/api/desktop-ui.ts:114](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/desktop-ui.ts:114) | `req.method === 'DELETE'` |

### src/server/api/diagnostics.ts

[src/server/api/diagnostics.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts)

方法选择：`DELETE`, `GET`, `POST`。

导出入口：[src/server/api/diagnostics.ts:18](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:18) `handleDiagnosticsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/diagnostics.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:31) | `action` | `status` |
| [src/server/api/diagnostics.ts:35](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:35) | `action` | `local-index` |
| [src/server/api/diagnostics.ts:40](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:40) | `action` | `local-index` |
| [src/server/api/diagnostics.ts:41](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:41) | `segments[3]` | `rebuild` |
| [src/server/api/diagnostics.ts:48](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:48) | `action` | `events` |
| [src/server/api/diagnostics.ts:54](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:54) | `action` | `issue-report` |
| [src/server/api/diagnostics.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:58) | `action` | `events` |
| [src/server/api/diagnostics.ts:68](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:68) | `typeof body.sessionId` | `string` |
| [src/server/api/diagnostics.ts:80](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:80) | `action` | `export` |
| [src/server/api/diagnostics.ts:84](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:84) | `action` | `open-log-dir` |
| [src/server/api/diagnostics.ts:89](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:89) | `action` | `(root)` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/diagnostics.ts:26](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:26) | `!action && req.method === 'DELETE'` |
| [src/server/api/diagnostics.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:31) | `action === 'status' && req.method === 'GET'` |
| [src/server/api/diagnostics.ts:35](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:35) | `action === 'local-index' && !segments[3] && req.method === 'GET'` |
| [src/server/api/diagnostics.ts:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:39) | `action === 'local-index' &&       segments[3] === 'rebuild' &&       !segments[4] &&       req.method === 'POST'` |
| [src/server/api/diagnostics.ts:48](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:48) | `action === 'events' && req.method === 'GET'` |
| [src/server/api/diagnostics.ts:54](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:54) | `action === 'issue-report' && req.method === 'GET'` |
| [src/server/api/diagnostics.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:58) | `action === 'events' && req.method === 'POST'` |
| [src/server/api/diagnostics.ts:80](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:80) | `action === 'export' && req.method === 'POST'` |
| [src/server/api/diagnostics.ts:84](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/diagnostics.ts:84) | `action === 'open-log-dir' && req.method === 'POST'` |

### src/server/api/doctor.ts

[src/server/api/doctor.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/doctor.ts)

方法选择：`GET`, `POST`。

导出入口：[src/server/api/doctor.ts:4](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/doctor.ts:4) `handleDoctorApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/doctor.ts:12](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/doctor.ts:12) | `sub` | `report` |
| [src/server/api/doctor.ts:18](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/doctor.ts:18) | `sub` | `repair` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/doctor.ts:12](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/doctor.ts:12) | `(req.method === 'GET' && !sub) &#124;&#124; (req.method === 'GET' && sub === 'report')` |
| [src/server/api/doctor.ts:18](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/doctor.ts:18) | `req.method === 'POST' && sub === 'repair'` |
| [src/server/api/doctor.ts:28](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/doctor.ts:28) | `!sub` |

### src/server/api/filesystem.ts

[src/server/api/filesystem.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/filesystem.ts)

方法选择：由委派 handler / transport 处理，参见文件。

导出入口：[src/server/api/filesystem.ts:84](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/filesystem.ts:84) `isAllowedFilesystemPath`, [src/server/api/filesystem.ts:105](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/filesystem.ts:105) `handleFilesystemRoute`, [src/server/api/filesystem.ts:257](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/filesystem.ts:257) `searchFilesystemEntries`, [src/server/api/filesystem.ts:350](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/filesystem.ts:350) `getProjectSearchFiles`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/filesystem.ts:106](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/filesystem.ts:106) | `pathname` | `/api/filesystem/browse` |
| [src/server/api/filesystem.ts:110](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/filesystem.ts:110) | `pathname` | `/api/filesystem/file` |

### src/server/api/h5-access.ts

[src/server/api/h5-access.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts)

方法选择：`GET`, `PUT`, `POST`。

导出入口：[src/server/api/h5-access.ts:36](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:36) `handleH5AccessApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/h5-access.ts:67](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:67) | `case` | `enable` |
| [src/server/api/h5-access.ts:73](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:73) | `case` | `disable` |
| [src/server/api/h5-access.ts:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:79) | `case` | `regenerate` |
| [src/server/api/h5-access.ts:85](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:85) | `case` | `verify` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/h5-access.ts:46](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:46) | `req.method === 'GET'` |
| [src/server/api/h5-access.ts:53](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:53) | `req.method === 'PUT'` |
| [src/server/api/h5-access.ts:68](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:68) | `req.method !== 'POST'` |
| [src/server/api/h5-access.ts:74](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:74) | `req.method !== 'POST'` |
| [src/server/api/h5-access.ts:80](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:80) | `req.method !== 'POST'` |
| [src/server/api/h5-access.ts:86](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/h5-access.ts:86) | `req.method !== 'POST'` |

### src/server/api/haha-grok-oauth.ts

[src/server/api/haha-grok-oauth.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-grok-oauth.ts)

方法选择：`POST`, `GET`, `DELETE`。

导出入口：[src/server/api/haha-grok-oauth.ts:7](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-grok-oauth.ts:7) `handleHahaGrokOAuthApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/haha-grok-oauth.ts:14](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-grok-oauth.ts:14) | `action` | `start` |
| [src/server/api/haha-grok-oauth.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-grok-oauth.ts:22) | `action` | `success` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/haha-grok-oauth.ts:14](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-grok-oauth.ts:14) | `action === 'start' && req.method === 'POST'` |
| [src/server/api/haha-grok-oauth.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-grok-oauth.ts:22) | `action === 'success' && req.method === 'GET'` |
| [src/server/api/haha-grok-oauth.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-grok-oauth.ts:31) | `action === undefined && req.method === 'GET'` |
| [src/server/api/haha-grok-oauth.ts:41](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-grok-oauth.ts:41) | `action === undefined && req.method === 'DELETE'` |

### src/server/api/haha-oauth.ts

[src/server/api/haha-oauth.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-oauth.ts)

方法选择：`POST`, `GET`, `DELETE`。

导出入口：[src/server/api/haha-oauth.ts:27](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-oauth.ts:27) `handleHahaOAuthApi`, [src/server/api/haha-oauth.ts:83](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-oauth.ts:83) `handleHahaOAuthCallback`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/haha-oauth.ts:35](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-oauth.ts:35) | `action` | `start` |
| [src/server/api/haha-oauth.ts:55](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-oauth.ts:55) | `action` | `callback` |
| [src/server/api/haha-oauth.ts:59](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-oauth.ts:59) | `action` | `status` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/haha-oauth.ts:35](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-oauth.ts:35) | `action === 'start' && req.method === 'POST'` |
| [src/server/api/haha-oauth.ts:55](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-oauth.ts:55) | `action === 'callback' && req.method === 'GET'` |
| [src/server/api/haha-oauth.ts:59](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-oauth.ts:59) | `(action === undefined &#124;&#124; action === 'status') && req.method === 'GET'` |
| [src/server/api/haha-oauth.ts:72](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-oauth.ts:72) | `action === undefined && req.method === 'DELETE'` |

### src/server/api/haha-openai-oauth.ts

[src/server/api/haha-openai-oauth.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-openai-oauth.ts)

方法选择：`POST`, `GET`, `DELETE`。

导出入口：[src/server/api/haha-openai-oauth.ts:26](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-openai-oauth.ts:26) `handleHahaOpenAIOAuthApi`, [src/server/api/haha-openai-oauth.ts:78](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-openai-oauth.ts:78) `handleHahaOpenAIOAuthCallback`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/haha-openai-oauth.ts:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-openai-oauth.ts:34) | `action` | `start` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/haha-openai-oauth.ts:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-openai-oauth.ts:34) | `action === 'start' && req.method === 'POST'` |
| [src/server/api/haha-openai-oauth.ts:54](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-openai-oauth.ts:54) | `action === undefined && req.method === 'GET'` |
| [src/server/api/haha-openai-oauth.ts:67](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/haha-openai-oauth.ts:67) | `action === undefined && req.method === 'DELETE'` |

### src/server/api/localFile.ts

[src/server/api/localFile.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/localFile.ts)

方法选择：由委派 handler / transport 处理，参见文件。

导出入口：[src/server/api/localFile.ts:33](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/localFile.ts:33) `reconstructAbsolutePath`, [src/server/api/localFile.ts:81](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/localFile.ts:81) `handleLocalFile`。

### src/server/api/macInstalledApps.ts

[src/server/api/macInstalledApps.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/macInstalledApps.ts)

方法选择：由委派 handler / transport 处理，参见文件。

导出入口：[src/server/api/macInstalledApps.ts:80](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/macInstalledApps.ts:80) `listInstalledMacApps`。

### src/server/api/market.ts

[src/server/api/market.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts)

方法选择：`GET`, `POST`。

导出入口：[src/server/api/market.ts:70](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:70) `handleMarketApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/market.ts:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:79) | `sub` | `skills` |
| [src/server/api/market.ts:98](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:98) | `sub` | `skills` |
| [src/server/api/market.ts:105](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:105) | `sub` | `skills` |
| [src/server/api/market.ts:105](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:105) | `segments[5]` | `file` |
| [src/server/api/market.ts:116](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:116) | `sub` | `status` |
| [src/server/api/market.ts:120](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:120) | `sub` | `install` |
| [src/server/api/market.ts:126](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:126) | `sub` | `uninstall` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/market.ts:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:79) | `method === 'GET' && sub === 'skills' && !segments[3]` |
| [src/server/api/market.ts:98](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:98) | `method === 'GET' && sub === 'skills' && segments[3] && segments[4] && !segments[5]` |
| [src/server/api/market.ts:105](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:105) | `method === 'GET' && sub === 'skills' && segments[3] && segments[4] && segments[5] === 'file'` |
| [src/server/api/market.ts:116](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:116) | `method === 'GET' && sub === 'status'` |
| [src/server/api/market.ts:120](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:120) | `method === 'POST' && sub === 'install'` |
| [src/server/api/market.ts:126](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/market.ts:126) | `method === 'POST' && sub === 'uninstall'` |

### src/server/api/mcp.ts

[src/server/api/mcp.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts)

方法选择：`POST`, `PUT`, `GET`, `DELETE`。

导出入口：[src/server/api/mcp.ts:750](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:750) `handleMcpApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/mcp.ts:266](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:266) | `case` | `connected` |
| [src/server/api/mcp.ts:268](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:268) | `case` | `needs-auth` |
| [src/server/api/mcp.ts:270](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:270) | `case` | `failed` |
| [src/server/api/mcp.ts:272](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:272) | `case` | `disabled` |
| [src/server/api/mcp.ts:274](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:274) | `case` | `checking` |
| [src/server/api/mcp.ts:774](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:774) | `action` | `status` |
| [src/server/api/mcp.ts:790](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:790) | `action` | `toggle` |
| [src/server/api/mcp.ts:794](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:794) | `action` | `reconnect` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/mcp.ts:127](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:127) | `!sessionId` |
| [src/server/api/mcp.ts:128](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:128) | `!conversationService.hasSession(sessionId)` |
| [src/server/api/mcp.ts:766](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:766) | `req.method === 'GET' && serverName === 'project-paths' && !action` |
| [src/server/api/mcp.ts:770](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:770) | `req.method === 'GET' && !serverName` |
| [src/server/api/mcp.ts:774](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:774) | `req.method === 'GET' && serverName && action === 'status'` |
| [src/server/api/mcp.ts:778](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:778) | `req.method === 'POST' && !serverName` |
| [src/server/api/mcp.ts:782](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:782) | `req.method === 'PUT' && serverName` |
| [src/server/api/mcp.ts:786](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:786) | `req.method === 'DELETE' && serverName && !action` |
| [src/server/api/mcp.ts:790](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:790) | `req.method === 'POST' && serverName && action === 'toggle'` |
| [src/server/api/mcp.ts:794](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/mcp.ts:794) | `req.method === 'POST' && serverName && action === 'reconnect'` |

### src/server/api/memory.ts

[src/server/api/memory.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/memory.ts)

方法选择：`GET`, `PUT`。

导出入口：[src/server/api/memory.ts:54](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/memory.ts:54) `handleMemoryApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/memory.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/memory.ts:63) | `case` | `projects` |
| [src/server/api/memory.ts:69](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/memory.ts:69) | `case` | `files` |
| [src/server/api/memory.ts:75](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/memory.ts:75) | `case` | `file` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/memory.ts:64](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/memory.ts:64) | `req.method !== 'GET'` |
| [src/server/api/memory.ts:70](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/memory.ts:70) | `req.method !== 'GET'` |
| [src/server/api/memory.ts:218](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/memory.ts:218) | `req.method === 'GET'` |
| [src/server/api/memory.ts:252](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/memory.ts:252) | `req.method === 'PUT'` |

### src/server/api/models.ts

[src/server/api/models.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/models.ts)

方法选择：`GET`, `PUT`。

导出入口：[src/server/api/models.ts:296](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/models.ts:296) `handleModelsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/models.ts:306](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/models.ts:306) | `resource` | `effort` |
| [src/server/api/models.ts:317](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/models.ts:317) | `case` | `current` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/models.ts:306](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/models.ts:306) | `resource === 'effort'` |
| [src/server/api/models.ts:314](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/models.ts:314) | `req.method !== 'GET'` |
| [src/server/api/models.ts:371](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/models.ts:371) | `req.method === 'GET'` |
| [src/server/api/models.ts:453](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/models.ts:453) | `req.method === 'PUT'` |
| [src/server/api/models.ts:495](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/models.ts:495) | `req.method === 'GET'` |
| [src/server/api/models.ts:501](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/models.ts:501) | `req.method === 'PUT'` |

### src/server/api/open-targets.ts

[src/server/api/open-targets.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts)

方法选择：`GET`, `POST`。

导出入口：[src/server/api/open-targets.ts:4](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts:4) `handleOpenTargetsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/open-targets.ts:23](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts:23) | `action` | `open` |
| [src/server/api/open-targets.ts:43](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts:43) | `action` | `icons` |
| [src/server/api/open-targets.ts:48](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts:48) | `typeof segments[3]` | `string` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/open-targets.ts:12](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts:12) | `!action` |
| [src/server/api/open-targets.ts:13](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts:13) | `req.method !== 'GET'` |
| [src/server/api/open-targets.ts:23](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts:23) | `action === 'open'` |
| [src/server/api/open-targets.ts:24](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts:24) | `req.method !== 'POST'` |
| [src/server/api/open-targets.ts:43](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts:43) | `action === 'icons'` |
| [src/server/api/open-targets.ts:44](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/open-targets.ts:44) | `req.method !== 'GET'` |

### src/server/api/plugins.ts

[src/server/api/plugins.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts)

方法选择：`GET`, `POST`。

导出入口：[src/server/api/plugins.ts:10](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:10) `handlePluginsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/plugins.ts:24](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:24) | `sub` | `detail` |
| [src/server/api/plugins.ts:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:34) | `sub` | `reload` |
| [src/server/api/plugins.ts:56](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:56) | `sub` | `uninstall` |
| [src/server/api/plugins.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:63) | `case` | `enable` |
| [src/server/api/plugins.ts:67](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:67) | `case` | `disable` |
| [src/server/api/plugins.ts:71](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:71) | `case` | `update` |
| [src/server/api/plugins.ts:77](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:77) | `case` | `uninstall` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/plugins.ts:20](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:20) | `method === 'GET' && !sub` |
| [src/server/api/plugins.ts:24](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:24) | `method === 'GET' && sub === 'detail'` |
| [src/server/api/plugins.ts:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:34) | `method === 'POST' && sub === 'reload'` |
| [src/server/api/plugins.ts:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:37) | `!sessionId` |
| [src/server/api/plugins.ts:47](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/plugins.ts:47) | `method === 'POST' && sub` |

### src/server/api/previewFs.ts

[src/server/api/previewFs.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/previewFs.ts)

方法选择：由委派 handler / transport 处理，参见文件。

导出入口：[src/server/api/previewFs.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/previewFs.ts:42) `contentTypeForPath`, [src/server/api/previewFs.ts:94](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/previewFs.ts:94) `parseRange`, [src/server/api/previewFs.ts:146](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/previewFs.ts:146) `handlePreviewFs`, [src/server/api/previewFs.ts:199](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/previewFs.ts:199) `rewritePreviewHtml`, [src/server/api/previewFs.ts:272](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/previewFs.ts:272) `serveFileWithRange`。

### src/server/api/providers.ts

[src/server/api/providers.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts)

方法选择：`POST`, `GET`, `PUT`, `DELETE`。

导出入口：[src/server/api/providers.ts:64](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:64) `handleProvidersApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/providers.ts:85](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:85) | `action` | `scan` |
| [src/server/api/providers.ts:89](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:89) | `action` | `import` |
| [src/server/api/providers.ts:93](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:93) | `action` | `` |
| [src/server/api/providers.ts:147](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:147) | `action` | `activate` |
| [src/server/api/providers.ts:154](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:154) | `action` | `models` |
| [src/server/api/providers.ts:171](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:171) | `action` | `test` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/providers.ts:74](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:74) | `id === 'test' && req.method === 'POST'` |
| [src/server/api/providers.ts:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:79) | `id === 'models' && req.method === 'POST'` |
| [src/server/api/providers.ts:85](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:85) | `action === 'scan'` |
| [src/server/api/providers.ts:86](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:86) | `req.method !== 'GET'` |
| [src/server/api/providers.ts:89](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:89) | `action === 'import'` |
| [src/server/api/providers.ts:90](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:90) | `req.method !== 'POST'` |
| [src/server/api/providers.ts:99](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:99) | `id === 'presets' && req.method === 'GET'` |
| [src/server/api/providers.ts:104](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:104) | `id === 'auth-status' && req.method === 'GET'` |
| [src/server/api/providers.ts:111](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:111) | `req.method === 'GET'` |
| [src/server/api/providers.ts:114](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:114) | `req.method === 'PUT'` |
| [src/server/api/providers.ts:123](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:123) | `id === 'official' && req.method === 'POST'` |
| [src/server/api/providers.ts:130](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:130) | `req.method !== 'PUT'` |
| [src/server/api/providers.ts:136](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:136) | `req.method === 'GET'` |
| [src/server/api/providers.ts:140](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:140) | `req.method === 'POST'` |
| [src/server/api/providers.ts:147](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:147) | `action === 'activate'` |
| [src/server/api/providers.ts:148](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:148) | `req.method !== 'POST'` |
| [src/server/api/providers.ts:154](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:154) | `action === 'models'` |
| [src/server/api/providers.ts:155](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:155) | `req.method === 'GET'` |
| [src/server/api/providers.ts:159](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:159) | `req.method === 'PUT'` |
| [src/server/api/providers.ts:171](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:171) | `action === 'test'` |
| [src/server/api/providers.ts:172](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:172) | `req.method !== 'POST'` |
| [src/server/api/providers.ts:213](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:213) | `req.method === 'GET'` |
| [src/server/api/providers.ts:217](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:217) | `req.method === 'PUT'` |
| [src/server/api/providers.ts:220](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/providers.ts:220) | `req.method === 'DELETE'` |

### src/server/api/scheduled-tasks.ts

[src/server/api/scheduled-tasks.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts)

方法选择：`GET`, `POST`, `PUT`, `DELETE`。

导出入口：[src/server/api/scheduled-tasks.ts:19](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:19) `handleScheduledTasksApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/scheduled-tasks.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:63) | `subResource` | `runs` |
| [src/server/api/scheduled-tasks.ts:121](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:121) | `subResource` | `run` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/scheduled-tasks.ts:30](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:30) | `method === 'GET' && taskId === 'runs' && subResource` |
| [src/server/api/scheduled-tasks.ts:36](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:36) | `method === 'GET' && taskId === 'runs'` |
| [src/server/api/scheduled-tasks.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:63) | `method === 'GET' && taskId && subResource === 'runs'` |
| [src/server/api/scheduled-tasks.ts:92](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:92) | `method === 'GET' && !taskId` |
| [src/server/api/scheduled-tasks.ts:98](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:98) | `method === 'POST' && !taskId` |
| [src/server/api/scheduled-tasks.ts:121](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:121) | `method === 'POST' && taskId && subResource === 'run'` |
| [src/server/api/scheduled-tasks.ts:134](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:134) | `method === 'PUT' && taskId && !subResource` |
| [src/server/api/scheduled-tasks.ts:141](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/scheduled-tasks.ts:141) | `method === 'DELETE' && taskId && !subResource` |

### src/server/api/search.ts

[src/server/api/search.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/search.ts)

方法选择：`POST`。

导出入口：[src/server/api/search.ts:13](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/search.ts:13) `handleSearchApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/search.ts:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/search.ts:37) | `sub` | `sessions` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/search.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/search.ts:22) | `method !== 'POST'` |
| [src/server/api/search.ts:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/search.ts:37) | `sub === 'sessions'` |
| [src/server/api/search.ts:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/search.ts:51) | `!sub` |

### src/server/api/sessionCollaboration.ts

[src/server/api/sessionCollaboration.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts)

方法选择：`POST`, `GET`。

导出入口：[src/server/api/sessionCollaboration.ts:18](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:18) `handleSessionCollaborationApi`, [src/server/api/sessionCollaboration.ts:53](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:53) `handleSessionCollaborationUiApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/sessionCollaboration.ts:28](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:28) | `case` | `list` |
| [src/server/api/sessionCollaboration.ts:29](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:29) | `case` | `read` |
| [src/server/api/sessionCollaboration.ts:33](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:33) | `case` | `create` |
| [src/server/api/sessionCollaboration.ts:41](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:41) | `case` | `send` |
| [src/server/api/sessionCollaboration.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:42) | `case` | `wait` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/sessionCollaboration.ts:20](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:20) | `req.method !== 'POST'` |
| [src/server/api/sessionCollaboration.ts:56](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:56) | `!parts.length && req.method === 'GET'` |
| [src/server/api/sessionCollaboration.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:58) | `!sessionId` |
| [src/server/api/sessionCollaboration.ts:59](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:59) | `parts.length === 2 && parts[1] === 'status' && req.method === 'GET'` |
| [src/server/api/sessionCollaboration.ts:60](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:60) | `parts.length === 2 && parts[1] === 'stop' && req.method === 'POST'` |
| [src/server/api/sessionCollaboration.ts:61](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessionCollaboration.ts:61) | `parts.length === 2 && parts[1] === 'resume' && req.method === 'POST'` |

### src/server/api/sessions.ts

[src/server/api/sessions.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts)

方法选择：`POST`, `GET`。

导出入口：[src/server/api/sessions.ts:106](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:106) `handleSessionsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/sessions.ts:121](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:121) | `case` | `GET` |
| [src/server/api/sessions.ts:123](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:123) | `case` | `POST` |
| [src/server/api/sessions.ts:134](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:134) | `sessionId` | `batch-delete` |
| [src/server/api/sessions.ts:145](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:145) | `sessionId` | `recent-projects` |
| [src/server/api/sessions.ts:149](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:149) | `sessionId` | `project-history` |
| [src/server/api/sessions.ts:165](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:165) | `sessionId` | `repository-context` |
| [src/server/api/sessions.ts:170](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:170) | `sessionId` | `repository-branch` |
| [src/server/api/sessions.ts:183](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:183) | `subResource` | `summary` |
| [src/server/api/sessions.ts:195](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:195) | `subResource` | `messages` |
| [src/server/api/sessions.ts:205](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:205) | `subResource` | `history-recovery` |
| [src/server/api/sessions.ts:209](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:209) | `subResource` | `trace` |
| [src/server/api/sessions.ts:216](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:216) | `segments[4]` | `raw` |
| [src/server/api/sessions.ts:225](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:225) | `segments[4]` | `calls` |
| [src/server/api/sessions.ts:230](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:230) | `subResource` | `git-info` |
| [src/server/api/sessions.ts:240](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:240) | `subResource` | `rewind` |
| [src/server/api/sessions.ts:250](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:250) | `subResource` | `branch` |
| [src/server/api/sessions.ts:260](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:260) | `subResource` | `side-chats` |
| [src/server/api/sessions.ts:265](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:265) | `subResource` | `side-question` |
| [src/server/api/sessions.ts:270](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:270) | `subResource` | `turn-checkpoints` |
| [src/server/api/sessions.ts:277](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:277) | `segments[4]` | `diff` |
| [src/server/api/sessions.ts:282](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:282) | `subResource` | `slash-commands` |
| [src/server/api/sessions.ts:292](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:292) | `subResource` | `inspection` |
| [src/server/api/sessions.ts:302](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:302) | `subResource` | `workspace` |
| [src/server/api/sessions.ts:312](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:312) | `subResource` | `review` |
| [src/server/api/sessions.ts:316](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:316) | `subResource` | `subagents` |
| [src/server/api/sessions.ts:319](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:319) | `segments[4]` | `by-agent` |
| [src/server/api/sessions.ts:342](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:342) | `segments[4]` | `by-tool` |
| [src/server/api/sessions.ts:345](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:345) | `segments[6]` | `messages` |
| [src/server/api/sessions.ts:349](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:349) | `segments[6]` | `messages` |
| [src/server/api/sessions.ts:404](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:404) | `subResource` | `chat` |
| [src/server/api/sessions.ts:418](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:418) | `case` | `GET` |
| [src/server/api/sessions.ts:420](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:420) | `case` | `DELETE` |
| [src/server/api/sessions.ts:422](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:422) | `case` | `PATCH` |
| [src/server/api/sessions.ts:593](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:593) | `case` | `watch` |
| [src/server/api/sessions.ts:595](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:595) | `case` | `status` |
| [src/server/api/sessions.ts:597](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:597) | `case` | `tree` |
| [src/server/api/sessions.ts:602](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:602) | `case` | `search` |
| [src/server/api/sessions.ts:604](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:604) | `case` | `file` |
| [src/server/api/sessions.ts:609](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:609) | `case` | `raw` |
| [src/server/api/sessions.ts:611](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:611) | `case` | `diff` |
| [src/server/api/sessions.ts:701](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:701) | `case` | `stage` |
| [src/server/api/sessions.ts:703](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:703) | `case` | `unstage` |
| [src/server/api/sessions.ts:765](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:765) | `case` | `unstaged` |
| [src/server/api/sessions.ts:766](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:766) | `case` | `staged` |
| [src/server/api/sessions.ts:767](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:767) | `case` | `uncommitted` |
| [src/server/api/sessions.ts:769](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:769) | `case` | `branch` |
| [src/server/api/sessions.ts:774](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:774) | `case` | `commit` |
| [src/server/api/sessions.ts:779](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:779) | `case` | `turn` |
| [src/server/api/sessions.ts:1040](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:1040) | `typeof sessionId` | `string` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/sessions.ts:119](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:119) | `!sessionId` |
| [src/server/api/sessions.ts:134](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:134) | `sessionId === 'batch-delete'` |
| [src/server/api/sessions.ts:135](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:135) | `req.method !== 'POST'` |
| [src/server/api/sessions.ts:145](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:145) | `sessionId === 'recent-projects' && req.method === 'GET'` |
| [src/server/api/sessions.ts:149](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:149) | `sessionId === 'project-history'` |
| [src/server/api/sessions.ts:150](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:150) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:165](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:165) | `sessionId === 'repository-context' && req.method === 'GET'` |
| [src/server/api/sessions.ts:170](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:170) | `sessionId === 'repository-branch'` |
| [src/server/api/sessions.ts:171](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:171) | `req.method !== 'POST'` |
| [src/server/api/sessions.ts:183](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:183) | `subResource === 'summary'` |
| [src/server/api/sessions.ts:184](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:184) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:195](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:195) | `subResource === 'messages'` |
| [src/server/api/sessions.ts:196](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:196) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:205](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:205) | `subResource === 'history-recovery' && req.method === 'GET'` |
| [src/server/api/sessions.ts:209](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:209) | `subResource === 'trace'` |
| [src/server/api/sessions.ts:210](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:210) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:216](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:216) | `segments[4] === 'raw'` |
| [src/server/api/sessions.ts:230](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:230) | `subResource === 'git-info'` |
| [src/server/api/sessions.ts:231](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:231) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:240](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:240) | `subResource === 'rewind'` |
| [src/server/api/sessions.ts:241](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:241) | `req.method !== 'POST'` |
| [src/server/api/sessions.ts:250](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:250) | `subResource === 'branch'` |
| [src/server/api/sessions.ts:251](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:251) | `req.method !== 'POST'` |
| [src/server/api/sessions.ts:260](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:260) | `subResource === 'side-chats'` |
| [src/server/api/sessions.ts:265](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:265) | `subResource === 'side-question'` |
| [src/server/api/sessions.ts:270](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:270) | `subResource === 'turn-checkpoints'` |
| [src/server/api/sessions.ts:271](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:271) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:282](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:282) | `subResource === 'slash-commands'` |
| [src/server/api/sessions.ts:283](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:283) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:292](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:292) | `subResource === 'inspection'` |
| [src/server/api/sessions.ts:293](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:293) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:302](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:302) | `subResource === 'workspace'` |
| [src/server/api/sessions.ts:303](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:303) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:312](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:312) | `subResource === 'review'` |
| [src/server/api/sessions.ts:316](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:316) | `subResource === 'subagents'` |
| [src/server/api/sessions.ts:319](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:319) | `segments[4] === 'by-agent' && segments[5] && segments.length === 6` |
| [src/server/api/sessions.ts:320](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:320) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:404](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:404) | `subResource === 'chat'` |
| [src/server/api/sessions.ts:638](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:638) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:645](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:645) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:650](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:650) | `req.method !== 'GET'` |
| [src/server/api/sessions.ts:667](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:667) | `req.method !== 'POST'` |
| [src/server/api/sessions.ts:1040](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sessions.ts:1040) | `typeof sessionId !== 'string' &#124;&#124; sessionId.trim().length === 0` |

### src/server/api/settings.ts

[src/server/api/settings.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts)

方法选择：`GET`, `PUT`, `POST`。

导出入口：[src/server/api/settings.ts:47](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:47) `handleSettingsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/settings.ts:57](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:57) | `resource` | `permissions` |
| [src/server/api/settings.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:58) | `sub` | `mode` |
| [src/server/api/settings.ts:73](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:73) | `case` | `user` |
| [src/server/api/settings.ts:76](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:76) | `case` | `project` |
| [src/server/api/settings.ts:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:79) | `case` | `output-styles` |
| [src/server/api/settings.ts:82](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:82) | `case` | `output-style` |
| [src/server/api/settings.ts:85](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:85) | `case` | `session-cleanup` |
| [src/server/api/settings.ts:88](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:88) | `case` | `cli-launcher` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/settings.ts:57](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:57) | `resource === 'permissions'` |
| [src/server/api/settings.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:58) | `sub === 'mode'` |
| [src/server/api/settings.ts:70](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:70) | `method !== 'GET'` |
| [src/server/api/settings.ts:89](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:89) | `method !== 'GET'` |
| [src/server/api/settings.ts:103](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:103) | `req.method === 'GET'` |
| [src/server/api/settings.ts:110](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:110) | `req.method === 'PUT'` |
| [src/server/api/settings.ts:126](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:126) | `req.method === 'GET'` |
| [src/server/api/settings.ts:130](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:130) | `req.method === 'PUT'` |
| [src/server/api/settings.ts:140](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:140) | `req.method !== 'GET'` |
| [src/server/api/settings.ts:168](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:168) | `req.method !== 'PUT'` |
| [src/server/api/settings.ts:214](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:214) | `req.method !== 'POST'` |
| [src/server/api/settings.ts:245](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:245) | `req.method === 'GET'` |
| [src/server/api/settings.ts:250](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/settings.ts:250) | `req.method === 'PUT'` |

### src/server/api/sideChats.ts

[src/server/api/sideChats.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sideChats.ts)

方法选择：`DELETE`, `POST`。

导出入口：[src/server/api/sideChats.ts:11](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sideChats.ts:11) `handleSideChatsRoute`。

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/sideChats.ts:13](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sideChats.ts:13) | `req.method === 'DELETE' && childId` |
| [src/server/api/sideChats.ts:20](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sideChats.ts:20) | `req.method !== 'POST' &#124;&#124; childId` |

### src/server/api/sideQuestions.ts

[src/server/api/sideQuestions.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sideQuestions.ts)

方法选择：`POST`, `DELETE`。

导出入口：[src/server/api/sideQuestions.ts:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sideQuestions.ts:39) `handleSideQuestionRoute`。

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/sideQuestions.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sideQuestions.ts:42) | `!conversationService.hasSession(sessionId)` |
| [src/server/api/sideQuestions.ts:47](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sideQuestions.ts:47) | `req.method === 'POST' && questionId === undefined` |
| [src/server/api/sideQuestions.ts:52](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sideQuestions.ts:52) | `req.method === 'DELETE' && questionId !== undefined` |
| [src/server/api/sideQuestions.ts:53](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/sideQuestions.ts:53) | `!z.uuid().safeParse(questionId).success` |

### src/server/api/skills.ts

[src/server/api/skills.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/skills.ts)

方法选择：`GET`。

导出入口：[src/server/api/skills.ts:425](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/skills.ts:425) `collectUserSkillNames`, [src/server/api/skills.ts:472](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/skills.ts:472) `listSkillSlashCommands`, [src/server/api/skills.ts:542](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/skills.ts:542) `handleSkillsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/skills.ts:557](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/skills.ts:557) | `case` | `mentions` |
| [src/server/api/skills.ts:563](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/skills.ts:563) | `case` | `detail` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/skills.ts:548](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/skills.ts:548) | `req.method !== 'GET'` |

### src/server/api/status.ts

[src/server/api/status.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/status.ts)

方法选择：`GET`。

导出入口：[src/server/api/status.ts:26](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/status.ts:26) `addUsage`, [src/server/api/status.ts:33](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/status.ts:33) `resetUsage`, [src/server/api/status.ts:41](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/status.ts:41) `handleStatusApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/status.ts:57](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/status.ts:57) | `case` | `diagnostics` |
| [src/server/api/status.ts:60](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/status.ts:60) | `case` | `usage` |
| [src/server/api/status.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/status.ts:63) | `case` | `user` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/status.ts:47](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/status.ts:47) | `req.method !== 'GET'` |

### src/server/api/teams.ts

[src/server/api/teams.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts)

方法选择：`GET`, `PATCH`, `POST`, `DELETE`。

导出入口：[src/server/api/teams.ts:18](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:18) `handleTeamsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/teams.ts:27](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:27) | `segments[4]` | `plan` |
| [src/server/api/teams.ts:30](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:30) | `segments[3]` | `plan` |
| [src/server/api/teams.ts:45](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:45) | `action` | `approve` |
| [src/server/api/teams.ts:46](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:46) | `action` | `return` |
| [src/server/api/teams.ts:46](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:46) | `action` | `cancel` |
| [src/server/api/teams.ts:46](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:46) | `action` | `retry` |
| [src/server/api/teams.ts:61](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:61) | `segments[4]` | `workbench` |
| [src/server/api/teams.ts:82](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:82) | `segments[3]` | `members` |
| [src/server/api/teams.ts:84](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:84) | `segments[5]` | `transcript` |
| [src/server/api/teams.ts:117](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:117) | `segments[3]` | `workbench` |
| [src/server/api/teams.ts:126](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:126) | `segments[3]` | `members` |
| [src/server/api/teams.ts:128](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:128) | `segments[5]` | `messages` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/teams.ts:27](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:27) | `method === 'GET' && teamName === 'session' && segments[3] && segments[4] === 'plan'` |
| [src/server/api/teams.ts:30](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:30) | `teamName && segments[3] === 'plan' && (method === 'PATCH' &#124;&#124; method === 'POST')` |
| [src/server/api/teams.ts:33](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:33) | `method === 'PATCH'` |
| [src/server/api/teams.ts:45](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:45) | `action === 'approve'` |
| [src/server/api/teams.ts:46](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:46) | `action === 'return' &#124;&#124; action === 'cancel' &#124;&#124; action === 'retry'` |
| [src/server/api/teams.ts:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:51) | `method === 'GET' && !teamName` |
| [src/server/api/teams.ts:57](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:57) | `method === 'GET' &&       teamName === 'session' &&       segments[3] &&       segments[4] === 'workbench'` |
| [src/server/api/teams.ts:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:79) | `method === 'GET' &&       teamName &&       segments[3] === 'members' &&       segments[4] &&       segments[5] === 'transcript'` |
| [src/server/api/teams.ts:117](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:117) | `method === 'GET' && teamName && segments[3] === 'workbench'` |
| [src/server/api/teams.ts:123](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:123) | `method === 'POST' &&       teamName &&       segments[3] === 'members' &&       segments[4] &&       segments[5] === 'messages'` |
| [src/server/api/teams.ts:143](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:143) | `method === 'GET' && teamName` |
| [src/server/api/teams.ts:149](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/teams.ts:149) | `method === 'DELETE' && teamName` |

### src/server/api/traces.ts

[src/server/api/traces.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts)

方法选择：`GET`, `PUT`, `DELETE`。

导出入口：[src/server/api/traces.ts:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:37) `handleTracesApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/traces.ts:50](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:50) | `case` | `settings` |
| [src/server/api/traces.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:63) | `segments[3]` | `revision` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/traces.ts:47](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:47) | `req.method !== 'GET'` |
| [src/server/api/traces.ts:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:51) | `req.method === 'GET'` |
| [src/server/api/traces.ts:54](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:54) | `req.method === 'PUT'` |
| [src/server/api/traces.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:63) | `segments.length === 4 && segments[3] === 'revision'` |
| [src/server/api/traces.ts:64](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:64) | `req.method !== 'GET'` |
| [src/server/api/traces.ts:69](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:69) | `req.method === 'DELETE' && segments.length === 3` |
| [src/server/api/traces.ts:81](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:81) | `!sessionId` |
| [src/server/api/traces.ts:103](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/traces.ts:103) | `!sessionId` |

### src/server/api/voice.ts

[src/server/api/voice.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts)

方法选择：由委派 handler / transport 处理，参见文件。

导出入口：[src/server/api/voice.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:22) `handleVoiceApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/voice.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:31) | `sub` | `catalog` |
| [src/server/api/voice.ts:36](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:36) | `sub` | `preferences` |
| [src/server/api/voice.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:42) | `sub` | `providers` |
| [src/server/api/voice.ts:47](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:47) | `action` | `status` |
| [src/server/api/voice.ts:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:51) | `action` | `prepare` |
| [src/server/api/voice.ts:55](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:55) | `action` | `cancel` |
| [src/server/api/voice.ts:59](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:59) | `action` | `assets` |
| [src/server/api/voice.ts:66](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:66) | `sub` | `transcribe` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/voice.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:31) | `sub === 'catalog'` |
| [src/server/api/voice.ts:36](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:36) | `sub === 'preferences'` |
| [src/server/api/voice.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:42) | `sub === 'providers'` |
| [src/server/api/voice.ts:45](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:45) | `!providerId &#124;&#124; !action` |
| [src/server/api/voice.ts:47](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:47) | `action === 'status'` |
| [src/server/api/voice.ts:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:51) | `action === 'prepare'` |
| [src/server/api/voice.ts:55](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:55) | `action === 'cancel'` |
| [src/server/api/voice.ts:59](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:59) | `action === 'assets'` |
| [src/server/api/voice.ts:66](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:66) | `sub === 'transcribe'` |
| [src/server/api/voice.ts:145](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/voice.ts:145) | `req.method !== method` |

### src/server/api/workflows.ts

[src/server/api/workflows.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts)

方法选择：`GET`, `POST`, `DELETE`。

导出入口：[src/server/api/workflows.ts:24](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:24) `handleWorkflowsApi`。

| 源码 | 选择字段 | 值 |
| --- | --- | --- |
| [src/server/api/workflows.ts:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:39) | `first` | `runs` |
| [src/server/api/workflows.ts:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:51) | `first` | `session-runs` |
| [src/server/api/workflows.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:58) | `first` | `runs` |
| [src/server/api/workflows.ts:66](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:66) | `first` | `validate` |
| [src/server/api/workflows.ts:74](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:74) | `first` | `save` |

| 源码 | if 条件（静态原文） |
| --- | --- |
| [src/server/api/workflows.ts:33](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:33) | `method === 'GET' && !first` |
| [src/server/api/workflows.ts:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:39) | `method === 'GET' && first === 'runs' && !segments[3]` |
| [src/server/api/workflows.ts:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:51) | `method === 'GET' && first === 'session-runs' && segments[3]` |
| [src/server/api/workflows.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:58) | `method === 'GET' && first === 'runs' && segments[3] && segments[4]` |
| [src/server/api/workflows.ts:66](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:66) | `method === 'POST' && first === 'validate'` |
| [src/server/api/workflows.ts:74](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:74) | `method === 'POST' && first === 'save'` |
| [src/server/api/workflows.ts:96](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:96) | `method === 'GET' && first` |
| [src/server/api/workflows.ts:101](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workflows.ts:101) | `method === 'DELETE' && first` |

### src/server/api/workspaceSearch.ts

[src/server/api/workspaceSearch.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workspaceSearch.ts)

方法选择：由委派 handler / transport 处理，参见文件。

导出入口：[src/server/api/workspaceSearch.ts:7](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workspaceSearch.ts:7) `handleWorkspaceSearchRoute`。

### src/server/api/workspaceWatch.ts

[src/server/api/workspaceWatch.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workspaceWatch.ts)

方法选择：由委派 handler / transport 处理，参见文件。

导出入口：[src/server/api/workspaceWatch.ts:4](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/api/workspaceWatch.ts:4) `handleWorkspaceWatchRoute`。

## 6. WS 全部 11 类客户端控制、30 类服务端消息、9 类子 Agent 流事件

### ClientMessage

| 类型 | 协议定义 | 原版处理 | 当前状态 | 任务 |
| --- | --- | --- | --- | --- |
| `prewarm_session` | [desktop/src/types/chat.ts:9](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:9) | [src/server/ws/handler.ts:734](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:734) | 初始化后直接返回；完整预热缺口 | D31 |
| `sync_state` | [desktop/src/types/chat.ts:10](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:10) | [src/server/ws/handler.ts:738](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:738) | 部分 snapshot；后台/团队/审批/问答等待补齐 | D31 |
| `user_message` | [desktop/src/types/chat.ts:11](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:11) | [src/server/ws/handler.ts:678](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:678) | 基础 text 有接线；attachments/sessionReferences 被拒绝 | D31 |
| `permission_response` | [desktop/src/types/chat.ts:13](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:13) | [src/server/ws/handler.ts:714](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:714) | 基础审批有接线；permissionUpdates/runtimeOverride/规则保存缺口 | D31 |
| `computer_use_permission_response` | [desktop/src/types/chat.ts:24](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:24) | [src/server/ws/handler.ts:722](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:722) | 显式未实现 | D31 |
| `set_permission_mode` | [desktop/src/types/chat.ts:28](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:28) | [src/server/ws/handler.ts:726](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:726) | 已有入口；原全部模式/规则待验收 | D31 |
| `set_runtime_config` | [desktop/src/types/chat.ts:29](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:29) | [src/server/ws/handler.ts:730](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:730) | 入口/具体修复已有；完整配置与重启恢复待验收 | D31 |
| `stop_generation` | [desktop/src/types/chat.ts:30](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:30) | [src/server/ws/handler.ts:750](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:750) | 基础取消有证据；复杂状态待完成 | D31 |
| `ask_user_question_activity` | [desktop/src/types/chat.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:31) | [src/server/ws/handler.ts:718](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:718) | 显式未实现 | D31 |
| `stop_background_task` | [desktop/src/types/chat.ts:32](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:32) | [src/server/ws/handler.ts:754](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:754) | 显式未实现 | D31 |
| `ping` | [desktop/src/types/chat.ts:33](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:33) | [src/server/ws/handler.ts:758](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/ws/handler.ts:758) | 入口存在；心跳/关闭策略待验收 | D31 |

### ServerMessage

| 类型 | 协议定义 | 待办 | 任务 |
| --- | --- | --- | --- |
| `connected` | [desktop/src/types/chat.ts:90](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:90) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `session_state` | [desktop/src/types/chat.ts:92](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:92) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `agent_run_event` | [desktop/src/types/chat.ts:97](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:97) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `content_start` | [desktop/src/types/chat.ts:104](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:104) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `content_delta` | [desktop/src/types/chat.ts:105](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:105) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `tool_use_complete` | [desktop/src/types/chat.ts:106](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:106) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `tool_result` | [desktop/src/types/chat.ts:107](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:107) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `permission_request` | [desktop/src/types/chat.ts:109](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:109) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `computer_use_permission_request` | [desktop/src/types/chat.ts:118](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:118) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `permission_resolved` | [desktop/src/types/chat.ts:123](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:123) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `permission_requests_snapshot` | [desktop/src/types/chat.ts:129](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:129) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `user_message_replay` | [desktop/src/types/chat.ts:134](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:134) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `message_complete` | [desktop/src/types/chat.ts:135](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:135) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `thinking` | [desktop/src/types/chat.ts:137](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:137) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `status` | [desktop/src/types/chat.ts:138](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:138) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `runtime_config_applied` | [desktop/src/types/chat.ts:140](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:140) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `permission_mode_changed` | [desktop/src/types/chat.ts:148](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:148) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `api_retry` | [desktop/src/types/chat.ts:150](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:150) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `streaming_fallback` | [desktop/src/types/chat.ts:159](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:159) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `error` | [desktop/src/types/chat.ts:160](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:160) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `background_task_stop_failed` | [desktop/src/types/chat.ts:161](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:161) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `system_notification` | [desktop/src/types/chat.ts:162](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:162) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `pong` | [desktop/src/types/chat.ts:163](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:163) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `team_update` | [desktop/src/types/chat.ts:164](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:164) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `team_plan_updated` | [desktop/src/types/chat.ts:165](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:165) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `team_created` | [desktop/src/types/chat.ts:166](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:166) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `team_workbench_updated` | [desktop/src/types/chat.ts:167](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:167) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `team_deleted` | [desktop/src/types/chat.ts:168](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:168) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `task_update` | [desktop/src/types/chat.ts:169](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:169) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `session_title_updated` | [desktop/src/types/chat.ts:170](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:170) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |

### AgentRunStreamMessage

| 类型 | 协议定义 | 待办 | 任务 |
| --- | --- | --- | --- |
| `content_start` | [desktop/src/types/chat.ts:173](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:173) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `content_delta` | [desktop/src/types/chat.ts:174](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:174) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `tool_use_complete` | [desktop/src/types/chat.ts:175](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:175) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `tool_result` | [desktop/src/types/chat.ts:176](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:176) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `thinking` | [desktop/src/types/chat.ts:177](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:177) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `status` | [desktop/src/types/chat.ts:178](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:178) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `api_retry` | [desktop/src/types/chat.ts:179](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:179) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `streaming_fallback` | [desktop/src/types/chat.ts:180](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:180) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |
| `error` | [desktop/src/types/chat.ts:181](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/types/chat.ts:181) | 补齐/核对实际生产者、字段、顺序、恢复与消费者；入口注册不代表完整实现 | D31/D35 |

ClientMessage 的可选字段以及 SDK wire/control 类型仍按原类型和运行时逐字段对照；此处类型名不是完整 schema。JSON.frontendWsSends 另有 18 个 send 对象静态命中，其中包括 preview bridge 消息，不能误称全部是 Agent WS 控制。

## 7. Native 89 个 IPC handler、14 个公开事件、1 个内部通道

### ELECTRON_IPC_CHANNELS

| 名称 | 通道 | 声明 | 注册 | 状态/任务 |
| --- | --- | --- | --- | --- |
| `publicAccessGetStatus` | `desktop:public-access:get-status` | [desktop/electron/ipc/channels.ts:2](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:2) | [desktop/electron/main.ts:538](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:538) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `publicAccessSaveCredential` | `desktop:public-access:save-credential` | [desktop/electron/ipc/channels.ts:3](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:3) | [desktop/electron/main.ts:539](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:539) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `publicAccessDeleteCredential` | `desktop:public-access:delete-credential` | [desktop/electron/ipc/channels.ts:4](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:4) | [desktop/electron/main.ts:540](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:540) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `publicAccessStart` | `desktop:public-access:start` | [desktop/electron/ipc/channels.ts:5](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:5) | [desktop/electron/main.ts:541](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:541) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `publicAccessStop` | `desktop:public-access:stop` | [desktop/electron/ipc/channels.ts:6](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:6) | [desktop/electron/main.ts:542](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:542) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `publicAccessSetAutoStart` | `desktop:public-access:set-auto-start` | [desktop/electron/ipc/channels.ts:7](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:7) | [desktop/electron/main.ts:543](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:543) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `appGetVersion` | `desktop:app:get-version` | [desktop/electron/ipc/channels.ts:8](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:8) | [desktop/electron/main.ts:544](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:544) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `appGetLocalePreference` | `desktop:app:get-locale-preference` | [desktop/electron/ipc/channels.ts:9](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:9) | [desktop/electron/main.ts:545](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:545) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `appSetLocalePreference` | `desktop:app:set-locale-preference` | [desktop/electron/ipc/channels.ts:10](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:10) | [desktop/electron/main.ts:549](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:549) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `appGetPreferredSystemLanguages` | `desktop:app:get-preferred-system-languages` | [desktop/electron/ipc/channels.ts:11](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:11) | [desktop/electron/main.ts:557](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:557) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `runtimeGetServerUrl` | `desktop:runtime:get-server-url` | [desktop/electron/ipc/channels.ts:12](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:12) | [desktop/electron/main.ts:561](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:561) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `runtimeGetLocalAccessToken` | `desktop:runtime:get-local-access-token` | [desktop/electron/ipc/channels.ts:13](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:13) | [desktop/electron/main.ts:562](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:562) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `runtimeGetPetAccessToken` | `desktop:runtime:get-pet-access-token` | [desktop/electron/ipc/channels.ts:14](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:14) | [desktop/electron/main.ts:566](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:566) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `commandInvoke` | `desktop:command:invoke` | [desktop/electron/ipc/channels.ts:15](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:15) | [desktop/electron/main.ts:570](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:570) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `clipboardReadText` | `desktop:clipboard:read-text` | [desktop/electron/ipc/channels.ts:16](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:16) | [desktop/electron/main.ts:571](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:571) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `clipboardWriteText` | `desktop:clipboard:write-text` | [desktop/electron/ipc/channels.ts:17](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:17) | [desktop/electron/main.ts:572](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:572) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `shellOpen` | `desktop:shell:open` | [desktop/electron/ipc/channels.ts:18](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:18) | [desktop/electron/main.ts:573](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:573) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `shellOpenPath` | `desktop:shell:open-path` | [desktop/electron/ipc/channels.ts:19](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:19) | [desktop/electron/main.ts:574](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:574) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `traceOpenWindow` | `desktop:trace:open-window` | [desktop/electron/ipc/channels.ts:20](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:20) | [desktop/electron/main.ts:575](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:575) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsList` | `desktop:pets:list` | [desktop/electron/ipc/channels.ts:21](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:21) | [desktop/electron/main.ts:576](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:576) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsCreateFromImage` | `desktop:pets:create-from-image` | [desktop/electron/ipc/channels.ts:22](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:22) | [desktop/electron/main.ts:577](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:577) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsCreateFromAtlas` | `desktop:pets:create-from-atlas` | [desktop/electron/ipc/channels.ts:23](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:23) | [desktop/electron/main.ts:605](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:605) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsPickSourceSheet` | `desktop:pets:pick-source-sheet` | [desktop/electron/ipc/channels.ts:24](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:24) | [desktop/electron/main.ts:633](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:633) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsCreateFromAtlasBytes` | `desktop:pets:create-from-atlas-bytes` | [desktop/electron/ipc/channels.ts:25](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:25) | [desktop/electron/main.ts:654](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:654) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsOpenFolder` | `desktop:pets:open-folder` | [desktop/electron/ipc/channels.ts:26](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:26) | [desktop/electron/main.ts:678](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:678) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsShow` | `desktop:pets:show` | [desktop/electron/ipc/channels.ts:27](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:27) | [desktop/electron/main.ts:682](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:682) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsHide` | `desktop:pets:hide` | [desktop/electron/ipc/channels.ts:28](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:28) | [desktop/electron/main.ts:686](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:686) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsShowContextMenu` | `desktop:pets:show-context-menu` | [desktop/electron/ipc/channels.ts:29](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:29) | [desktop/electron/main.ts:690](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:690) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsDragWindow` | `desktop:pets:drag-window` | [desktop/electron/ipc/channels.ts:30](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:30) | [desktop/electron/main.ts:698](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:698) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsSetIgnoreMouseEvents` | `desktop:pets:set-ignore-mouse-events` | [desktop/electron/ipc/channels.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:31) | [desktop/electron/main.ts:703](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:703) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsSetInteractiveRegions` | `desktop:pets:set-interactive-regions` | [desktop/electron/ipc/channels.ts:32](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:32) | [desktop/electron/main.ts:706](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:706) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsFocusMainWindow` | `desktop:pets:focus-main-window` | [desktop/electron/ipc/channels.ts:33](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:33) | [desktop/electron/main.ts:711](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:711) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petsFocusSession` | `desktop:pets:focus-session` | [desktop/electron/ipc/channels.ts:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:34) | [desktop/electron/main.ts:717](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:717) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `dialogOpen` | `desktop:dialog:open` | [desktop/electron/ipc/channels.ts:35](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:35) | [desktop/electron/main.ts:719](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:719) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `dialogSave` | `desktop:dialog:save` | [desktop/electron/ipc/channels.ts:36](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:36) | [desktop/electron/main.ts:721](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:721) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `updateCheck` | `desktop:update:check` | [desktop/electron/ipc/channels.ts:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:37) | [desktop/electron/main.ts:723](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:723) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `updateDownload` | `desktop:update:download` | [desktop/electron/ipc/channels.ts:38](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:38) | [desktop/electron/main.ts:725](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:725) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `updateInstall` | `desktop:update:install` | [desktop/electron/ipc/channels.ts:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:39) | [desktop/electron/main.ts:728](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:728) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `updatePrepareInstall` | `desktop:update:prepare-install` | [desktop/electron/ipc/channels.ts:40](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:40) | [desktop/electron/main.ts:729](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:729) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `updateCancelInstall` | `desktop:update:cancel-install` | [desktop/electron/ipc/channels.ts:41](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:41) | [desktop/electron/main.ts:730](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:730) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `updateRelaunch` | `desktop:update:relaunch` | [desktop/electron/ipc/channels.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:42) | [desktop/electron/main.ts:731](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:731) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `notificationPermissionState` | `desktop:notification:permission-state` | [desktop/electron/ipc/channels.ts:43](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:43) | [desktop/electron/main.ts:740](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:740) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `notificationRequestPermission` | `desktop:notification:request-permission` | [desktop/electron/ipc/channels.ts:44](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:44) | [desktop/electron/main.ts:741](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:741) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `notificationSend` | `desktop:notification:send` | [desktop/electron/ipc/channels.ts:45](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:45) | [desktop/electron/main.ts:742](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:742) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `notificationActionAck` | `desktop:notification:action-ack` | [desktop/electron/ipc/channels.ts:46](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:46) | [desktop/electron/main.ts:747](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:747) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `windowMinimize` | `desktop:window:minimize` | [desktop/electron/ipc/channels.ts:47](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:47) | [desktop/electron/main.ts:749](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:749) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `windowToggleMaximize` | `desktop:window:toggle-maximize` | [desktop/electron/ipc/channels.ts:48](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:48) | [desktop/electron/main.ts:750](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:750) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `windowClose` | `desktop:window:close` | [desktop/electron/ipc/channels.ts:49](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:49) | [desktop/electron/main.ts:755](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:755) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `windowStartDragging` | `desktop:window:start-dragging` | [desktop/electron/ipc/channels.ts:50](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:50) | [desktop/electron/main.ts:756](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:756) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `windowRequestAttention` | `desktop:window:request-attention` | [desktop/electron/ipc/channels.ts:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:51) | [desktop/electron/main.ts:757](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:757) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `windowFocus` | `desktop:window:focus` | [desktop/electron/ipc/channels.ts:52](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:52) | [desktop/electron/main.ts:758](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:758) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `windowIsMaximized` | `desktop:window:is-maximized` | [desktop/electron/ipc/channels.ts:53](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:53) | [desktop/electron/main.ts:759](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:759) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `terminalSpawn` | `desktop:terminal:spawn` | [desktop/electron/ipc/channels.ts:54](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:54) | [desktop/electron/main.ts:760](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:760) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `terminalWrite` | `desktop:terminal:write` | [desktop/electron/ipc/channels.ts:55](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:55) | [desktop/electron/main.ts:762](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:762) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `terminalResize` | `desktop:terminal:resize` | [desktop/electron/ipc/channels.ts:56](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:56) | [desktop/electron/main.ts:766](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:766) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `terminalKill` | `desktop:terminal:kill` | [desktop/electron/ipc/channels.ts:57](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:57) | [desktop/electron/main.ts:770](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:770) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `terminalGetBashPath` | `desktop:terminal:get-bash-path` | [desktop/electron/ipc/channels.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:58) | [desktop/electron/main.ts:774](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:774) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `terminalSetBashPath` | `desktop:terminal:set-bash-path` | [desktop/electron/ipc/channels.ts:59](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:59) | [desktop/electron/main.ts:775](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:775) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `previewOpen` | `desktop:preview:open` | [desktop/electron/ipc/channels.ts:60](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:60) | [desktop/electron/main.ts:776](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:776) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `previewNavigate` | `desktop:preview:navigate` | [desktop/electron/ipc/channels.ts:61](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:61) | [desktop/electron/main.ts:780](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:780) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `previewSetBounds` | `desktop:preview:set-bounds` | [desktop/electron/ipc/channels.ts:62](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:62) | [desktop/electron/main.ts:781](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:781) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `previewSetVisible` | `desktop:preview:set-visible` | [desktop/electron/ipc/channels.ts:63](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:63) | [desktop/electron/main.ts:782](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:782) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `previewSetZoom` | `desktop:preview:set-zoom` | [desktop/electron/ipc/channels.ts:64](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:64) | [desktop/electron/main.ts:783](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:783) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `previewClose` | `desktop:preview:close` | [desktop/electron/ipc/channels.ts:65](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:65) | [desktop/electron/main.ts:784](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:784) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `previewMessage` | `desktop:preview:message` | [desktop/electron/ipc/channels.ts:66](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:66) | [desktop/electron/main.ts:785](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:785) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserCreate` | `desktop:workspace-browser:create` | [desktop/electron/ipc/channels.ts:67](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:67) | [desktop/electron/main.ts:786](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:786) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserShowMenu` | `desktop:workspace-browser:show-menu` | [desktop/electron/ipc/channels.ts:68](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:68) | [desktop/electron/main.ts:802](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:802) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserNavigate` | `desktop:workspace-browser:navigate` | [desktop/electron/ipc/channels.ts:69](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:69) | [desktop/electron/main.ts:798](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:798) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserGoBack` | `desktop:workspace-browser:go-back` | [desktop/electron/ipc/channels.ts:70](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:70) | [desktop/electron/main.ts:811](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:811) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserGoForward` | `desktop:workspace-browser:go-forward` | [desktop/electron/ipc/channels.ts:71](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:71) | [desktop/electron/main.ts:813](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:813) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserReload` | `desktop:workspace-browser:reload` | [desktop/electron/ipc/channels.ts:72](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:72) | [desktop/electron/main.ts:815](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:815) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserStop` | `desktop:workspace-browser:stop` | [desktop/electron/ipc/channels.ts:73](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:73) | [desktop/electron/main.ts:819](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:819) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserSetBounds` | `desktop:workspace-browser:set-bounds` | [desktop/electron/ipc/channels.ts:74](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:74) | [desktop/electron/main.ts:821](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:821) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserSetVisible` | `desktop:workspace-browser:set-visible` | [desktop/electron/ipc/channels.ts:75](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:75) | [desktop/electron/main.ts:825](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:825) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserSetZoom` | `desktop:workspace-browser:set-zoom` | [desktop/electron/ipc/channels.ts:76](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:76) | [desktop/electron/main.ts:829](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:829) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserFind` | `desktop:workspace-browser:find` | [desktop/electron/ipc/channels.ts:77](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:77) | [desktop/electron/main.ts:833](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:833) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserStopFind` | `desktop:workspace-browser:stop-find` | [desktop/electron/ipc/channels.ts:78](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:78) | [desktop/electron/main.ts:841](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:841) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserCapture` | `desktop:workspace-browser:capture` | [desktop/electron/ipc/channels.ts:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:79) | [desktop/electron/main.ts:843](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:843) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserSnapshot` | `desktop:workspace-browser:snapshot` | [desktop/electron/ipc/channels.ts:80](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:80) | [desktop/electron/main.ts:847](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:847) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserMessage` | `desktop:workspace-browser:message` | [desktop/electron/ipc/channels.ts:81](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:81) | [desktop/electron/main.ts:849](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:849) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserPrintToPdf` | `desktop:workspace-browser:print-to-pdf` | [desktop/electron/ipc/channels.ts:82](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:82) | [desktop/electron/main.ts:853](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:853) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserClose` | `desktop:workspace-browser:close` | [desktop/electron/ipc/channels.ts:83](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:83) | [desktop/electron/main.ts:855](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:855) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `appModeGet` | `desktop:app-mode:get` | [desktop/electron/ipc/channels.ts:84](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:84) | [desktop/electron/main.ts:857](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:857) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `appModeSet` | `desktop:app-mode:set` | [desktop/electron/ipc/channels.ts:85](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:85) | [desktop/electron/main.ts:858](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:858) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `appModePrepareRestart` | `desktop:app-mode:prepare-restart` | [desktop/electron/ipc/channels.ts:86](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:86) | [desktop/electron/main.ts:859](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:859) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `appModeRestart` | `desktop:app-mode:restart` | [desktop/electron/ipc/channels.ts:87](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:87) | [desktop/electron/main.ts:860](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:860) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `adaptersRestartSidecar` | `desktop:adapters:restart-sidecar` | [desktop/electron/ipc/channels.ts:88](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:88) | [desktop/electron/main.ts:865](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:865) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `zoomSet` | `desktop:zoom:set` | [desktop/electron/ipc/channels.ts:89](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:89) | [desktop/electron/main.ts:866](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:866) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `appearanceSetApplied` | `desktop:appearance:set-applied` | [desktop/electron/ipc/channels.ts:90](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:90) | [desktop/electron/main.ts:867](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/main.ts:867) | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |

### ELECTRON_EVENT_CHANNELS

| 名称 | 通道 | 声明 | 注册 | 状态/任务 |
| --- | --- | --- | --- | --- |
| `event` | `desktop:event` | [desktop/electron/ipc/channels.ts:94](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:94) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `appLocaleChanged` | `desktop:app:locale-changed` | [desktop/electron/ipc/channels.ts:95](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:95) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `webviewDragDrop` | `desktop:webview:drag-drop` | [desktop/electron/ipc/channels.ts:96](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:96) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `notificationAction` | `desktop:notification:action` | [desktop/electron/ipc/channels.ts:97](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:97) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `updateDownloadEvent` | `desktop:update:download-event` | [desktop/electron/ipc/channels.ts:98](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:98) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `windowResized` | `desktop:window:resized` | [desktop/electron/ipc/channels.ts:99](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:99) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `nativeMenuNavigate` | `desktop:window:native-menu-navigate` | [desktop/electron/ipc/channels.ts:100](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:100) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `terminalOutput` | `desktop:terminal:output` | [desktop/electron/ipc/channels.ts:101](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:101) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `terminalExit` | `desktop:terminal:exit` | [desktop/electron/ipc/channels.ts:102](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:102) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `previewEvent` | `desktop:preview:event` | [desktop/electron/ipc/channels.ts:103](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:103) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `workspaceBrowserEvent` | `desktop:workspace-browser:event` | [desktop/electron/ipc/channels.ts:104](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:104) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petNavigateSession` | `desktop:pets:navigate-session` | [desktop/electron/ipc/channels.ts:105](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:105) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petVisibilityChanged` | `desktop:pets:visibility-changed` | [desktop/electron/ipc/channels.ts:106](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:106) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |
| `petPanelPlacementChanged` | `desktop:pets:panel-placement-changed` | [desktop/electron/ipc/channels.ts:107](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:107) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |

### ELECTRON_INTERNAL_CHANNELS

| 名称 | 通道 | 声明 | 注册 | 状态/任务 |
| --- | --- | --- | --- | --- |
| `previewMessageFromView` | `desktop:preview:message-from-view` | [desktop/electron/ipc/channels.ts:111](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/electron/ipc/channels.ts:111) | 事件/内部通道：保留消费者和生产者联调 | 原 handler 保留；补 Go 依赖与实际整包验收（D33） |

上述普通 IPC 的 89 个声明均能对应主进程注册；它们不是 89 个“Go 缺失接口”。PublicAccess、Terminal、Preview、Notifications、IM、更新等仍需验证其 Go 业务依赖。事件与内部通道不以 registerHandler 判断。

## 8. 全部 103 处 native bridge 调用及 7 处旧通知命令

| ID | 前端位置 | 调用所在函数/变量 | 方式 | 通道 | 工作包 |
| --- | --- | --- | --- | --- | --- |
| N001 | [desktop/src/lib/desktopHost/electronHost.ts:50](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:50) | `unlisten` | `subscribe` | `ELECTRON_EVENT_CHANNELS.updateDownloadEvent` | D33 |
| N002 | [desktop/src/lib/desktopHost/electronHost.ts:53](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:53) | `download` | `invoke` | `ELECTRON_IPC_CHANNELS.updateDownload` | D33 |
| N003 | [desktop/src/lib/desktopHost/electronHost.ts:58](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:58) | `install` | `invoke` | `ELECTRON_IPC_CHANNELS.updateInstall` | D33 |
| N004 | [desktop/src/lib/desktopHost/electronHost.ts:59](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:59) | `close` | `invoke` | `ELECTRON_IPC_CHANNELS.updateCancelInstall` | D33 |
| N005 | [desktop/src/lib/desktopHost/electronHost.ts:64](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:64) | `getStatus` | `invoke` | `ELECTRON_IPC_CHANNELS.publicAccessGetStatus` | D33 |
| N006 | [desktop/src/lib/desktopHost/electronHost.ts:65](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:65) | `saveCredential` | `invoke` | `ELECTRON_IPC_CHANNELS.publicAccessSaveCredential` | D33 |
| N007 | [desktop/src/lib/desktopHost/electronHost.ts:66](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:66) | `deleteCredential` | `invoke` | `ELECTRON_IPC_CHANNELS.publicAccessDeleteCredential` | D33 |
| N008 | [desktop/src/lib/desktopHost/electronHost.ts:67](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:67) | `start` | `invoke` | `ELECTRON_IPC_CHANNELS.publicAccessStart` | D33 |
| N009 | [desktop/src/lib/desktopHost/electronHost.ts:68](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:68) | `stop` | `invoke` | `ELECTRON_IPC_CHANNELS.publicAccessStop` | D33 |
| N010 | [desktop/src/lib/desktopHost/electronHost.ts:69](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:69) | `setAutoStart` | `invoke` | `ELECTRON_IPC_CHANNELS.publicAccessSetAutoStart` | D33 |
| N011 | [desktop/src/lib/desktopHost/electronHost.ts:87](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:87) | `getServerUrl` | `invoke` | `ELECTRON_IPC_CHANNELS.runtimeGetServerUrl` | D33 |
| N012 | [desktop/src/lib/desktopHost/electronHost.ts:88](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:88) | `getLocalAccessToken` | `invoke` | `ELECTRON_IPC_CHANNELS.runtimeGetLocalAccessToken` | D33 |
| N013 | [desktop/src/lib/desktopHost/electronHost.ts:91](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:91) | `getVersion` | `invoke` | `ELECTRON_IPC_CHANNELS.appGetVersion` | D33 |
| N014 | [desktop/src/lib/desktopHost/electronHost.ts:92](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:92) | `getLocalePreference` | `invoke` | `ELECTRON_IPC_CHANNELS.appGetLocalePreference` | D33 |
| N015 | [desktop/src/lib/desktopHost/electronHost.ts:93](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:93) | `setLocalePreference` | `invoke` | `ELECTRON_IPC_CHANNELS.appSetLocalePreference` | D33 |
| N016 | [desktop/src/lib/desktopHost/electronHost.ts:94](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:94) | `getPreferredSystemLanguages` | `invoke` | `ELECTRON_IPC_CHANNELS.appGetPreferredSystemLanguages` | D33 |
| N017 | [desktop/src/lib/desktopHost/electronHost.ts:95](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:95) | `onLocaleChanged` | `subscribe` | `ELECTRON_EVENT_CHANNELS.appLocaleChanged` | D33 |
| N018 | [desktop/src/lib/desktopHost/electronHost.ts:98](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:98) | `invoke` | `invoke` | `ELECTRON_IPC_CHANNELS.commandInvoke` | D33 |
| N019 | [desktop/src/lib/desktopHost/electronHost.ts:101](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:101) | `readText` | `invoke` | `ELECTRON_IPC_CHANNELS.clipboardReadText` | D33 |
| N020 | [desktop/src/lib/desktopHost/electronHost.ts:102](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:102) | `writeText` | `invoke` | `ELECTRON_IPC_CHANNELS.clipboardWriteText` | D33 |
| N021 | [desktop/src/lib/desktopHost/electronHost.ts:113](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:113) | `listen` | `subscribe` | `ELECTRON_EVENT_CHANNELS.event` | D33 |
| N022 | [desktop/src/lib/desktopHost/electronHost.ts:116](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:116) | `onDragDropEvent` | `subscribe` | `ELECTRON_EVENT_CHANNELS.webviewDragDrop` | D33 |
| N023 | [desktop/src/lib/desktopHost/electronHost.ts:119](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:119) | `open` | `invoke` | `ELECTRON_IPC_CHANNELS.shellOpen` | D33 |
| N024 | [desktop/src/lib/desktopHost/electronHost.ts:120](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:120) | `openPath` | `invoke` | `ELECTRON_IPC_CHANNELS.shellOpenPath` | D33 |
| N025 | [desktop/src/lib/desktopHost/electronHost.ts:123](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:123) | `openWindow` | `invoke` | `ELECTRON_IPC_CHANNELS.traceOpenWindow` | D33 |
| N026 | [desktop/src/lib/desktopHost/electronHost.ts:126](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:126) | `list` | `invoke` | `ELECTRON_IPC_CHANNELS.petsList` | D33 |
| N027 | [desktop/src/lib/desktopHost/electronHost.ts:127](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:127) | `createFromImage` | `invoke` | `ELECTRON_IPC_CHANNELS.petsCreateFromImage` | D33 |
| N028 | [desktop/src/lib/desktopHost/electronHost.ts:128](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:128) | `createFromAtlas` | `invoke` | `ELECTRON_IPC_CHANNELS.petsCreateFromAtlas` | D33 |
| N029 | [desktop/src/lib/desktopHost/electronHost.ts:129](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:129) | `pickSourceSheet` | `invoke` | `ELECTRON_IPC_CHANNELS.petsPickSourceSheet` | D33 |
| N030 | [desktop/src/lib/desktopHost/electronHost.ts:130](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:130) | `createFromAtlasBytes` | `invoke` | `ELECTRON_IPC_CHANNELS.petsCreateFromAtlasBytes` | D33 |
| N031 | [desktop/src/lib/desktopHost/electronHost.ts:131](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:131) | `openFolder` | `invoke` | `ELECTRON_IPC_CHANNELS.petsOpenFolder` | D33 |
| N032 | [desktop/src/lib/desktopHost/electronHost.ts:132](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:132) | `show` | `invoke` | `ELECTRON_IPC_CHANNELS.petsShow` | D33 |
| N033 | [desktop/src/lib/desktopHost/electronHost.ts:133](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:133) | `hide` | `invoke` | `ELECTRON_IPC_CHANNELS.petsHide` | D33 |
| N034 | [desktop/src/lib/desktopHost/electronHost.ts:134](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:134) | `showContextMenu` | `invoke` | `ELECTRON_IPC_CHANNELS.petsShowContextMenu` | D33 |
| N035 | [desktop/src/lib/desktopHost/electronHost.ts:138](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:138) | `dragWindow` | `invoke` | `ELECTRON_IPC_CHANNELS.petsDragWindow` | D33 |
| N036 | [desktop/src/lib/desktopHost/electronHost.ts:139](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:139) | `setIgnoreMouseEvents` | `invoke` | `ELECTRON_IPC_CHANNELS.petsSetIgnoreMouseEvents` | D33 |
| N037 | [desktop/src/lib/desktopHost/electronHost.ts:140](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:140) | `setInteractiveRegions` | `invoke` | `ELECTRON_IPC_CHANNELS.petsSetInteractiveRegions` | D33 |
| N038 | [desktop/src/lib/desktopHost/electronHost.ts:141](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:141) | `focusMainWindow` | `invoke` | `ELECTRON_IPC_CHANNELS.petsFocusMainWindow` | D33 |
| N039 | [desktop/src/lib/desktopHost/electronHost.ts:142](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:142) | `focusSession` | `invoke` | `ELECTRON_IPC_CHANNELS.petsFocusSession` | D33 |
| N040 | [desktop/src/lib/desktopHost/electronHost.ts:143](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:143) | `onNavigateSession` | `subscribe` | `ELECTRON_EVENT_CHANNELS.petNavigateSession` | D33 |
| N041 | [desktop/src/lib/desktopHost/electronHost.ts:144](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:144) | `onVisibilityChanged` | `subscribe` | `ELECTRON_EVENT_CHANNELS.petVisibilityChanged` | D33 |
| N042 | [desktop/src/lib/desktopHost/electronHost.ts:146](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:146) | `onPanelPlacementChanged` | `subscribe` | `ELECTRON_EVENT_CHANNELS.petPanelPlacementChanged` | D33 |
| N043 | [desktop/src/lib/desktopHost/electronHost.ts:149](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:149) | `open` | `invoke` | `ELECTRON_IPC_CHANNELS.dialogOpen` | D33 |
| N044 | [desktop/src/lib/desktopHost/electronHost.ts:150](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:150) | `save` | `invoke` | `ELECTRON_IPC_CHANNELS.dialogSave` | D33 |
| N045 | [desktop/src/lib/desktopHost/electronHost.ts:154](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:154) | `update` | `invoke` | `ELECTRON_IPC_CHANNELS.updateCheck` | D33 |
| N046 | [desktop/src/lib/desktopHost/electronHost.ts:157](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:157) | `prepareInstall` | `invoke` | `ELECTRON_IPC_CHANNELS.updatePrepareInstall` | D33 |
| N047 | [desktop/src/lib/desktopHost/electronHost.ts:158](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:158) | `cancelInstall` | `invoke` | `ELECTRON_IPC_CHANNELS.updateCancelInstall` | D33 |
| N048 | [desktop/src/lib/desktopHost/electronHost.ts:159](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:159) | `relaunch` | `invoke` | `ELECTRON_IPC_CHANNELS.updateRelaunch` | D33 |
| N049 | [desktop/src/lib/desktopHost/electronHost.ts:162](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:162) | `permissionState` | `invoke` | `ELECTRON_IPC_CHANNELS.notificationPermissionState` | D33 |
| N050 | [desktop/src/lib/desktopHost/electronHost.ts:163](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:163) | `requestPermission` | `invoke` | `ELECTRON_IPC_CHANNELS.notificationRequestPermission` | D33 |
| N051 | [desktop/src/lib/desktopHost/electronHost.ts:164](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:164) | `send` | `invoke` | `ELECTRON_IPC_CHANNELS.notificationSend` | D33 |
| N052 | [desktop/src/lib/desktopHost/electronHost.ts:165](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:165) | `onAction` | `subscribe` | `ELECTRON_EVENT_CHANNELS.notificationAction` | D33 |
| N053 | [desktop/src/lib/desktopHost/electronHost.ts:166](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:166) | `ackAction` | `invoke` | `ELECTRON_IPC_CHANNELS.notificationActionAck` | D33 |
| N054 | [desktop/src/lib/desktopHost/electronHost.ts:169](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:169) | `minimize` | `invoke` | `ELECTRON_IPC_CHANNELS.windowMinimize` | D33 |
| N055 | [desktop/src/lib/desktopHost/electronHost.ts:170](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:170) | `toggleMaximize` | `invoke` | `ELECTRON_IPC_CHANNELS.windowToggleMaximize` | D33 |
| N056 | [desktop/src/lib/desktopHost/electronHost.ts:171](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:171) | `close` | `invoke` | `ELECTRON_IPC_CHANNELS.windowClose` | D33 |
| N057 | [desktop/src/lib/desktopHost/electronHost.ts:172](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:172) | `startDragging` | `invoke` | `ELECTRON_IPC_CHANNELS.windowStartDragging` | D33 |
| N058 | [desktop/src/lib/desktopHost/electronHost.ts:173](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:173) | `requestAttention` | `invoke` | `ELECTRON_IPC_CHANNELS.windowRequestAttention` | D33 |
| N059 | [desktop/src/lib/desktopHost/electronHost.ts:174](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:174) | `focus` | `invoke` | `ELECTRON_IPC_CHANNELS.windowFocus` | D33 |
| N060 | [desktop/src/lib/desktopHost/electronHost.ts:175](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:175) | `isMaximized` | `invoke` | `ELECTRON_IPC_CHANNELS.windowIsMaximized` | D33 |
| N061 | [desktop/src/lib/desktopHost/electronHost.ts:176](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:176) | `onResized` | `subscribe` | `ELECTRON_EVENT_CHANNELS.windowResized` | D33 |
| N062 | [desktop/src/lib/desktopHost/electronHost.ts:177](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:177) | `onNativeMenuNavigate` | `subscribe` | `ELECTRON_EVENT_CHANNELS.nativeMenuNavigate` | D33 |
| N063 | [desktop/src/lib/desktopHost/electronHost.ts:181](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:181) | `spawn` | `invoke` | `ELECTRON_IPC_CHANNELS.terminalSpawn` | D33 |
| N064 | [desktop/src/lib/desktopHost/electronHost.ts:182](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:182) | `write` | `invoke` | `ELECTRON_IPC_CHANNELS.terminalWrite` | D33 |
| N065 | [desktop/src/lib/desktopHost/electronHost.ts:183](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:183) | `resize` | `invoke` | `ELECTRON_IPC_CHANNELS.terminalResize` | D33 |
| N066 | [desktop/src/lib/desktopHost/electronHost.ts:184](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:184) | `kill` | `invoke` | `ELECTRON_IPC_CHANNELS.terminalKill` | D33 |
| N067 | [desktop/src/lib/desktopHost/electronHost.ts:185](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:185) | `onOutput` | `subscribe` | `ELECTRON_EVENT_CHANNELS.terminalOutput` | D33 |
| N068 | [desktop/src/lib/desktopHost/electronHost.ts:186](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:186) | `onExit` | `subscribe` | `ELECTRON_EVENT_CHANNELS.terminalExit` | D33 |
| N069 | [desktop/src/lib/desktopHost/electronHost.ts:187](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:187) | `getBashPath` | `invoke` | `ELECTRON_IPC_CHANNELS.terminalGetBashPath` | D33 |
| N070 | [desktop/src/lib/desktopHost/electronHost.ts:188](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:188) | `setBashPath` | `invoke` | `ELECTRON_IPC_CHANNELS.terminalSetBashPath` | D33 |
| N071 | [desktop/src/lib/desktopHost/electronHost.ts:191](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:191) | `open` | `invoke` | `ELECTRON_IPC_CHANNELS.previewOpen` | D33 |
| N072 | [desktop/src/lib/desktopHost/electronHost.ts:192](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:192) | `navigate` | `invoke` | `ELECTRON_IPC_CHANNELS.previewNavigate` | D33 |
| N073 | [desktop/src/lib/desktopHost/electronHost.ts:193](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:193) | `setBounds` | `invoke` | `ELECTRON_IPC_CHANNELS.previewSetBounds` | D33 |
| N074 | [desktop/src/lib/desktopHost/electronHost.ts:194](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:194) | `setVisible` | `invoke` | `ELECTRON_IPC_CHANNELS.previewSetVisible` | D33 |
| N075 | [desktop/src/lib/desktopHost/electronHost.ts:195](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:195) | `setZoom` | `invoke` | `ELECTRON_IPC_CHANNELS.previewSetZoom` | D33 |
| N076 | [desktop/src/lib/desktopHost/electronHost.ts:196](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:196) | `close` | `invoke` | `ELECTRON_IPC_CHANNELS.previewClose` | D33 |
| N077 | [desktop/src/lib/desktopHost/electronHost.ts:197](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:197) | `message` | `invoke` | `ELECTRON_IPC_CHANNELS.previewMessage` | D33 |
| N078 | [desktop/src/lib/desktopHost/electronHost.ts:198](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:198) | `onEvent` | `subscribe` | `ELECTRON_EVENT_CHANNELS.previewEvent` | D33 |
| N079 | [desktop/src/lib/desktopHost/electronHost.ts:201](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:201) | `showMenu` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserShowMenu` | D33 |
| N080 | [desktop/src/lib/desktopHost/electronHost.ts:202](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:202) | `create` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserCreate` | D33 |
| N081 | [desktop/src/lib/desktopHost/electronHost.ts:209](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:209) | `navigate` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserNavigate` | D33 |
| N082 | [desktop/src/lib/desktopHost/electronHost.ts:210](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:210) | `goBack` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserGoBack` | D33 |
| N083 | [desktop/src/lib/desktopHost/electronHost.ts:211](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:211) | `goForward` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserGoForward` | D33 |
| N084 | [desktop/src/lib/desktopHost/electronHost.ts:212](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:212) | `reload` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserReload` | D33 |
| N085 | [desktop/src/lib/desktopHost/electronHost.ts:216](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:216) | `stop` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserStop` | D33 |
| N086 | [desktop/src/lib/desktopHost/electronHost.ts:217](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:217) | `setBounds` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserSetBounds` | D33 |
| N087 | [desktop/src/lib/desktopHost/electronHost.ts:218](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:218) | `setVisible` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserSetVisible` | D33 |
| N088 | [desktop/src/lib/desktopHost/electronHost.ts:219](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:219) | `setZoom` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserSetZoom` | D33 |
| N089 | [desktop/src/lib/desktopHost/electronHost.ts:220](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:220) | `find` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserFind` | D33 |
| N090 | [desktop/src/lib/desktopHost/electronHost.ts:225](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:225) | `stopFind` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserStopFind` | D33 |
| N091 | [desktop/src/lib/desktopHost/electronHost.ts:226](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:226) | `capture` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserCapture` | D33 |
| N092 | [desktop/src/lib/desktopHost/electronHost.ts:227](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:227) | `snapshot` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserSnapshot` | D33 |
| N093 | [desktop/src/lib/desktopHost/electronHost.ts:228](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:228) | `message` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserMessage` | D33 |
| N094 | [desktop/src/lib/desktopHost/electronHost.ts:229](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:229) | `printToPdf` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserPrintToPdf` | D33 |
| N095 | [desktop/src/lib/desktopHost/electronHost.ts:230](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:230) | `close` | `invoke` | `ELECTRON_IPC_CHANNELS.workspaceBrowserClose` | D33 |
| N096 | [desktop/src/lib/desktopHost/electronHost.ts:231](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:231) | `onEvent` | `subscribe` | `ELECTRON_EVENT_CHANNELS.workspaceBrowserEvent` | D33 |
| N097 | [desktop/src/lib/desktopHost/electronHost.ts:234](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:234) | `get` | `invoke` | `ELECTRON_IPC_CHANNELS.appModeGet` | D33 |
| N098 | [desktop/src/lib/desktopHost/electronHost.ts:235](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:235) | `set` | `invoke` | `ELECTRON_IPC_CHANNELS.appModeSet` | D33 |
| N099 | [desktop/src/lib/desktopHost/electronHost.ts:236](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:236) | `prepareRestart` | `invoke` | `ELECTRON_IPC_CHANNELS.appModePrepareRestart` | D33 |
| N100 | [desktop/src/lib/desktopHost/electronHost.ts:237](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:237) | `restart` | `invoke` | `ELECTRON_IPC_CHANNELS.appModeRestart` | D33 |
| N101 | [desktop/src/lib/desktopHost/electronHost.ts:240](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:240) | `restartSidecar` | `invoke` | `ELECTRON_IPC_CHANNELS.adaptersRestartSidecar` | D33 |
| N102 | [desktop/src/lib/desktopHost/electronHost.ts:243](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:243) | `set` | `invoke` | `ELECTRON_IPC_CHANNELS.zoomSet` | D33 |
| N103 | [desktop/src/lib/desktopHost/electronHost.ts:246](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopHost/electronHost.ts:246) | `setApplied` | `invoke` | `ELECTRON_IPC_CHANNELS.appearanceSetApplied` | D33 |

| 位置 | 命令 | 分类/任务 |
| --- | --- | --- |
| [desktop/src/lib/desktopNotifications.ts:89](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopNotifications.ts:89) | `plugin:notification&#124;is_permission_granted` | 原 commandInvoke switch 已有处理，保留并联调 D33 |
| [desktop/src/lib/desktopNotifications.ts:103](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopNotifications.ts:103) | `plugin:notification&#124;request_permission` | 原 commandInvoke switch 已有处理，保留并联调 D33 |
| [desktop/src/lib/desktopNotifications.ts:117](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopNotifications.ts:117) | `macos_notification_permission_state` | 原 commandInvoke switch 已有处理，保留并联调 D33 |
| [desktop/src/lib/desktopNotifications.ts:131](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopNotifications.ts:131) | `macos_request_notification_permission` | 原 commandInvoke switch 已有处理，保留并联调 D33 |
| [desktop/src/lib/desktopNotifications.ts:218](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopNotifications.ts:218) | `macos_send_notification` | 原 commandInvoke switch 已有处理，保留并联调 D33 |
| [desktop/src/lib/desktopNotifications.ts:269](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopNotifications.ts:269) | `macos_open_notification_settings` | 原 commandInvoke switch 已有处理，保留并联调 D33 |
| [desktop/src/lib/desktopNotifications.ts:278](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/desktop/src/lib/desktopNotifications.ts:278) | `open_windows_notification_settings` | 原 commandInvoke switch 已有处理，保留并联调 D33 |

## 9. 原默认工具的全部 49 个注册表达式

注册表达式可以展开多个工具或零个工具；49 不是独立工具数量。原 feature/env/internal/test 条件保留，实施时冻结原发行特性并逐项判定可达性。不得擅自全部开启，也不得省略原已发行工具。Go 当前 registry 枚举的 13 个基础工具只覆盖其中有限范围。

| 原版位置 | 原注册表达式（含条件） | 任务 |
| --- | --- | --- |
| [src/tools.ts:206](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:206) | `AgentTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:207](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:207) | `TaskOutputTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:208](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:208) | `...(isBashToolEnabled() ? [BashTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:212](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:212) | `...(hasEmbeddedSearchTools() ? [] : [GlobTool, GrepTool])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:213](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:213) | `ExitPlanModeV2Tool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:214](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:214) | `FileReadTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:215](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:215) | `FileEditTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:216](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:216) | `FileWriteTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:217](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:217) | `NotebookEditTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:218](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:218) | `WebFetchTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:219](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:219) | `TodoWriteTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:220](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:220) | `WebSearchTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:221](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:221) | `TaskStopTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:222](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:222) | `AskUserQuestionTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:223](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:223) | `SkillTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:224](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:224) | `EnterPlanModeTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:225](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:225) | `...(process.env.USER_TYPE === 'ant' ? [ConfigTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:226](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:226) | `...(process.env.USER_TYPE === 'ant' ? [TungstenTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:227](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:227) | `...(SuggestBackgroundPRTool ? [SuggestBackgroundPRTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:228](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:228) | `...(WebBrowserTool ? [WebBrowserTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:229](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:229) | `...(isTodoV2Enabled()       ? [TaskCreateTool, TaskGetTool, TaskUpdateTool, TaskListTool]       : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:232](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:232) | `...(OverflowTestTool ? [OverflowTestTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:233](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:233) | `...(CtxInspectTool ? [CtxInspectTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:234](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:234) | `...(TerminalCaptureTool ? [TerminalCaptureTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:235](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:235) | `...(isEnvTruthy(process.env.ENABLE_LSP_TOOL) ? [LSPTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:236](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:236) | `...(isWorktreeModeEnabled() ? [EnterWorktreeTool, ExitWorktreeTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:237](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:237) | `getSendMessageTool()` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:238](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:238) | `...sessionCollaborationTools` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:239](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:239) | `...(ListPeersTool ? [ListPeersTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:240](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:240) | `...(isAgentSwarmsEnabled()       ? [getTeamCreateTool(), getTeamPlanTool(), getTeamDeleteTool()]       : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:243](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:243) | `...(VerifyPlanExecutionTool ? [VerifyPlanExecutionTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:244](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:244) | `...(process.env.USER_TYPE === 'ant' && REPLTool ? [REPLTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:245](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:245) | `getWorkflowTool()` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:246](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:246) | `...(SleepTool ? [SleepTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:247](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:247) | `...cronTools` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:248](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:248) | `...(RemoteTriggerTool ? [RemoteTriggerTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:249](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:249) | `...(MonitorTool ? [MonitorTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:250](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:250) | `BriefTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:251](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:251) | `ImageGenTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:252](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:252) | `ImageEditTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:253](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:253) | `...(SendUserFileTool ? [SendUserFileTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:254](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:254) | `...(PushNotificationTool ? [PushNotificationTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:255](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:255) | `...(SubscribePRTool ? [SubscribePRTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:256](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:256) | `...(powerShellTool ? [powerShellTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:257](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:257) | `...(SnipTool ? [SnipTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:258](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:258) | `...(process.env.NODE_ENV === 'test' ? [TestingPermissionTool] : [])` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:259](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:259) | `ListMcpResourcesTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:260](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:260) | `ReadMcpResourceTool` | D32；并绑定所属 D01–D31 runtime |
| [src/tools.ts:263](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:263) | `...(isToolSearchEnabledOptimistic() ? [ToolSearchTool] : [])` | D32；并绑定所属 D01–D31 runtime |

## 10. Go Server / application 的全部 46 处显式能力守卫命中

包括条件绑定检查、缺失分支和 unsupported 定义。不能把源码命中数当作未实现功能数量；条件守卫必须检查正常启动的真实注入。每条在所属工作包关闭前保留其判断。

- G01 [backend/internal/server/filesystem_search.go:395](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/filesystem_search.go:395)：`return nil, unsupported("filesystem search initial/global settings binding")`
- G02 [backend/internal/server/models.go:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/models.go:51)：`return unsupported("models/current managed settings")`
- G03 [backend/internal/server/models.go:76](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/models.go:76)：`return unsupported("models/current user settings")`
- G04 [backend/internal/server/models.go:89](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/models.go:89)：`return unsupported("models standalone/Claude OAuth resolution")`
- G05 [backend/internal/server/models.go:101](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/models.go:101)：`return unsupported("models/current managed settings")`
- G06 [backend/internal/server/models.go:153](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/models.go:153)：`return unsupported("effort user settings")`
- G07 [backend/internal/server/output_styles.go:28](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/output_styles.go:28)：`return nil, unsupported("settings/output-styles native loader")`
- G08 [backend/internal/server/providers.go:44](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/providers.go:44)：`return unsupported("providers/auth-status OAuth freshness")`
- G09 [backend/internal/server/providers.go:66](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/providers.go:66)：`return unsupported("providers/auth-status original settings")`
- G10 [backend/internal/server/providers.go:111](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/providers.go:111)：`return unsupported("providers/" + id + "/" + action)`
- G11 [backend/internal/server/providers.go:145](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/providers.go:145)：`return unsupported("providers/" + id + "/" + action)`
- G12 [backend/internal/server/providers.go:180](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/providers.go:180)：`return unsupported("providers/settings")`
- G13 [backend/internal/server/rewind.go:72](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/rewind.go:72)：`return unsupported("session rewind recorded shell classifier binding")`
- G14 [backend/internal/server/server.go:342](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:342)：`func unsupported(resource string) error {`
- G15 [backend/internal/server/server.go:395](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:395)：`writeError(writer, unsupported("provider proxy/filesystem preview transport"))`
- G16 [backend/internal/server/server.go:410](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:410)：`err = unsupported("desktop-ui")`
- G17 [backend/internal/server/server.go:416](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:416)：`err = unsupported("h5-access")`
- G18 [backend/internal/server/server.go:434](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:434)：`err = unsupported(strings.Join(parts[1:], "/"))`
- G19 [backend/internal/server/server.go:437](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/server.go:437)：`err = unsupported(strings.Join(parts[1:], "/"))`
- G20 [backend/internal/server/sessions.go:87](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/sessions.go:87)：`return nil, unsupported("session repository/worktree launch")`
- G21 [backend/internal/server/sessions.go:390](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/sessions.go:390)：`return unsupported("sessions/" + id)`
- G22 [backend/internal/server/sessions.go:431](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/sessions.go:431)：`return unsupported(strings.Join(parts[1:], "/"))`
- G23 [backend/internal/server/settings.go:12](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/settings.go:12)：`return unsupported("settings")`
- G24 [backend/internal/server/settings.go:51](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/settings.go:51)：`return unsupported("settings/" + sub)`
- G25 [backend/internal/server/subagent_runs.go:202](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/subagent_runs.go:202)：`return nil, unsupported("subagent by-agent workflow reconstruction")`
- G26 [backend/internal/server/subagent_runs.go:279](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/subagent_runs.go:279)：`return unsupported("subagent live inbox send_agent_message control")`
- G27 [backend/internal/server/transcript_trim.go:83](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/transcript_trim.go:83)：`return sessionrewind.TrimResult{}, unsupported("rewind full settings persistence policy binding")`
- G28 [backend/internal/server/turn_checkpoints.go:116](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/turn_checkpoints.go:116)：`return unsupported("turn checkpoints recorded shell classifier binding")`
- G29 [backend/internal/server/turn_checkpoints.go:172](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/turn_checkpoints.go:172)：`return unsupported("turn checkpoint incomplete file-history evidence")`
- G30 [backend/internal/server/turn_checkpoints.go:176](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/turn_checkpoints.go:176)：`return unsupported("turn checkpoint recorded shell classifier: " + commandCapability.Feature)`
- G31 [backend/internal/server/turn_checkpoints.go:180](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/turn_checkpoints.go:180)：`return unsupported("turn checkpoint: " + capability.Feature)`
- G32 [backend/internal/server/workspace_changes.go:316](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/server/workspace_changes.go:316)：`return nil, unsupported("workspace non-string file-history backup name")`
- G33 [backend/internal/application/command.go:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/command.go:34)：`return Command{}, parsed, &clibootstrap.MainCapabilityError{Boundary: "CLI action " + parsed.Parsed.Path}`
- G34 [backend/internal/application/command.go:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/command.go:39)：`return Command{}, parsed, &clibootstrap.MainCapabilityError{Boundary: "CLI option " + key}`
- G35 [backend/internal/application/command.go:85](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/command.go:85)：`return Command{}, parsed, &clibootstrap.MainCapabilityError{Boundary: "complete default tool catalog"}`
- G36 [backend/internal/application/command.go:114](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/command.go:114)：`return Command{}, parsed, &clibootstrap.MainCapabilityError{Boundary: "verbose JSON output collection"}`
- G37 [backend/internal/application/command.go:128](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/command.go:128)：`return 1, &clibootstrap.MainCapabilityError{Boundary: "positional prompt with structured SDK stdin"}`
- G38 [backend/internal/application/desktop.go:92](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/desktop.go:92)：`return &clibootstrap.MainCapabilityError{Boundary: "desktop attachments"}`
- G39 [backend/internal/application/desktop.go:95](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/desktop.go:95)：`return &clibootstrap.MainCapabilityError{Boundary: "desktop session reference context"}`
- G40 [backend/internal/application/desktop.go:128](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/desktop.go:128)：`return &clibootstrap.MainCapabilityError{Boundary: "permission runtime/rule updates"}`
- G41 [backend/internal/application/desktop.go:159](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/desktop.go:159)：`return &clibootstrap.MainCapabilityError{Boundary: "desktop control " + m.String("type")}`
- G42 [backend/internal/application/session.go:141](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/session.go:141)：`return &clibootstrap.MainCapabilityError{Boundary: "interactive attachment producer"}`
- G43 [backend/internal/application/session.go:146](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/session.go:146)：`return &clibootstrap.MainCapabilityError{Boundary: "interactive pasted attachment producer " + pasted.Type}`
- G44 [backend/internal/application/session.go:154](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/session.go:154)：`return &clibootstrap.MainCapabilityError{Boundary: "interactive input mode " + in.Mode}`
- G45 [backend/internal/application/session.go:159](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/session.go:159)：`return &clibootstrap.MainCapabilityError{Boundary: "interactive /model argument handler"}`
- G46 [backend/internal/application/session.go:258](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/session.go:258)：`return &clibootstrap.MainCapabilityError{Boundary: "permission rule persistence"}`

## 11. 实施与关闭方式

每个入口先绑定 D01–D35 的工作包，再记录原参数/字段/错误/副作用、Go 路由与服务、持久化和事件。接口返回成功只能关闭响应分支；涉及安装/认证/运行/写文件/发消息/重启的业务必须验证其实际结果。

先固定对照和确定性门禁，再做真实 Electron/Web/CLI/TUI 与官方 DeepSeek 任务，最后在相同评分条件下比较原 Benchmark。跨平台和未执行项继续标注未验收。

本次只生成静态索引与待办。源码快照 SHA-256 保存在 JSON.sourceSnapshot，后续源码变化时重新盘点受影响入口，保留旧证据对应的候选。
