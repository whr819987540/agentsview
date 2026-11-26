import { describe, expect, it } from "vite-plus/test";
import type { DbMessage as Message } from "../api/generated/index.js";
import type { DisplayItem } from "./display-items.js";
import {
  countInheritedMessages,
  hideInheritedRows,
} from "./inherited-context.js";

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

// A fork view: two inherited messages and a tool group, the boundary row at
// -1, then the fork's own rows.
const forkRows = [
  messageRow(-5),
  toolRow(-4, -3),
  messageRow(-2),
  messageRow(-1),
  messageRow(0),
  toolRow(1, 2),
];

describe("hideInheritedRows", () => {
  it("keeps the boundary and own rows while collapsed", () => {
    expect(hideInheritedRows(forkRows, true).map((item) => item.ordinals)).toEqual([
      [-1],
      [0],
      [1, 2],
    ]);
  });

  it("shows every row when expanded", () => {
    expect(hideInheritedRows(forkRows, false)).toBe(forkRows);
  });

  it("returns the same rows when none are inherited", () => {
    const own = [messageRow(0), messageRow(1)];
    expect(hideInheritedRows(own, true)).toBe(own);
  });
});

describe("countInheritedMessages", () => {
  it("counts inherited messages, not rows or the boundary", () => {
    expect(countInheritedMessages(forkRows)).toBe(4);
  });
});
