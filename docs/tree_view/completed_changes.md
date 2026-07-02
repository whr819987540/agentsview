# 会话关系树已完成修改

## Commits

- `ddd462e feat: add session relationship tree`
- `571d0c7 test: format session tree test`

## Backend

- 新增 `GET /api/v1/sessions/{id}/tree`。
- API 返回 `root`、`active_session_id`、`truncated`，节点包含：
  `session`、`children`、`depth`、`is_active`、`is_leaf`、`is_branch_start`。
- 树构建逻辑复用 `db.Store.GetSession` 和 `GetChildSessions`，因此走现有
  SQLite/PostgreSQL/DuckDB store 接口能力。
- 向上查找父链时支持缺失父节点：以当前可达最高节点为 root。
- 加入循环保护；遇到循环时停止继续展开并设置 `truncated: true`。
- 加入节点上限 `500`；超过上限时停止展开并设置 `truncated: true`。

## Codex Parser

- `internal/parser/codex.go` 读取 `session_meta.payload.forked_from_id`。
- Codex fork session 会写入：
  - `ParentSessionID = "codex:" + forked_from_id`
  - `RelationshipType = parser.RelFork`
- 复用 `codexPrefixedSessionID`，同时保留 subagent session id 的 `codex:` 前缀处理。
- 原有 fork replay 抑制逻辑保持不变。
- `internal/db/db.go` 的 parser data version 从 `57` bump 到 `58`，用于触发旧 Codex
  fork session 非破坏性 resync 补齐关系字段。

## Frontend

- 新增 `frontend/src/lib/components/content/SessionRelationshipTree.svelte`。
- 新增 `frontend/src/lib/api/sessionTree.ts`。
- 在 `frontend/src/lib/api/types/core.ts` 增加 `SessionTreeResponse` 和
  `SessionTreeNode` 类型。
- 在 `frontend/src/App.svelte` 的右侧 vitals slot 中，将 `SessionRelationshipTree`
  渲染在 `SessionVitals` 上方。
- 树只在节点数超过 1 时显示；单 session 不占用空间。
- 每个节点是可点击 anchor：
  - `href` 使用 `router.buildSessionHref(session.id)`
  - 普通左键点击使用 `router.navigateToSession(session.id)`
- 节点显示：
  - title 或 untitled fallback
  - first-message preview 或 session id
  - agent
  - relationship chip
  - message count
- active 节点、叶子节点、分支起点分别有独立 class 和视觉强调。
- 新增本地化 key：
  - `session_tree_title`
  - `session_tree_truncated`
  - `session_tree_untitled`
  - `session_tree_relationship_root`
  - `session_tree_relationship_fork`
  - `session_tree_relationship_subagent`
  - `session_tree_relationship_continuation`
  - `session_tree_message_count`
- 本地化文件已同步：
  - `frontend/messages/en.json`
  - `frontend/messages/zh-CN.json`
  - `frontend/messages/zh-TW.json`

## Tests Added

- Codex parser test now asserts `forked_from_id` produces:
  - `ParentSessionID = "codex:parent-1"`
  - `RelationshipType = RelFork`
  - replayed fork history remains skipped
- Server route tests cover:
  - current node in middle of a tree
  - missing parent fallback
  - cycle protection
  - truncation at node limit
- Frontend component tests cover:
  - hiding single-node tree
  - active highlight
  - leaf class
  - branch-start class
  - truncated/localized labels
  - click navigation via router

## Validation

Frontend validation:

```bash
cd frontend
npm run i18n:compile
npm test -- SessionRelationshipTree.test.ts
npm run check
```

Go validation used after installing Go and restoring the pricing snapshot:

```bash
make pricing-snapshot
mkdir -p "$HOME/tmp"
TMPDIR="$HOME/tmp" go test -tags fts5 ./internal/pricing ./internal/parser ./internal/server ./internal/db
TMPDIR="$HOME/tmp" go vet -tags fts5 ./internal/pricing ./internal/parser ./internal/server ./internal/db
```

Additional checks:

```bash
git diff --check
node -e "for (const f of ['frontend/messages/en.json','frontend/messages/zh-CN.json','frontend/messages/zh-TW.json']) JSON.parse(require('fs').readFileSync(f,'utf8'));"
```

## Runtime Notes

- UI 入口在 session 详情页右上角的 Analysis/Vitals 图标；打开右侧栏后，
  `Session tree` 显示在 `Analysis` 面板上方。
- 如果当前 session 所在树只有一个节点，树不会显示。
- Codex 原生 fork 关系需要 data version `58` 后重新 sync/resync 才会补齐到 DB。
- 本机 `/tmp/.git` 会污染 parser 测试的项目名推断；运行 Go 测试时建议设置
  `TMPDIR="$HOME/tmp"`。
