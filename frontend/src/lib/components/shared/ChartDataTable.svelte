<script lang="ts">
  import { Button, Table } from "@kenn-io/kit-ui";
  import type { Snippet } from "svelte";

  interface Props {
    /** Id of the table region. While the table is open, the chart points
     *  aria-describedby here. */
    id: string;
    /** The table's accessible name, usually the chart title. */
    label: string;
    /** Visible button text while the table is closed and open. */
    showLabel: string;
    hideLabel: string;
    /** Button names that also name the chart, for screen readers. */
    showAriaLabel: string;
    hideAriaLabel: string;
    /** Whether the table is shown. Bind it so the chart is described by
     *  the table only while the table exists. */
    open?: boolean;
    /** Header cells: <TableHeaderCell> or <th> elements. */
    header: Snippet;
    /** Body rows: <tr> elements. */
    children: Snippet;
  }

  let {
    id,
    label,
    showLabel,
    hideLabel,
    showAriaLabel,
    hideAriaLabel,
    open = $bindable(false),
    header,
    children,
  }: Props = $props();
</script>

<!-- A chart's values as a table, rendered only on request: a long trend has
     hundreds of rows that would otherwise sit in the DOM unseen. -->
<div class="chart-data">
  <Button
    size="sm"
    surface="soft"
    ariaExpanded={open}
    ariaLabel={open ? hideAriaLabel : showAriaLabel}
    onclick={() => (open = !open)}
  >
    {open ? hideLabel : showLabel}
  </Button>
  {#if open}
    <div {id} class="chart-data-table">
      <Table ariaLabel={label} {header}>
        {@render children()}
      </Table>
    </div>
  {/if}
</div>

<style>
  .chart-data {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 6px;
    margin-top: 6px;
  }

  .chart-data-table {
    display: flex;
    width: 100%;
    max-height: 240px;
  }

  /* Row headers carry the date; match kit-ui's body cell spacing. */
  .chart-data-table :global(tbody th) {
    padding: 6px 10px;
    font-size: var(--font-size-sm);
    font-weight: normal;
    text-align: left;
    color: var(--text-secondary);
    border-bottom: 1px solid var(--border-muted);
  }
</style>
