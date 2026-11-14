<script lang="ts">
  import { Bar, Chart, Layer } from "layerchart";
  import { scaleBand } from "d3-scale";
  import type { DbSignalsTrendBucket as SignalsTrendBucket } from "../../api/generated/index.js";
  import { getGradeStyle, scoreToGrade } from "../../utils/grade.js";
  import { m } from "../../i18n/index.js";
  import LargeChartFrame from "../shared/LargeChartFrame.svelte";
  import ChartDataTable from "../shared/ChartDataTable.svelte";
  import { TableHeaderCell } from "@kenn-io/kit-ui";

  interface Props {
    trend: SignalsTrendBucket[];
  }

  let { trend }: Props = $props();

  let dataTableOpen = $state(false);

  const chartData = $derived(
    trend.map((bucket) => ({
      ...bucket,
      value: bucket.avg_health_score ?? 50,
      color: bucket.avg_health_score != null
        ? getGradeStyle(scoreToGrade(bucket.avg_health_score)).bg
        : getGradeStyle(null).bg,
    })),
  );

  const xTicks = $derived(
    trend.length > 1
      ? [trend[0]!.date, trend[trend.length - 1]!.date]
      : trend.map((bucket) => bucket.date),
  );

  let tooltip = $state<{
    x: number;
    y: number;
    text: string;
  } | null>(null);

  function barTitle(bucket: SignalsTrendBucket): string {
    const score = bucket.avg_health_score;
    return `${bucket.date}: ${score != null
      ? Math.round(score)
      : m.analytics_health_trend_no_scored_sessions()} (${m.analytics_session_shape_session_count({
        count: bucket.session_count,
        countLabel: bucket.session_count.toLocaleString(),
      })})`;
  }

  function showTooltip(event: PointerEvent, bucket: SignalsTrendBucket) {
    const rect = (event.currentTarget as SVGElement).getBoundingClientRect();
    tooltip = {
      x: rect.left + rect.width / 2,
      y: rect.top - 4,
      text: barTitle(bucket),
    };
  }
</script>

<div class="health-trend">
  <div class="chart-title">{m.analytics_health_trend_title()}</div>
  {#if trend.length > 0}
    <Chart
      data={chartData}
      x="date"
      y="value"
      xScale={scaleBand().paddingInner(0.16).paddingOuter(0.04)}
      yDomain={[0, 100]}
      padding={{ top: 8, right: 12, bottom: 22, left: 32 }}
      height={126}
      class="health-chart"
      role="img"
      aria-label={m.analytics_health_trend_title()}
      aria-describedby={dataTableOpen ? "health-trend-data" : undefined}
    >
      <Layer>
        <LargeChartFrame
          {xTicks}
          yTicks={[0, 50, 100]}
          formatX={(value) => String(value)}
          formatY={(value) => String(value)}
        >
          {#each chartData as bucket (bucket.date)}
            <Bar
              data={bucket}
              class="health-bar"
              fill={bucket.color}
              radius={2}
              onpointerenter={(event) => showTooltip(event, bucket)}
              onpointerleave={() => (tooltip = null)}
            />
          {/each}
        </LargeChartFrame>
      </Layer>
    </Chart>
    {#if tooltip}
      <div
        class="tooltip"
        style="left: {tooltip.x}px; top: {tooltip.y}px;"
      >
        <span>{tooltip.text}</span>
      </div>
    {/if}
    <div class="chart-caption">
      {m.analytics_health_trend_caption()}
    </div>
    <ChartDataTable
      id="health-trend-data"
      label={m.analytics_health_trend_title()}
      showLabel={m.analytics_chart_data_show()}
      hideLabel={m.analytics_chart_data_hide()}
      showAriaLabel={m.analytics_chart_data_show_for({ chart: m.analytics_health_trend_title() })}
      hideAriaLabel={m.analytics_chart_data_hide_for({ chart: m.analytics_health_trend_title() })}
      bind:open={dataTableOpen}
    >
      {#snippet header()}
        <TableHeaderCell label={m.analytics_skill_trend_date()} />
        <TableHeaderCell label={m.analytics_session_health_avg_score()} numeric />
        <TableHeaderCell label={m.analytics_col_sessions()} numeric />
      {/snippet}
      {#each trend as bucket (bucket.date)}
        <tr>
          <th scope="row">{bucket.date}</th>
          <td>
            {bucket.avg_health_score != null
              ? Math.round(bucket.avg_health_score)
              : m.analytics_health_trend_no_scored_sessions()}
          </td>
          <td>{bucket.session_count.toLocaleString()}</td>
        </tr>
      {/each}
    </ChartDataTable>
  {:else}
    <div class="empty">{m.analytics_no_trend_data()}</div>
  {/if}
</div>

<style>
  .chart-title {
    font-size: 12px;
    font-weight: 600;
    color: var(--text-primary);
    margin-bottom: 10px;
  }
  .health-trend :global(.health-chart) {
    display: block;
    width: 100%;
  }
  .tooltip {
    position: fixed;
    transform: translateX(-50%) translateY(-100%);
    padding: 4px 8px;
    background: var(--text-primary);
    color: var(--bg-primary);
    font-size: 10px;
    border-radius: var(--radius-sm);
    white-space: nowrap;
    pointer-events: none;
    z-index: var(--z-tooltip);
  }
  .chart-caption {
    font-size: 11px;
    color: var(--text-muted);
    margin-top: 6px;
  }
  .empty {
    color: var(--text-muted);
    font-size: 12px;
  }
</style>
