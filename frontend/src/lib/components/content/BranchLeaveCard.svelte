<script lang="ts">
  import type { ServiceBranchLeave as BranchLeave } from "../../api/generated/index.js";
  import { m } from "../../i18n/index.js";
  import { CornerDownRightIcon, GitForkIcon } from "../../icons.js";
  import { router } from "../../stores/router.svelte.js";
  import { leaveLabel } from "../../utils/branch-points.js";
  import { formatNumber } from "../../utils/format.js";

  interface Props {
    leave: BranchLeave;
  }

  let { leave }: Props = $props();

  let label = $derived(leaveLabel(leave));
  // A continuation that resumed from an earlier turn leaves the parent's
  // later turns behind; a fork always does, so it does not say so.
  let skipped = $derived(
    leave.relationship_type === "continuation" ? leave.turns_after : 0,
  );
  let params = $derived({ msg: String(leave.after_ordinal) });

  function open(event: MouseEvent & { currentTarget: HTMLAnchorElement }) {
    event.stopPropagation();
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
    router.navigateToSession(leave.parent.id, params);
  }
</script>

<div class="branch-leave" data-relationship={leave.relationship_type}>
  <span class="leave-icon" aria-hidden="true">
    {#if leave.relationship_type === "fork"}
      <GitForkIcon size="12" strokeWidth="2.2" />
    {:else}
      <CornerDownRightIcon size="12" strokeWidth="2.2" />
    {/if}
  </span>
  <span class="leave-label">{label}</span>
  <a
    class="leave-open"
    href={router.buildSessionHref(leave.parent.id, params)}
    onclick={open}
  >
    {leave.handoff ? m.branch_leave_open_handoff() : m.branch_leave_open_parent()}
  </a>
  {#if skipped > 0}
    {@const counts = { count: skipped, countLabel: formatNumber(skipped) }}
    <span class="leave-skipped">
      {leave.handoff
        ? m.branch_leave_handoff_rewound(counts)
        : m.branch_leave_skipped(counts)}
    </span>
  {/if}
</div>

<style>
  .branch-leave {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 6px;
    margin: 6px 12px;
    padding: 6px 10px;
    border-left: 3px solid
      color-mix(in srgb, var(--accent-blue) 55%, transparent);
    background: color-mix(in srgb, var(--accent-blue) 6%, transparent);
    color: var(--text-secondary);
    font-size: 12px;
  }

  .leave-icon {
    display: inline-flex;
    align-self: center;
    color: var(--accent-blue);
  }

  .leave-open {
    color: var(--accent-blue);
    text-decoration: none;
  }

  .leave-open:hover {
    text-decoration: underline;
  }

  .leave-skipped {
    flex-basis: 100%;
    color: var(--text-muted);
  }
</style>
