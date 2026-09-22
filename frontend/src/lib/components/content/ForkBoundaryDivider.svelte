<script lang="ts">
  import type { DbMessage as Message } from "../../api/generated/index.js";
  import { formatTimestamp } from "../../utils/format.js";
  import { LinkIcon } from "../../icons.js";
  import { m } from "../../i18n/index.js";

  interface Props {
    message: Message;
  }

  let { message }: Props = $props();
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
<div class="fork-note">{m.fork_boundary_note()}</div>

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
</style>
