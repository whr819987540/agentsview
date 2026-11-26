<script lang="ts">
  import { Button } from "@kenn-io/kit-ui";
  import type {
    DbMessage as Message,
    ServiceBranchLeave as BranchLeave,
  } from "../../api/generated/index.js";
  import { formatNumber, formatTimestamp } from "../../utils/format.js";
  import { leaveLabel } from "../../utils/branch-points.js";
  import { LinkIcon } from "../../icons.js";
  import { m } from "../../i18n/index.js";

  interface Props {
    message: Message;
    /** Where the fork leaves its parent, when the archive can place it. */
    leave?: BranchLeave | null;
    /** Inherited messages above the boundary. */
    inheritedCount?: number;
    /** Whether the inherited messages are hidden. */
    collapsed?: boolean;
    onToggle?: () => void;
  }

  let {
    message,
    leave = null,
    inheritedCount = 0,
    collapsed = false,
    onToggle,
  }: Props = $props();

  let note = $derived(
    collapsed ? m.fork_boundary_note_collapsed() : m.fork_boundary_note(),
  );

  function toggle(event: MouseEvent) {
    // The row under the boundary selects its message on click.
    event.stopPropagation();
    onToggle?.();
  }
</script>

<div class="fork-boundary" title={m.fork_boundary_title()}>
  <span class="fork-line"></span>
  <span class="fork-label">
    <span class="fork-icon">
      <LinkIcon size="12" strokeWidth="2.2" aria-hidden="true" />
    </span>
    {m.fork_boundary_label()}
    {#if message.timestamp}
      <span class="fork-time">
        &middot; {formatTimestamp(message.timestamp)}
      </span>
    {/if}
  </span>
  <span class="fork-line"></span>
</div>
<div class="fork-note">
  {#if leave}
    <span class="fork-leave">{leaveLabel(leave)}</span>
  {/if}
  <span>{note}</span>
  {#if onToggle && inheritedCount > 0}
    <Button size="sm" surface="soft" onclick={toggle}>
      {collapsed
        ? m.fork_boundary_show_inherited({
            count: inheritedCount,
            countLabel: formatNumber(inheritedCount),
          })
        : m.fork_boundary_hide_inherited()}
    </Button>
  {/if}
</div>

<style>
  .fork-boundary {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 6px 12px 0;
    color: var(--accent-blue);
    font-size: 11px;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-weight: 600;
  }

  .fork-line {
    flex: 1;
    height: 1px;
    background: color-mix(
      in srgb, var(--accent-blue) 35%, transparent
    );
  }

  .fork-label {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    white-space: nowrap;
  }

  .fork-icon {
    display: inline-flex;
    align-items: center;
  }

  .fork-time {
    color: var(--text-muted);
    font-weight: 500;
    text-transform: none;
    letter-spacing: 0;
  }

  .fork-note {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-3) var(--space-5);
    margin: 4px 12px 6px;
    padding: 6px 10px;
    background: color-mix(
      in srgb, var(--accent-blue) 8%, transparent
    );
    border-left: 2px solid
      color-mix(in srgb, var(--accent-blue) 50%, transparent);
    color: var(--text-secondary);
    font-size: 12px;
    line-height: 1.5;
    border-radius: 0 var(--radius-sm, 4px)
      var(--radius-sm, 4px) 0;
  }

  .fork-leave {
    flex-basis: 100%;
    color: var(--text-primary);
    font-weight: 500;
  }
</style>
