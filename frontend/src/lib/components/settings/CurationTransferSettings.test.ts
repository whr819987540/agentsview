// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
import { flushSync, mount, unmount } from "svelte";
// @ts-ignore
import CurationTransferSettings from "./CurationTransferSettings.svelte";
import { CurationService } from "../../api/generated/index";
import { ApiError } from "../../api/runtime.js";
import { settings } from "../../stores/settings.svelte.js";
import { starred } from "../../stores/starred.svelte.js";
import { sessionFolders } from "../../stores/sessionFolders.svelte.js";
import { sessionEndMarks } from "../../stores/sessionEndMarks.svelte.js";
import { saveCurationExport } from "./curationTransfer.js";

vi.mock("../../api/generated/index", async (importOriginal) => {
  const orig = await importOriginal<typeof import("../../api/generated/index")>();
  return {
    ...orig,
    CurationService: {
      getApiV1CurationExport: vi.fn(),
      postApiV1CurationImport: vi.fn(),
    },
  };
});

vi.mock("../../stores/settings.svelte.js", () => ({
  settings: { readOnly: false },
}));
vi.mock("../../stores/starred.svelte.js", () => ({ starred: { refresh: vi.fn() } }));
vi.mock("../../stores/sessionFolders.svelte.js", () => ({
  sessionFolders: { refresh: vi.fn() },
}));
vi.mock("../../stores/sessionEndMarks.svelte.js", () => ({
  sessionEndMarks: { refresh: vi.fn() },
}));

// The browser download is a DOM side effect covered in curationTransfer.test.ts.
vi.mock("./curationTransfer.js", async (importOriginal) => {
  const orig = await importOriginal<typeof import("./curationTransfer.js")>();
  return { ...orig, saveCurationExport: vi.fn() };
});

const curation = CurationService as unknown as {
  getApiV1CurationExport: ReturnType<typeof vi.fn>;
  postApiV1CurationImport: ReturnType<typeof vi.fn>;
};
const refreshes = [starred.refresh, sessionFolders.refresh, sessionEndMarks.refresh] as Array<
  ReturnType<typeof vi.fn>
>;

const exportDoc = {
  format: "agentsview-curation",
  version: 1,
  sessions: [
    { id: "s1", agent: "claude", project: "alpha", starred: true },
    { id: "s2", agent: "claude", project: "alpha", folder: "Docs" },
    { id: "gone", agent: "claude", project: "alpha", starred: true },
  ],
  folders: [{ project: "alpha", name: "Docs" }],
};

function counts(added: number, unchanged: number, kept: number) {
  return { added, unchanged, kept };
}

function result(overrides: Record<string, unknown> = {}) {
  return {
    dry_run: true,
    sessions: 3,
    matched: 2,
    matched_by_prefix: 1,
    unmatched: ["gone"],
    ambiguous: [],
    stars: counts(1, 0, 0),
    end_marks: counts(0, 1, 0),
    folder_members: counts(0, 0, 1),
    folders_created: 1,
    ...overrides,
  };
}

async function settle(): Promise<void> {
  for (let i = 0; i < 3; i++) {
    await new Promise((resolve) => setTimeout(resolve, 0));
    flushSync();
  }
}

function button(label: string): HTMLButtonElement {
  const found = Array.from(document.body.querySelectorAll("button")).find(
    (b) => b.textContent?.trim() === label,
  );
  if (!found) throw new Error(`no button "${label}"`);
  return found;
}

function hasButton(label: string): boolean {
  return Array.from(document.body.querySelectorAll("button")).some(
    (b) => b.textContent?.trim() === label,
  );
}

async function chooseFile(name: string, content: string): Promise<void> {
  const input = document.body.querySelector<HTMLInputElement>('input[type="file"]')!;
  Object.defineProperty(input, "files", {
    value: [new File([content], name, { type: "application/json" })],
    configurable: true,
  });
  // Browsers bubble change events, which Svelte delegates to the root.
  input.dispatchEvent(new Event("change", { bubbles: true }));
  await settle();
}

function rowCells(key: string): string[] {
  const row = document.body.querySelector(`tr[data-row="${key}"]`);
  return Array.from(row?.querySelectorAll("td") ?? []).map((td) => td.textContent?.trim() ?? "");
}

