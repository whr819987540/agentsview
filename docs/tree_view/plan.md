# 会话关系树可视化计划

## Summary

在会话详情页右侧 Vitals 栏上方新增紧凑的 “Session tree” 关系树。树围绕当前
session 展开到其根节点和所有后代，节点可点击并跳转到对应 session；叶子节点和
分支起点会有明确视觉强调。默认覆盖所有已落库的 `parent_session_id` 关系，包括
`fork`、`subagent`、`continuation`，并补齐 Codex 原生 fork 的父子关系落库。

## Key Changes

Codex 解析：

- 读取 `session_meta.payload.forked_from_id`，将 fork session 写为
  `parent_session_id = "codex:" + forked_from_id`、`relationship_type = "fork"`。
- 保持现有 fork replay 抑制逻辑不变。
- bump parser `data_version`，让已有 Codex fork session 通过非破坏性 resync
  补齐关系字段。

API/interface：

- 新增 `GET /api/v1/sessions/{id}/tree`。
- 返回嵌套结构：

  ```ts
  interface SessionTreeResponse {
    root: SessionTreeNode;
    active_session_id: string;
    truncated: boolean;
  }

  interface SessionTreeNode {
    session: Session;
    children: SessionTreeNode[];
    depth: number;
    is_active: boolean;
    is_leaf: boolean;
    is_branch_start: boolean;
  }
  ```

- 服务端用现有 `db.Store.GetSession` 和 `GetChildSessions` 构建树，自动兼容
  SQLite、PostgreSQL、DuckDB；缺失父节点时以当前可达最高节点为 root。
- 加循环保护和节点上限，超过上限返回 `truncated: true`。

Frontend UI：

- 新增 `SessionRelationshipTree.svelte`，在 `App.svelte` 的右侧 vitals slot 中渲染
  于 `SessionVitals` 上方。
- 仅当树中超过 1 个节点时显示；单独 session 不占用空间。
- 每个节点渲染为 anchor/button，使用 `router.buildSessionHref()` 和
  `router.navigateToSession()` 跳转。
- 节点显示 session title/preview、agent、relationship chip、消息数；active 节点
  高亮。
- 叶子节点与分支起点视觉强调；所有节点保持可点击，满足叶子和分支起点跳转要求。
- 所有新增可见文案写入 `frontend/messages/en.json`、`zh-CN.json`、`zh-TW.json`，
  通过 Paraglide `m.*()` 调用。

## Test Plan

Go：

- Codex parser test：`forked_from_id` 生成 `ParentSessionID` 和 `RelFork`，并继续跳过
  replay history。
- Server route test：当前节点在中间层、根节点、叶子节点、缺失父节点、循环保护、
  truncation。
- SQLite store behavior uses existing child query；PostgreSQL/DuckDB parity verified by
  route/service tests where practical。
- Go edits 后运行 `go fmt ./...`、`go vet ./...` 和相关 `go test`。

Frontend：

- Component tests for loading tree, hiding single-node tree, active highlight, leaf/branch
  classes, click navigation, localized labels。
- Run `npm run i18n:compile` and `npm run check` from `frontend/`。

## Assumptions

- 未收到进一步选择时，默认覆盖所有 session relationships，而不是只做 Codex fork。
- 默认位置为右侧 Vitals 栏，避免打断 transcript 主阅读区。
- 不新增数据库表；复用现有 `sessions.parent_session_id` 和 `relationship_type`。
- 实现完成且修改 tracked files 时，按仓库规则创建一个 focused conventional commit。
