// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { mount, tick, unmount } from "svelte";
import HealthTrend from "./HealthTrend.svelte";

describe("HealthTrend", () => {
  it("renders health buckets as an SVG chart with grade colors", async () => {
    const component = mount(HealthTrend, {
      target: document.body,
      props: {
        trend: [
          {
            date: "2026-08-18",
            session_count: 4,
            avg_health_score: 92,
            completed: 3,
            errored: 0,
            abandoned: 1,
            avg_failure_signals: 0.25,
          },
          {
            date: "2026-08-19",
            session_count: 2,
            avg_health_score: 58,
            completed: 1,
            errored: 1,
            abandoned: 0,
            avg_failure_signals: 1,
          },
        ],
      },
    });
    await tick();

    expect(document.querySelector("svg")).not.toBeNull();
    const bars = document.querySelectorAll<SVGRectElement>("rect.health-bar");
    expect(bars).toHaveLength(2);
    expect(bars[0]!.getAttribute("fill")).not.toBe(bars[1]!.getAttribute("fill"));

    bars[0]!.dispatchEvent(
      new MouseEvent("pointerenter", {
        bubbles: true,
      }),
    );
    await tick();
    expect(document.querySelector(".tooltip")?.textContent).toContain(
      "2026-08-18: 92 (4 sessions)",
    );
    // The data table renders only on request.
    const chart = document.querySelector<HTMLElement>("[aria-label='Health Trend'][role='img']");
    expect(document.querySelector("table")).toBeNull();
    expect(chart?.hasAttribute("aria-describedby")).toBe(false);
    const toggle = document.querySelector<HTMLButtonElement>(
      "button[aria-label='Show data table for Health Trend']",
    )!;
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    toggle.click();
    await tick();
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(chart?.getAttribute("aria-describedby")).toBe("health-trend-data");
    const accessibleRows = document.querySelectorAll("#health-trend-data table tbody tr");
    expect(accessibleRows).toHaveLength(2);
    expect(accessibleRows[0]?.textContent).toContain("2026-08-18");
    expect(accessibleRows[0]?.textContent).toContain("92");
    expect(accessibleRows[0]?.textContent).toContain("4");

    toggle.click();
    await tick();
    expect(document.querySelector("table")).toBeNull();
    expect(chart?.hasAttribute("aria-describedby")).toBe(false);

    await unmount(component);
  });
});