function headerLabels(): string[] {
  return Array.from(document.body.querySelectorAll("th")).map((th) => th.textContent?.trim() ?? "");
}

function alertText(): string {
  return document.body.querySelector('[role="alert"]')?.textContent?.trim() ?? "";
}

describe("CurationTransferSettings", () => {
  let component: ReturnType<typeof mount>;

  beforeEach(() => {
    curation.getApiV1CurationExport.mockReset();
    curation.postApiV1CurationImport.mockReset();
    for (const refresh of refreshes) refresh.mockReset().mockResolvedValue(undefined);
    vi.mocked(saveCurationExport).mockReset();
    (settings as { readOnly: boolean }).readOnly = false;
    component = mount(CurationTransferSettings, { target: document.body });
  });

  afterEach(() => {
    unmount(component);
    vi.useRealTimers();
    document.body.innerHTML = "";
  });

  it("makes no request until the user acts", () => {
    expect(curation.getApiV1CurationExport).not.toHaveBeenCalled();
    expect(curation.postApiV1CurationImport).not.toHaveBeenCalled();
  });

  it("exports through the API and saves a dated file", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 10, 24, 9, 30));
    curation.getApiV1CurationExport.mockResolvedValue(exportDoc);

    button("Export to file").click();
    await settle();

    expect(curation.getApiV1CurationExport).toHaveBeenCalledTimes(1);
    expect(saveCurationExport).toHaveBeenCalledWith(exportDoc, "agentsview-curation-2026-11-24.json");
    const summary = document.body.querySelector('[role="status"]')?.textContent ?? "";
    expect(summary).toContain("agentsview-curation-2026-11-24.json");
    expect(summary).toMatch(/Curated sessions\s*3/);
    expect(summary).toMatch(/Folders\s*1/);
  });

  it("explains a replica server once and turns both actions off", async () => {
    curation.getApiV1CurationExport.mockRejectedValue(new ApiError(501, "not available in remote mode"));

    button("Export to file").click();
    await settle();

    expect(document.body.textContent).toContain(
      "Moving stars and folders works only when this server reads its local archive.",
    );
    expect(alertText()).toBe("");
    expect(button("Export to file").disabled).toBe(true);
    expect(button("Choose file…").disabled).toBe(true);
  });

  it("previews a chosen export, then imports it and refreshes the sidebar", async () => {
    curation.postApiV1CurationImport.mockResolvedValueOnce(result());

    await chooseFile("curation.json", JSON.stringify(exportDoc));

    expect(curation.postApiV1CurationImport).toHaveBeenCalledTimes(1);
    const [sentDoc, params] = curation.postApiV1CurationImport.mock.calls[0]!;
    expect(sentDoc).toEqual(exportDoc);
    expect(params).toEqual({ dry_run: true });
    expect(document.body.textContent).toContain("curation.json");
    expect(document.body.textContent).toMatch(/Matched sessions\s*2 of 3/);
    expect(document.body.textContent).toMatch(/Matched through a prefixed ID\s*1/);
    expect(document.body.textContent).toMatch(/Folders to create\s*1/);
    expect(headerLabels()).toEqual(["Item", "To add", "Already here", "Kept existing"]);
    expect(rowCells("stars")).toEqual(["Stars", "1", "0", "0"]);
    expect(rowCells("end-marks")).toEqual(["Ended marks", "0", "1", "0"]);
    expect(rowCells("folder-members")).toEqual(["Sessions in folders", "0", "0", "1"]);
    const unmatched = document.body.querySelector('[data-group="unmatched"]');
    expect(unmatched?.textContent).toContain("Not found here (1)");
    expect(unmatched?.querySelector("li")?.textContent).toBe("gone");
    for (const refresh of refreshes) expect(refresh).not.toHaveBeenCalled();

    curation.postApiV1CurationImport.mockResolvedValueOnce(result({ dry_run: false }));
    button("Import").click();
    await settle();

    expect(curation.postApiV1CurationImport).toHaveBeenCalledTimes(2);
    expect(curation.postApiV1CurationImport.mock.calls[1]).toEqual([exportDoc]);
    for (const refresh of refreshes) expect(refresh).toHaveBeenCalledTimes(1);
    expect(document.body.textContent).toContain(
      "Import complete. The sidebar now shows the imported stars, ended marks, and folders.",
    );
    expect(headerLabels()[1]).toBe("Added");
    expect(document.body.textContent).toMatch(/Folders created\s*1/);
    expect(hasButton("Import")).toBe(false);
  });

  it.each([
    {
      name: "a file that is not an export",
      content: '{"format":"recall","version":1}',
      message: "This file is not an AgentsView export of stars and folders.",
    },
    {
      name: "an export from a newer version",
      content: '{"format":"agentsview-curation","version":2,"sessions":[]}',
      message:
        "This file was exported by a newer version of AgentsView. Update this machine, then try again.",
    },
  ])("rejects $name without asking the server", async ({ content, message }) => {
    await chooseFile("other.json", content);

    expect(curation.postApiV1CurationImport).not.toHaveBeenCalled();
    expect(alertText()).toBe(message);
    expect(document.body.querySelector("table")).toBeNull();
  });

  it("shows the server's reason when it rejects the file", async () => {
    curation.postApiV1CurationImport.mockRejectedValue(
      new ApiError(400, "invalid curation export: session s1 has end mark kind \"all\""),
    );

    await chooseFile("curation.json", JSON.stringify(exportDoc));

    expect(alertText()).toBe('invalid curation export: session s1 has end mark kind "all"');
    expect(hasButton("Import")).toBe(false);
  });

  it("offers no import when this archive already has everything", async () => {
    curation.postApiV1CurationImport.mockResolvedValue(
      result({
        stars: counts(0, 2, 0),
        end_marks: counts(0, 1, 0),
        folder_members: counts(0, 0, 1),
        folders_created: 0,
      }),
    );

    await chooseFile("curation.json", JSON.stringify(exportDoc));

    expect(document.body.textContent).toContain("This archive already has everything in this file.");
    expect(hasButton("Import")).toBe(false);
  });

  it("lists ten skipped sessions and counts the rest", async () => {
    const ids = Array.from({ length: 12 }, (_, i) => `u${String(i + 1).padStart(2, "0")}`);
    curation.postApiV1CurationImport.mockResolvedValue(result({ unmatched: ids, ambiguous: ["twin"] }));

    await chooseFile("curation.json", JSON.stringify(exportDoc));

    const unmatched = document.body.querySelector('[data-group="unmatched"]')!;
    expect(Array.from(unmatched.querySelectorAll("li")).map((li) => li.textContent)).toEqual(
      ids.slice(0, 10),
    );
    expect(unmatched.textContent).toContain("Not found here (12)");
    expect(unmatched.textContent).toContain("and 2 more");
    const ambiguous = document.body.querySelector('[data-group="ambiguous"]')!;
    expect(ambiguous.textContent).toContain("Matched more than one session (1)");
    expect(ambiguous.querySelector("li")?.textContent).toBe("twin");
  });

  it("keeps the preview and says nothing changed when the import fails", async () => {
    curation.postApiV1CurationImport
      .mockResolvedValueOnce(result())
      .mockRejectedValueOnce(new ApiError(500, "disk full"));

    await chooseFile("curation.json", JSON.stringify(exportDoc));
    button("Import").click();
    await settle();

    expect(alertText()).toBe("disk full Import failed, and nothing was changed.");
    expect(button("Import").disabled).toBe(false);
    expect(rowCells("stars")).toEqual(["Stars", "1", "0", "0"]);
    for (const refresh of refreshes) expect(refresh).not.toHaveBeenCalled();
  });

  it("turns imports off on a read-only archive but still exports", async () => {
    unmount(component);
    document.body.innerHTML = "";
    (settings as { readOnly: boolean }).readOnly = true;
    component = mount(CurationTransferSettings, { target: document.body });
    flushSync();

    expect(document.body.textContent).toContain("This archive is read-only, so imports are turned off.");
    expect(button("Choose file…").disabled).toBe(true);
    expect(button("Export to file").disabled).toBe(false);
  });
});
