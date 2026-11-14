<script lang="ts">
  import { Button, EmptyState } from "@kenn-io/kit-ui";
  import SessionFilterControl from "../filters/SessionFilterControl.svelte";
  import SidebarToggleButton from "../layout/SidebarToggleButton.svelte";
  import { ChartColumnIcon } from "../../icons.js";
  import { ui } from "../../stores/ui.svelte.js";
  import { m } from "../../i18n/index.js";
</script>

<!-- Shown in place of the dashboard until the reader opens it, so a page
     that only reads transcripts never builds the dashboard's charts. -->
<div class="dashboard-launcher">
  {#if !ui.isMobileViewport && !ui.sidebarOpen}
    <div class="launcher-toolbar">
      <div
        class="toolbar-filter-anchor"
        data-sidebar-focus-region="content"
      >
        <SidebarToggleButton placement="content" />
        <SessionFilterControl
          showDisplay={false}
          showStarred={false}
          align="left"
        />
      </div>
    </div>
  {/if}

  <EmptyState
    title={m.analytics_launcher_title()}
    description={m.analytics_launcher_description()}
  >
    {#snippet icon()}
      <ChartColumnIcon size="36" strokeWidth="1.5" aria-hidden="true" />
    {/snippet}
    <Button size="sm" onclick={() => ui.openDashboard()}>
      {m.analytics_launcher_open()}
    </Button>
  </EmptyState>
</div>

<style>
  .dashboard-launcher {
    flex: 1;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .launcher-toolbar {
    display: flex;
    align-items: center;
    padding: 8px 16px;
    background: var(--bg-surface);
    border-bottom: 1px solid var(--border-muted);
    flex-shrink: 0;
  }

  .toolbar-filter-anchor {
    position: relative;
    display: flex;
    align-items: center;
  }
</style>
