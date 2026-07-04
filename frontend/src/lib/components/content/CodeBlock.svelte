<script lang="ts">
  import { onDestroy, tick, untrack } from "svelte";
  import { copyToClipboard } from "../../utils/clipboard.js";
  import { applyHighlight, applyMarks, clearMarks, escapeHTML } from "../../utils/highlight.js";
  import { highlightToHtml } from "../../utils/syntax-highlight.js";
  import CopyButton from "../shared/CopyButton.svelte";
  import { m } from "../../i18n/index.js";
  import { ChevronRightIcon } from "../../icons.js";
  import { ui } from "../../stores/ui.svelte.js";

  interface Props {
    content: string;
    language?: string;
    highlightQuery?: string;
    isCurrentHighlight?: boolean;
  }

  let { content, language, highlightQuery = "", isCurrentHighlight = false }: Props = $props();
  let copied = $state(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let userCollapsed = $state(false);
  let userOverride = $state(false);
  let searchExpanded = $state(false);
  let prevQuery = $state("");
  let appliedBulkCommandId = $state(0);

  let highlighted = $state<string | null>(null);
  let preEl = $state<HTMLElement | undefined>(undefined);

  $effect(() => {
    const q = highlightQuery;
    const trimmed = q.trim();
    searchExpanded =
      trimmed !== "" &&
      content.toLowerCase().includes(trimmed.toLowerCase());
    if (q !== prevQuery) {
      userOverride = false;
      prevQuery = q;
    }
  });

  $effect(() => {
    const command = ui.bulkCollapseCommand;
    if (!command || command.id === appliedBulkCommandId) return;
    appliedBulkCommandId = command.id;
    if (!command.visibleBlocks.includes("code")) return;
    userCollapsed = command.target === "collapsed";
    userOverride = true;
  });

  let collapsed = $derived(
    userOverride ? userCollapsed
      : searchExpanded ? false
      : userCollapsed,
  );

  let displayLanguage = $derived(language || m.code_block_label());
  let previewLine = $derived.by(() => {
    const firstLine = content
      .split(/\r?\n/)
      .map((line) => line.trim())
      .find((line) => line.length > 0);
    return firstLine?.slice(0, 160) ?? "";
  });

  $effect(() => {
    highlighted = null;
    if (!language) return;

    const effectContent = content;
    const effectLang = language;
    let cancelled = false;

    highlightToHtml(effectContent, effectLang).then(async (html) => {
      if (cancelled) return;
      highlighted = html;
      // Flush the {@html} swap to the DOM before re-applying marks.
      await tick();
      if (cancelled) return;
      // Read current prop values after the await — intentionally untracked
      // because we are inside an async continuation, not during the sync
      // reactive evaluation.
      const q = untrack(() => highlightQuery);
      const current = untrack(() => isCurrentHighlight);
      const el = untrack(() => preEl);
      if (el && q.trim()) {
        clearMarks(el);
        applyMarks(el, q, current);
      }
    });

    return () => {
      cancelled = true;
    };
  });

  async function handleCopy() {
    const ok = await copyToClipboard(content);
    if (!ok) return;

    clearTimeout(copyTimer);
    copied = true;
    copyTimer = setTimeout(() => {
      copied = false;
    }, 1500);
  }

  onDestroy(() => {
    clearTimeout(copyTimer);
  });
</script>

<div class="code-block">
  <div class="code-header">
    <button
      type="button"
      class="code-toggle"
      title={collapsed ? m.code_block_expand() : m.code_block_collapse()}
      aria-label={collapsed ? m.code_block_expand() : m.code_block_collapse()}
      onclick={() => {
        userCollapsed = !userCollapsed;
        userOverride = true;
      }}
    >
      <span class="code-chevron" class:open={!collapsed}>
        <ChevronRightIcon size="10" strokeWidth="2.4" aria-hidden="true" />
      </span>
      <span class="code-lang">{displayLanguage}</span>
      {#if collapsed && previewLine}
        <span class="code-preview">{previewLine}</span>
      {/if}
    </button>
    <CopyButton
      class="code-copy"
      {copied}
      ariaLabel={m.code_block_copy_code_block()}
      copiedAriaLabel={m.code_block_copied_code_block()}
      title={m.code_block_copy_code()}
      copiedTitle={m.code_block_copied()}
      onclick={handleCopy}
    />
  </div>
  {#if !collapsed}
    <pre
      class="code-content"
      bind:this={preEl}
      use:applyHighlight={{ q: highlightQuery, current: isCurrentHighlight, content }}
    ><code>{@html highlighted ?? escapeHTML(content)}</code></pre>
  {/if}
</div>

<style>
  .code-block {
    background: var(--code-bg);
    border-radius: var(--radius-md);
    margin: 4px 0;
    overflow: hidden;
  }

  .code-header {
    display: flex;
    align-items: center;
    border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  }

  .code-block:hover :global(.code-copy.copy-btn),
  .code-header :global(.code-copy.copy-btn:focus-visible) {
    opacity: 1;
  }

  .code-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
    flex: 1;
    padding: 4px 12px;
    color: var(--code-text);
    text-align: left;
    transition: background 0.1s;
  }

  .code-toggle:hover {
    background: rgba(255, 255, 255, 0.04);
  }

  .code-chevron {
    display: inline-flex;
    align-items: center;
    color: var(--text-muted);
    flex-shrink: 0;
    transition: transform 0.15s;
  }

  .code-chevron.open {
    transform: rotate(90deg);
  }

  .code-lang {
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 500;
    color: var(--code-text);
    opacity: 0.5;
    white-space: nowrap;
    flex-shrink: 0;
  }

  .code-preview {
    font-family: var(--font-mono);
    font-size: 12px;
    color: var(--text-muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
  }

  :global(.code-copy.copy-btn) {
    opacity: 1;
    margin-right: 4px;
  }

  .code-content {
    padding: 12px 16px;
    font-family: var(--font-mono);
    font-size: 13px;
    line-height: 1.55;
    color: var(--code-text);
    overflow-x: auto;
  }

  .code-content code {
    font-family: inherit;
  }

  @media (max-width: 767px) {
    .code-content {
      max-width: calc(100vw - 32px);
    }
  }
</style>
