<script lang="ts">
  import { fetchSessionInputOutline } from "../../api/inputOutline.js";
  import type { ServiceInputOutlineItem as InputOutlineItem } from "../../api/generated/index.js";
  import { m } from "../../i18n/index.js";
  import { MessageSquareTextIcon, SquareTerminalIcon } from "../../icons.js";
  import { ui } from "../../stores/ui.svelte.js";

  interface Props {
    sessionId: string;
  }

  let { sessionId }: Props = $props();

  let items = $state<InputOutlineItem[]>([]);
  let loading = $state(false);
  let error = $state<string | null>(null);

  $effect(() => {
    const controller = new AbortController();
    loading = true;
    error = null;
    items = [];

    fetchSessionInputOutline(sessionId, {
      includeForkContext: true,
      signal: controller.signal,
    })
      .then((outline) => {
        items = outline.items ?? [];
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        error = err instanceof Error ? err.message : String(err);
      })
      .finally(() => {
        if (!controller.signal.aborted) loading = false;
      });

    return () => controller.abort();
  });

  function jumpTo(item: InputOutlineItem) {
    ui.scrollToOrdinal(item.ordinal);
  }
</script>

<section class="outline-section">
  <header class="outline-header">
    <span>{m.session_input_outline_title()}</span>
    {#if items.length > 0}
      <span class="outline-count">{items.length}</span>
    {/if}
  </header>

  {#if loading}
    <div class="outline-state">{m.session_input_outline_loading()}</div>
  {:else if error}
    <div class="outline-state error">{m.session_input_outline_error()}</div>
  {:else if items.length === 0}
    <div class="outline-state">{m.session_input_outline_empty()}</div>
  {:else}
    <div class="outline-list">
      {#each items as item (item.ordinal)}
        <button
          type="button"
          class="outline-item"
          class:active={ui.selectedOrdinal === item.ordinal}
          title={m.session_input_outline_jump_to({
            preview: item.preview,
          })}
          onclick={() => jumpTo(item)}
        >
          <span class="outline-icon" class:shell={item.is_shell}>
            {#if item.is_shell}
              <SquareTerminalIcon size="12" strokeWidth="2" aria-hidden="true" />
            {:else}
              <MessageSquareTextIcon size="12" strokeWidth="2" aria-hidden="true" />
            {/if}
          </span>
          <span class="outline-preview" class:shell={item.is_shell}>
            {item.preview}
          </span>
          <span class="outline-ordinal">#{item.ordinal}</span>
        </button>
      {/each}
    </div>
  {/if}
</section>

<style>
  .outline-section {
    padding: 10px 14px 12px;
    border-bottom: 1px solid var(--border-muted);
  }

  .outline-header {
    color: var(--text-muted);
    font-size: 9px;
    text-transform: uppercase;
    letter-spacing: 0.6px;
    margin-bottom: 8px;
    font-weight: 500;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  .outline-count {
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 9px;
    text-transform: none;
    letter-spacing: 0;
  }

  .outline-state {
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.35;
    padding: 2px 0;
  }

  .outline-state.error {
    color: var(--slow-fg);
  }

  .outline-list {
    display: grid;
    gap: 4px;
  }

  .outline-item {
    width: 100%;
    min-height: 28px;
    display: grid;
    grid-template-columns: 16px minmax(0, 1fr) auto;
    align-items: center;
    gap: 6px;
    padding: 4px 6px;
    border-radius: var(--radius-sm);
    border: 1px solid transparent;
    background: transparent;
    color: var(--text-secondary);
    text-align: left;
    cursor: pointer;
    transition: background 0.12s, border-color 0.12s, color 0.12s;
  }

  .outline-item:hover {
    background: rgba(255, 255, 255, 0.035);
    color: var(--text-primary);
  }

  .outline-item.active {
    color: var(--text-primary);
    background: color-mix(in srgb, var(--accent-blue) 12%, transparent);
    border-color: color-mix(in srgb, var(--accent-blue) 32%, transparent);
  }

  .outline-item:focus-visible {
    outline: 2px solid var(--accent-blue);
    outline-offset: 2px;
  }

  .outline-icon {
    width: 16px;
    height: 16px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    color: var(--text-muted);
  }

  .outline-icon.shell {
    color: var(--accent-green);
  }

  .outline-preview {
    min-width: 0;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
    font-size: 11px;
    line-height: 1.35;
  }

  .outline-preview.shell {
    font-family: var(--font-mono);
    font-size: 10px;
  }

  .outline-ordinal {
    color: var(--text-muted);
    font-family: var(--font-mono);
    font-size: 9px;
  }
</style>
