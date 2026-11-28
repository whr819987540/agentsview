<script lang="ts">
  /**
   * Tree lines to the left of a sidebar row below a group's row: a line for
   * each ancestor level that runs past the row, and an elbow from the
   * parent's line into the row. Each row draws its own segments, so the
   * lines stay whole in the virtualized list. The row must be positioned.
   */
  interface Props {
    /** One entry per level from 1 to the row's depth: below the row's own
     *  level, whether that level's line runs past the row; at its own level,
     *  whether a sibling follows. */
    guides: readonly boolean[];
    /** Extra indent of the row in px, such as the folders around it. */
    offset?: number;
    /** How far the elbow reaches past the start of the row's indent, in px:
     *  to just before the row's first mark. */
    reach: number;
  }

  let { guides, offset = 0, reach }: Props = $props();

  // Rows indent 16 px per level after 8 px of padding, and start with a
  // 16 px toggle column, so a level's line runs down the middle of its
  // parent's toggle column.
  const railX = (level: number) => offset + 16 * level;
  let depth = $derived(guides.length);
</script>

{#each guides.slice(0, -1) as runsOn, i (i)}
  {#if runsOn}
    <span class="tree-guide rail" style:left="{railX(i + 1)}px" aria-hidden="true"></span>
  {/if}
{/each}
{#if depth > 0}
  <span
    class="tree-guide rail"
    class:end={!guides[depth - 1]}
    style:left="{railX(depth)}px"
    aria-hidden="true"
  ></span>
  <span
    class="tree-guide elbow"
    style:left="{railX(depth)}px"
    style:width="{8 + reach}px"
    aria-hidden="true"
  ></span>
{/if}

<style>
  .tree-guide {
    position: absolute;
    pointer-events: none;
    background: var(--border-default);
  }

  .rail {
    top: 0;
    bottom: 0;
    width: 1px;
  }

  .rail.end {
    bottom: 50%;
  }

  .elbow {
    top: 50%;
    height: 1px;
  }
</style>
