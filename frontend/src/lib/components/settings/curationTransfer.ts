import type { DbCurationExport } from "../../api/generated/index";

/** The `format` every curation export carries. */
export const CURATION_EXPORT_FORMAT = "agentsview-curation";
/** The export version this build reads and writes. */
export const CURATION_EXPORT_VERSION = 1;
/** How many unmatched or ambiguous session IDs the import preview lists. */
export const CURATION_ID_PREVIEW_LIMIT = 10;

export type CurationFileCheck =
  | { ok: true; doc: DbCurationExport }
  | { ok: false; reason: "not-export" | "newer-version" };

/**
 * Check that a chosen file is a curation export this build can read, before
 * sending it to the server. The server validates the entries themselves.
 */
export function parseCurationExport(text: string): CurationFileCheck {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return { ok: false, reason: "not-export" };
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return { ok: false, reason: "not-export" };
  }
  const doc = value as Record<string, unknown>;
  if (doc.format !== CURATION_EXPORT_FORMAT || typeof doc.version !== "number") {
    return { ok: false, reason: "not-export" };
  }
  if (doc.version > CURATION_EXPORT_VERSION) {
    return { ok: false, reason: "newer-version" };
  }
  if (doc.version !== CURATION_EXPORT_VERSION || !Array.isArray(doc.sessions)) {
    return { ok: false, reason: "not-export" };
  }
  if (doc.folders !== undefined && !Array.isArray(doc.folders)) {
    return { ok: false, reason: "not-export" };
  }
  const folders = (doc.folders ?? []) as DbCurationExport["folders"];
  return { ok: true, doc: { ...(doc as unknown as DbCurationExport), folders } };
}

/** The download name for an export made on the given local date. */
export function curationExportFileName(now: Date): string {
  const yyyy = String(now.getFullYear());
  const mm = String(now.getMonth() + 1).padStart(2, "0");
  const dd = String(now.getDate()).padStart(2, "0");
  return `agentsview-curation-${yyyy}-${mm}-${dd}.json`;
}

/**
 * Save an export as a JSON file. The export is fetched through the API
 * client first, so the download carries the same authorization as any
 * other request.
 */
export function saveCurationExport(doc: DbCurationExport, fileName: string): void {
  const blob = new Blob([`${JSON.stringify(doc, null, 2)}\n`], {
    type: "application/json",
  });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = fileName;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 100);
}
