// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import {
  curationExportFileName,
  parseCurationExport,
  saveCurationExport,
} from "./curationTransfer.js";

const session = { id: "s1", agent: "claude", project: "alpha", starred: true };

describe("parseCurationExport", () => {
  it.each([
    { name: "text that is not JSON", text: "stars: s1", reason: "not-export" },
    { name: "a JSON array", text: "[]", reason: "not-export" },
    { name: "another document", text: '{"format":"recall","version":1,"sessions":[]}', reason: "not-export" },
    { name: "a version that is not a number", text: '{"format":"agentsview-curation","version":"1","sessions":[]}', reason: "not-export" },
    { name: "a newer version", text: '{"format":"agentsview-curation","version":2,"sessions":[]}', reason: "newer-version" },
    { name: "version zero", text: '{"format":"agentsview-curation","version":0,"sessions":[]}', reason: "not-export" },
    { name: "sessions that are not a list", text: '{"format":"agentsview-curation","version":1,"sessions":{}}', reason: "not-export" },
    { name: "folders that are not a list", text: '{"format":"agentsview-curation","version":1,"sessions":[],"folders":"Docs"}', reason: "not-export" },
  ])("rejects $name", ({ text, reason }) => {
    expect(parseCurationExport(text)).toEqual({ ok: false, reason });
  });

  it("accepts an export and keeps its entries", () => {
    const text = JSON.stringify({
      format: "agentsview-curation",
      version: 1,
      artifact_origin: "origin-a",
      sessions: [session],
      folders: [{ project: "alpha", name: "Docs" }],
    });
    expect(parseCurationExport(text)).toEqual({
      ok: true,
      doc: {
        format: "agentsview-curation",
        version: 1,
        artifact_origin: "origin-a",
        sessions: [session],
        folders: [{ project: "alpha", name: "Docs" }],
      },
    });
  });

  it("treats a missing folder list as empty, since the server requires one", () => {
    const check = parseCurationExport(
      JSON.stringify({ format: "agentsview-curation", version: 1, sessions: [session] }),
    );
    expect(check).toEqual({
      ok: true,
      doc: { format: "agentsview-curation", version: 1, sessions: [session], folders: [] },
    });
  });
});

describe("curationExportFileName", () => {
  it("names the file after the local date with zero-padded parts", () => {
    expect(curationExportFileName(new Date(2026, 0, 5, 23, 59))).toBe(
      "agentsview-curation-2026-01-05.json",
    );
  });
});

describe("saveCurationExport", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("downloads the document as indented JSON under the given name", async () => {
    vi.useFakeTimers();
    let saved: Blob | null = null;
    const createObjectURL = vi.fn((blob: Blob) => {
      saved = blob;
      return "blob:curation";
    });
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", { ...URL, createObjectURL, revokeObjectURL });
    const clicks: Array<{ href: string; download: string }> = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clicks.push({ href: this.href, download: this.download });
    });

    const doc = { format: "agentsview-curation", version: 1, sessions: [session], folders: [] };
    saveCurationExport(doc, "agentsview-curation-2026-01-05.json");

    expect(clicks).toEqual([
      { href: "blob:curation", download: "agentsview-curation-2026-01-05.json" },
    ]);
    expect(saved).not.toBeNull();
    const text = await saved!.text();
    expect(text).toBe(`${JSON.stringify(doc, null, 2)}\n`);
    expect(saved!.type).toBe("application/json");
    expect(revokeObjectURL).not.toHaveBeenCalled();
    vi.advanceTimersByTime(100);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:curation");
    vi.unstubAllGlobals();
  });
});
