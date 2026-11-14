// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { mount, tick, unmount } from "svelte";
import { analytics } from "../../stores/analytics.svelte.js";
import type { DbSignalsAnalyticsResponse as SignalsAnalyticsResponse } from "../../api/generated/index.js";
import SessionHealthSection from "./SessionHealthSection.svelte";

function projectRow(index: number) {
  return {
    project: `project-${String(index).padStart(2, "0")}`,
    // A project's session count is its index, so the busiest project comes
    // last in the input and first in the table.
    session_count: index,
    avg_health_score: 80,
    completed_rate: 50,
    avg_failure_signals: 0,
  };
}

function signalsWithProjects(count: number): SignalsAnalyticsResponse {
  return {
    scored_sessions: 10,
    unscored_sessions: 0,
    grade_distribution: { A: 10 },
    avg_health_score: 80,
    outcome_distribution: { completed: 10 },
    outcome_confidence_distribution: { high: 10 },
    tool_health: {
      total_failure_signals: 0,
      total_retries: 0,
      total_edit_churn: 0,
      sessions_with_failures: 0,
      failure_rate: 0,
    },
    context_health: {
      avg_compaction_count: 0,
      sessions_with_compaction: 0,
      mid_task_compaction_count: 0,
      sessions_with_mid_task_compaction: 0,
      sessions_with_context_data: 0,
      avg_context_pressure: 0,
      high_pressure_sessions: 0,
    },
    quality_health: {
      computed_sessions: 0,
      totals: {
        short_prompt_count: 0,
        unstructured_start: 0,
        missing_success_criteria_count: 0,
        missing_verification_count: 0,
        duplicate_prompt_count: 0,
        no_code_context_count: 0,
        runaway_tool_loop_count: 0,
        frustration_marker_count: 0,
      },
      sessions_with_signal: {
        short_prompt_count: 0,
        unstructured_start: 0,
        missing_success_criteria_count: 0,
        missing_verification_count: 0,
        duplicate_prompt_count: 0,
        no_code_context_count: 0,
        runaway_tool_loop_count: 0,
        frustration_marker_count: 0,
      },
    },
    trend: [],
    by_agent: [],
    by_project: Array.from({ length: count }, (_, i) => projectRow(i + 1)),
    calibration: {},
  };
}

function projectTable(): HTMLTableElement {
  const title = [...document.querySelectorAll(".table-title")].find(
    (el) => el.textContent === "By Project",
  );
  return title!.closest(".mini-table")!.querySelector("table")!;
}

function projectNames(): string[] {
  return [...projectTable().querySelectorAll("tbody tr td:first-child")].map(
    (cell) => cell.textContent ?? "",
  );
}

function toggleButton(): HTMLButtonElement | undefined {
  return [...document.querySelectorAll<HTMLButtonElement>(".table-more button")][0];
}

describe("SessionHealthSection project table", () => {
  let component: ReturnType<typeof mount> | undefined;

  afterEach(() => {
    if (component) unmount(component);
    component = undefined;
    analytics.signals = null;
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("starts with the 20 busiest projects and expands to all of them", async () => {
    analytics.signals = signalsWithProjects(25);
    component = mount(SessionHealthSection, { target: document.body });
    await tick();

    const shown = projectNames();
    expect(shown).toHaveLength(20);
    expect(shown[0]).toBe("project-25");
    expect(shown[19]).toBe("project-06");

    const toggle = toggleButton()!;
    expect(toggle.textContent?.trim()).toBe("Show all 25 projects");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");

    toggle.click();
    await tick();
    expect(projectNames()).toHaveLength(25);
    expect(projectNames()[24]).toBe("project-01");
    expect(toggle.textContent?.trim()).toBe("Show fewer");
    expect(toggle.getAttribute("aria-expanded")).toBe("true");

    toggle.click();
    await tick();
    expect(projectNames()).toHaveLength(20);
  });

  it("shows every project without a toggle when they fit", async () => {
    analytics.signals = signalsWithProjects(20);
    component = mount(SessionHealthSection, { target: document.body });
    await tick();

    expect(projectNames()).toHaveLength(20);
    expect(toggleButton()).toBeUndefined();
  });
});
