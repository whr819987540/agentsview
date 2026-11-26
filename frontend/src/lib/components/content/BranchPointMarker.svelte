<script lang="ts">
  import type { ServiceBranchPoint as BranchPoint } from "../../api/generated/index.js";
  import { m } from "../../i18n/index.js";
  import {
    CornerDownRightIcon,
    GitForkIcon,
    Undo2Icon,
  } from "../../icons.js";
  import { router } from "../../stores/router.svelte.js";
  import { branchSessionTitle } from "../../utils/branch-points.js";
  import { formatNumber } from "../../utils/format.js";

  interface Props {
    point: BranchPoint;
  }

  let { point }: Props = $props();

  let title = $derived(
    point.session
      ? branchSessionTitle(point.session, m.session_tree_untitled())
      : "",
  );
  let turns = $derived(point.session?.user_message_count ?? 0);
  let inputs = $derived(point.inputs ?? []);
  let label = $derived.by(() => {
    switch (point.kind) {
      case "fork":
        return m.branch_marker_fork({ title });
      case "continuation":
        return point.turns_after > 0
          ? m.branch_marker_continuation_rewound({
              title,
              count: point.turns_after,
              countLabel: formatNumber(point.turns_after),
            })
          : m.branch_marker_continuation({ title });
      default:
        return m.branch_marker_rewind({
          count: inputs.length,
          countLabel: formatNumber(inputs.length),
        });
    }
  });

  function open(
    event: MouseEvent & { currentTarget: HTMLAnchorElement },
    id: string,
  ) {
    // The row under the marker selects its message on click.
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
    router.navigateToSession(id);
  }
</script>

<div class="branch-marker" data-branch-kind={point.kind}>
  <span class="branch-icon" aria-hidden="true">
    {#if point.kind === "fork"}
      <GitForkIcon size="12" strokeWidth="2.2" />
    {:else if point.kind === "continuation"}
      <CornerDownRightIcon size="12" strokeWidth="2.2" />
    {:else}
      <Undo2Icon size="12" strokeWidth="2.2" />
    {/if}
  </span>
  {#if point.session}
    <a
      class="branch-link"
      href={router.buildSessionHref(point.session.id)}
      onclick={(event) => open(event, point.session!.id)}
    >
      {label}
    </a>
    <span class="branch-meta">
      {turns > 0
        ? m.branch_marker_turns({ count: turns, countLabel: formatNumber(turns) })
        : m.branch_marker_no_turns()}
    </span>
  {:else}
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <details class="branch-inputs" onclick={(event) => event.stopPropagation()}>
      <summary>{label}</summary>
      <ol>
        {#each inputs as input, i (i)}
          <li><pre>{input}</pre></li>
        {/each}
      </ol>
    </details>
  {/if}
</div>

<style>
  .branch-marker {
    display: flex;
    align-items: baseline;
    flex-wrap: wrap;
    gap: 6px;
    margin: 2px 12px 6px;
    padding: 4px 10px;
    border-left: 2px dashed
      color-mix(in srgb, var(--accent-blue) 55%, transparent);
    color: var(--text-muted);
    font-size: 12px;
  }

  .branch-marker[data-branch-kind="rewind"] {
    border-left-color: color-mix(
      in srgb, var(--text-muted) 45%, transparent
    );
  }

  .branch-icon {
    display: inline-flex;
    align-self: center;
    color: var(--accent-blue);
  }

  .branch-marker[data-branch-kind="rewind"] .branch-icon {
    color: var(--text-muted);
  }

  .branch-link {
    color: var(--accent-blue);
    text-decoration: none;
  }

  .branch-link:hover {
    text-decoration: underline;
  }

  .branch-meta {
    color: var(--text-muted);
  }

  .branch-inputs summary {
    cursor: pointer;
  }

  .branch-inputs ol {
    margin: 4px 0 0;
    padding-left: 20px;
  }

  .branch-inputs pre {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    font-family: inherit;
    color: var(--text-secondary);
  }
</style>
