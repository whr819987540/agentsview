import { describe, expect, it } from "vite-plus/test";
import type {
  DbMessage as Message,
  ServiceBranchPoint as BranchPoint,
} from "../api/generated/index.js";
import type { DisplayItem } from "./display-items.js";
import { attachBranchPoints, branchSessionTitle } from "./branch-points.js";

function messageRow(ordinal: number): DisplayItem {
  return {
    kind: "message",
    message: { ordinal } as Message,
    ordinals: [ordinal],
  };
}

function toolRow(...ordinals: number[]): DisplayItem {
  return { kind: "tool-group", messages: [], ordinals, timestamp: "" };
}

function point(kind: BranchPoint["kind"], after: number): BranchPoint {
  return { kind, after_ordinal: after, after_turn: 0, turns_after: 0 };
}

function layout(markers: Map<number, { before: BranchPoint[]; after: BranchPoint[] }>) {
  return [...markers.entries()]
    .sort(([a], [b]) => a - b)
    .map(([index, m]) => ({
      index,
      before: m.before.map((p) => `${p.kind}@${p.after_ordinal}`),
      after: m.after.map((p) => `${p.kind}@${p.after_ordinal}`),
    }));
}

describe("attachBranchPoints", () => {
  it("attaches each point after the row it follows", () => {
    // Rows: 0, tools 1-3, 4 (5 is a hidden system row), 6.
    const items = [messageRow(0), toolRow(1, 2, 3), messageRow(4), messageRow(6)];
    const points = [
      point("fork", 0),
      point("rewind", 2),
      point("continuation", 5),
      point("fork", 9),
    ];
    expect(layout(attachBranchPoints(items, points))).toEqual([
      { index: 0, before: [], after: ["fork@0"] },
      { index: 1, before: [], after: ["rewind@2"] },
      { index: 2, before: [], after: ["continuation@5"] },
      { index: 3, before: [], after: ["fork@9"] },
    ]);
  });

  it("places a point before the first own row when it precedes every row", () => {
    const items = [messageRow(2), messageRow(3)];
    expect(layout(attachBranchPoints(items, [point("rewind", -1), point("rewind", 1)]))).toEqual([
      { index: 0, before: ["rewind@-1", "rewind@1"], after: [] },
    ]);
  });

  it("never attaches points to inherited rows", () => {
    // A fork view: inherited rows, the boundary at -1, then own rows.
    const items = [messageRow(-3), messageRow(-2), messageRow(-1), messageRow(0)];
    expect(layout(attachBranchPoints(items, [point("fork", 0)]))).toEqual([
      { index: 3, before: [], after: ["fork@0"] },
    ]);
  });

  it("attaches nothing when the transcript has no own rows", () => {
    expect(attachBranchPoints([messageRow(-2)], [point("fork", 0)]).size).toBe(0);
  });
});

describe("branchSessionTitle", () => {
  it.each([
    [{ display_name: "Named", first_message: "prompt" }, "Named"],
    [{ display_name: "", first_message: "  prompt " }, "prompt"],
    [{ first_message: null }, "Untitled"],
  ])("titles %j as %s", (session, want) => {
    expect(branchSessionTitle(session, "Untitled")).toBe(want);
  });
});
