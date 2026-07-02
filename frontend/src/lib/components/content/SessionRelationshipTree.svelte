<script lang="ts">
  import { fetchSessionTree } from "../../api/sessionTree.js";
  import type {
    Session,
    SessionTreeNode,
    SessionTreeResponse,
  } from "../../api/types/core.js";
  import { m } from "../../i18n/index.js";
  import { router } from "../../stores/router.svelte.js";
  import { formatNumber } from "../../utils/format.js";

  interface Props {
    sessionId: string;
  }

  let { sessionId }: Props = $props();

  let tree = $state<SessionTreeResponse | null>(null);
  let requestSerial = 0;

  $effect(() => {
    const id = sessionId;
    const serial = ++requestSerial;
    tree = null;

    fetchSessionTree(id)
      .then((next) => {
        if (serial === requestSerial) {
          tree = next;
        }
      })
      .catch((err) => {
        if (serial === requestSerial) {
          tree = null;
        }
        console.error("failed to load session tree", err);
      });
  });

  function flattenTree(node: SessionTreeNode): SessionTreeNode[] {
    const rows = [node];
    for (const child of node.children) {
      rows.push(...flattenTree(child));
    }
    return rows;
  }

  let rows = $derived(tree ? flattenTree(tree.root) : []);
  let visible = $derived(rows.length > 1);

  function sessionTitle(session: Session): string {
    const title = (
      session.display_name ||
      session.first_message ||
      ""
    ).trim();
    return title || m.session_tree_untitled();
  }

  function sessionPreview(session: Session): string {
    const first = (session.first_message || "").trim();
    if (!first || first === sessionTitle(session)) return session.id;
    return first;
  }

  function relationshipLabel(value: string | undefined): string {
    switch (value) {
      case "fork":
        return m.session_tree_relationship_fork();
      case "subagent":
        return m.session_tree_relationship_subagent();
      case "continuation":
        return m.session_tree_relationship_continuation();
      default:
        return m.session_tree_relationship_root();
    }
  }

  function messageCountLabel(count: number): string {
    return m.session_tree_message_count({
      count,
      countLabel: formatNumber(count),
    });
  }

  function navigate(
    event: MouseEvent & { currentTarget: HTMLAnchorElement },
    id: string,
  ) {
    if (
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    ) {
      return;
    }
    event.preventDefault();
    router.navigateToSession(id);
  }
</script>

{#if visible}
  <section class="session-tree" aria-label={m.session_tree_title()}>
    <header class="tree-header">
      <span>{m.session_tree_title()}</span>
      {#if tree?.truncated}
        <span class="tree-truncated">{m.session_tree_truncated()}</span>
      {/if}
    </header>

    <div class="tree-list">
      {#each rows as node (node.session.id)}
        {@const session = node.session}
        <a
          class="tree-node"
          class:active={node.is_active}
          class:leaf={node.is_leaf}
          class:branch-start={node.is_branch_start}
          href={router.buildSessionHref(session.id)}
          aria-current={node.is_active ? "page" : undefined}
          title={sessionTitle(session)}
          style={`--tree-depth: ${node.depth};`}
          onclick={(event) => navigate(event, session.id)}
        >
          <span class="tree-guide" aria-hidden="true"></span>
          <span class="tree-marker" aria-hidden="true"></span>
          <span class="tree-body">
            <span class="tree-row-main">
              <span class="tree-title">{sessionTitle(session)}</span>
              <span class="tree-rel">
                {relationshipLabel(session.relationship_type)}
              </span>
            </span>
            <span class="tree-preview">{sessionPreview(session)}</span>
            <span class="tree-meta">
              <span>{session.agent}</span>
              <span>{messageCountLabel(session.message_count)}</span>
            </span>
          </span>
        </a>
      {/each}
    </div>
  </section>
{/if}

<style>
  .session-tree {
    flex-shrink: 0;
    border-bottom: 1px solid var(--border-default);
    background: var(--bg-surface);
  }

  .tree-header {
    min-height: 34px;
    padding: 9px 14px 7px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    color: var(--text-muted);
    font-size: 9px;
    font-weight: 600;
    letter-spacing: 0.6px;
    text-transform: uppercase;
  }

  .tree-truncated {
    color: var(--warning-fg, var(--text-muted));
    font-family: var(--font-mono);
    font-size: 9px;
    font-weight: 500;
    letter-spacing: 0;
    text-transform: none;
    white-space: nowrap;
  }

  .tree-list {
    padding: 0 8px 10px;
    display: grid;
    gap: 2px;
  }

  .tree-node {
    --depth-offset: calc(var(--tree-depth) * 14px);
    position: relative;
    min-height: 46px;
    padding: 6px 8px 6px calc(14px + var(--depth-offset));
    display: grid;
    grid-template-columns: 14px minmax(0, 1fr);
    gap: 7px;
    align-items: start;
    border-radius: var(--radius-sm);
    color: inherit;
    text-decoration: none;
    outline: none;
  }

  .tree-node:hover {
    background: var(--bg-surface-hover);
  }

  .tree-node:focus-visible {
    box-shadow: 0 0 0 2px var(--accent-blue);
  }

  .tree-node.active {
    background: var(--selection-bg);
  }

  .tree-guide {
    position: absolute;
    left: calc(15px + var(--depth-offset));
    top: 0;
    bottom: 0;
    width: 1px;
    background: var(--border-muted);
  }

  .tree-marker {
    position: relative;
    z-index: 1;
    width: 8px;
    height: 8px;
    margin-top: 6px;
    border-radius: 50%;
    border: 1px solid var(--border-strong);
    background: var(--bg-surface);
  }

  .tree-node.leaf .tree-marker {
    border-color: var(--accent-green, var(--accent-blue));
    background: var(--accent-green, var(--accent-blue));
    box-shadow: 0 0 0 3px var(--accent-green-muted, transparent);
  }

  .tree-node.branch-start .tree-marker {
    width: 10px;
    height: 10px;
    margin-left: -1px;
    border-color: var(--accent-blue);
    background: var(--accent-blue);
  }

  .tree-node.active .tree-marker {
    border-color: var(--text-primary);
    background: var(--text-primary);
  }

  .tree-body {
    min-width: 0;
    display: grid;
    gap: 2px;
  }

  .tree-row-main {
    display: flex;
    align-items: center;
    min-width: 0;
    gap: 6px;
  }

  .tree-title {
    min-width: 0;
    overflow: hidden;
    color: var(--text-primary);
    font-size: 11px;
    font-weight: 620;
    line-height: 1.25;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .tree-rel {
    flex-shrink: 0;
    padding: 1px 5px;
    border: 1px solid var(--border-muted);
    border-radius: var(--radius-xs, 4px);
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 8px;
    line-height: 1.35;
    text-transform: uppercase;
  }

  .tree-node.active .tree-rel {
    border-color: var(--accent-blue);
    color: var(--accent-blue);
  }

  .tree-preview {
    min-width: 0;
    overflow: hidden;
    color: var(--text-secondary);
    font-size: 10px;
    line-height: 1.25;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .tree-meta {
    display: flex;
    min-width: 0;
    gap: 7px;
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 9px;
    line-height: 1.25;
  }

  .tree-meta span {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
</style>
