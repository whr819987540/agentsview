<script lang="ts">
  import { onDestroy } from "svelte";
  import { copyToClipboard } from "../../utils/clipboard.js";
  import { searchBlock } from "../../search/session-block.svelte.js";
  import { highlightToHtml } from "../../utils/syntax-highlight.js";
  import { CopyButton } from "@kenn-io/kit-ui";
  import { ChevronRightIcon } from "../../icons.js";
  import { m } from "../../i18n/index.js";
  import { searchCollapsed } from "../../search/component-state.js";
  import { inSessionSearch } from "../../stores/inSessionSearch.svelte.js";
  import { ui } from "../../stores/ui.svelte.js";

  interface Props {
    content: string;
    language?: string;
    searchKey?: string;
  }

  let { content, language, searchKey }: Props = $props();
  let copied = $state(false);
  let copyTimer: ReturnType<typeof setTimeout> | undefined;
  let highlighted = $state<string | null>(null);

  // Code blocks start expanded: unlike thinking/tool blocks they are
  // usually the point of the message. They collapse to a one-line
  // preview so the breadcrumb's bulk controls can fold a long
  // transcript down to its prompts and answers.
  let userCollapsed = $state(false);
  let overrideSeq = $state(-1);
  let collapsed = $derived(searchCollapsed(
    userCollapsed, inSessionSearch.isCurrentBlock(searchKey),
    inSessionSearch.navigationRevision, overrideSeq,
  ));

  // A bulk collapse/expand command from the breadcrumb controls acts
  // like a manual toggle on every visible code block: it wins over
  // search auto-reveal until the next search navigation.
  let appliedBulkCommandId = $state(0);
  $effect(() => {
    const command = ui.bulkCollapseCommand;
    if (!command || command.id === appliedBulkCommandId) return;
    appliedBulkCommandId = command.id;
    if (!command.visibleBlocks.includes("code")) return;
    userCollapsed = command.target === "collapsed";
    overrideSeq = inSessionSearch.navigationRevision;
  });

  let displayLanguage = $derived(language || m.code_block_label());
  let previewLine = $derived.by(() => {
    const firstLine = content
      .split(/\r?\n/)
      .map((line) => line.trim())
      .find((line) => line.length > 0);
    return firstLine?.slice(0, 160) ?? "";
  });

  $effect(() => {
    const source = content;
    const lang = language;
    highlighted = null;
    if (!lang) return;
    let cancelled = false;
    void highlightToHtml(source, lang).then((html) => {
      if (!cancelled) highlighted = html;
    }).catch(() => {
      // Keep the original text if the optional syntax highlighter fails.
    });
    return () => { cancelled = true; };
  });

  // Keep copy controlled through the application's clipboard utility.
  async function handleCopy() {
    const ok = await copyToClipboard(content);
    if (!ok) return;
    clearTimeout(copyTimer);
    copied = true;
    copyTimer = setTimeout(() => { copied = false; }, 1500);
  }

  onDestroy(() => { clearTimeout(copyTimer); });
</script>

<!-- kit-ui-check-ignore: controlled clipboard behavior and a search attachment on pre require the app-owned code block. -->
<div class="code-block">
  <CopyButton
    class="code-copy"
    revealOnHover
    {copied}
    ariaLabel={m.code_block_copy_code_block()}
    copiedAriaLabel={m.code_block_copied_code_block()}
    title={m.code_block_copy_code()}
    copiedTitle={m.code_block_copied()}
    onclick={handleCopy}
  />
  <div class="code-header">
    <button
      type="button"
      class="code-toggle"
      aria-expanded={!collapsed}
      title={collapsed ? m.code_block_expand() : m.code_block_collapse()}
      aria-label={collapsed ? m.code_block_expand() : m.code_block_collapse()}
      onclick={() => {
        userCollapsed = !collapsed;
        overrideSeq = inSessionSearch.navigationRevision;
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
  </div>
  {#if !collapsed}
    <pre class="code-content" {@attach searchBlock(searchKey)}><code>{#if highlighted !== null}{@html highlighted}{:else}{content}{/if}</code></pre>
  {/if}
</div>

<style>
  /* kit-ui-check-ignore: app-owned code block, see markup note above */
  .code-block {
    position: relative;
    background: var(--code-bg);
    border-radius: var(--radius-md);
    margin: 4px 0;
    overflow: hidden;
  }
  :global(.code-copy.kit-copy-btn) {
    position: absolute;
    top: 6px;
    right: 6px;
    z-index: 1;
  }
  /* kit-ui-check-ignore: app-owned code block, see markup note above */
  .code-block:hover :global(.code-copy.kit-copy-btn) { opacity: 1; }
  .code-header {
    border-bottom: 1px solid color-mix(in srgb, var(--code-text) 8%, transparent);
  }
  .code-toggle {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 4px 12px;
    background: none;
    border: none;
    text-align: left;
    cursor: pointer;
    color: var(--code-text);
    min-width: 0;
  }
  .code-chevron {
    display: inline-flex;
    align-items: center;
    opacity: 0.5;
    transition: transform 0.15s;
  }
  .code-chevron.open { transform: rotate(90deg); }
  .code-lang {
    font-family: var(--font-mono);
    font-size: 11px;
    font-weight: 500;
    opacity: 0.5;
  }
  .code-preview {
    font-family: var(--font-mono);
    font-size: 11px;
    opacity: 0.45;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
  }
  .code-content {
    padding: 12px 16px;
    font-family: var(--font-mono);
    font-size: 13px;
    line-height: 1.55;
    color: var(--code-text);
    overflow-x: auto;
  }
  .code-content code { font-family: inherit; }
  @media (max-width: 760px) {
    .code-content { max-width: calc(100vw - 32px); }
  }
</style>
