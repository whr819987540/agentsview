<script lang="ts">
  import { Button, Table, TableHeaderCell } from "@kenn-io/kit-ui";
  import { onDestroy } from "svelte";
  import { getLocale, m } from "../../i18n/index.js";
  import { CurationService } from "../../api/generated/index";
  import type {
    DbCurationExport,
    DbCurationImportCounts,
    DbCurationImportResult,
  } from "../../api/generated/index";
  import { ApiError, isAbortError } from "../../api/runtime.js";
  import { LatestRead } from "../../utils/latest-read.js";
  import { settings } from "../../stores/settings.svelte.js";
  import { starred } from "../../stores/starred.svelte.js";
  import { sessionFolders } from "../../stores/sessionFolders.svelte.js";
  import { sessionEndMarks } from "../../stores/sessionEndMarks.svelte.js";
  import {
    CURATION_ID_PREVIEW_LIMIT,
    curationExportFileName,
    parseCurationExport,
    saveCurationExport,
  } from "./curationTransfer.js";

  let exportPhase = $state<"idle" | "exporting" | "exported" | "failed">("idle");
  let exportSummary = $state<{ fileName: string; sessions: number; folders: number } | null>(
    null,
  );
  let exportError = $state("");

  let importPhase = $state<
    "idle" | "checking" | "previewed" | "importing" | "imported" | "failed"
  >("idle");
  let fileName = $state("");
  let importDoc = $state<DbCurationExport | null>(null);
  let preview = $state<DbCurationImportResult | null>(null);
  let imported = $state<DbCurationImportResult | null>(null);
  let importError = $state("");

  // Both actions need the server to read its own local archive; a replica
  // answers 501 and the panel says so once instead of per action.
  let unavailable = $state(false);
  let fileInput: HTMLInputElement | undefined = $state();
  const previewRead = new LatestRead();
  let disposed = false;

  onDestroy(() => {
    disposed = true;
    previewRead.cancel();
  });

  function formatCount(value: number): string {
    return new Intl.NumberFormat(getLocale()).format(value);
  }

  function apiErrorText(e: unknown, fallback: string): string {
    if (e instanceof ApiError && e.status === 501) {
      unavailable = true;
      return "";
    }
    return e instanceof ApiError && e.message ? e.message : fallback;
  }

  async function runExport() {
    if (exportPhase === "exporting") return;
    exportPhase = "exporting";
    exportError = "";
    try {
      const doc = await CurationService.getApiV1CurationExport();
      if (disposed) return;
      const name = curationExportFileName(new Date());
      saveCurationExport(doc, name);
      exportSummary = {
        fileName: name,
        sessions: doc.sessions.length,
        folders: doc.folders.length,
      };
      exportPhase = "exported";
    } catch (e) {
      if (disposed || isAbortError(e)) return;
      exportError = apiErrorText(e, m.settings_curation_export_failed());
      exportPhase = exportError ? "failed" : "idle";
    }
  }

  function chooseFile() {
    fileInput?.click();
  }

  async function onFileChange(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const file = input.files?.[0];
    // Clear the input so choosing the same file again still fires change.
    input.value = "";
    if (!file) return;

    const signal = previewRead.begin();
    fileName = file.name;
    importDoc = null;
    preview = null;
    imported = null;
    importError = "";
    importPhase = "checking";
    try {
      const check = parseCurationExport(await file.text());
      if (disposed || !previewRead.isCurrent(signal)) return;
      if (!check.ok) {
        importError =
          check.reason === "newer-version"
            ? m.settings_curation_newer_version()
            : m.settings_curation_not_export();
        importPhase = "failed";
        return;
      }
      const result = await CurationService.postApiV1CurationImport(
        check.doc,
        { dry_run: true },
        { signal },
      );
      if (disposed || !previewRead.isCurrent(signal)) return;
      importDoc = check.doc;
      preview = result;
      importPhase = "previewed";
    } catch (e) {
      if (disposed || isAbortError(e) || !previewRead.isCurrent(signal)) return;
      importError = apiErrorText(e, m.settings_curation_preview_failed());
      importPhase = importError ? "failed" : "idle";
    } finally {
      previewRead.finish(signal);
    }
  }

  async function runImport() {
    if (importPhase !== "previewed" || importDoc === null) return;
    importPhase = "importing";
    importError = "";
    try {
      const result = await CurationService.postApiV1CurationImport(importDoc);
      if (disposed) return;
      imported = result;
      importPhase = "imported";
      // Show the imported curation in the sidebar without a reload.
      await Promise.allSettled([
        starred.refresh(),
        sessionFolders.refresh(),
        sessionEndMarks.refresh(),
      ]);
    } catch (e) {
      if (disposed || isAbortError(e)) return;
      const text = apiErrorText(e, "");
      importError = text
        ? `${text} ${m.settings_curation_import_failed()}`
        : m.settings_curation_import_failed();
      importPhase = "previewed";
    }
  }

  function addsNothing(r: DbCurationImportResult): boolean {
    return (
      r.stars.added + r.end_marks.added + r.folder_members.added + r.folders_created === 0
    );
  }

  const shown = $derived(importPhase === "imported" ? imported : preview);
  const rows = $derived.by((): Array<{ key: string; label: string; counts: DbCurationImportCounts }> =>
    shown === null
      ? []
      : [
          { key: "stars", label: m.settings_curation_row_stars(), counts: shown.stars },
          { key: "end-marks", label: m.settings_curation_row_end_marks(), counts: shown.end_marks },
          {
            key: "folder-members",
            label: m.settings_curation_row_folder_members(),
            counts: shown.folder_members,
          },
        ],
  );
  // Session IDs the import skipped, listed so the user can tell whether
  // they have not synced here yet.
  const idGroups = $derived.by(() => {
    if (shown === null) return [];
    const count = (ids: string[]) => formatCount(ids.length);
    return [
      {
        key: "unmatched",
        label: m.settings_curation_unmatched_label({ count: count(shown.unmatched) }),
        ids: shown.unmatched,
        hint: m.settings_curation_unmatched_hint(),
      },
      {
        key: "ambiguous",
        label: m.settings_curation_ambiguous_label({ count: count(shown.ambiguous) }),
        ids: shown.ambiguous,
        hint: m.settings_curation_ambiguous_hint(),
      },
    ].filter((group) => group.ids.length > 0);
  });
  const importDisabled = $derived(settings.readOnly || unavailable);
</script>

<div class="curation-transfer">
  {#if unavailable}
    <p class="msg muted">{m.settings_curation_unavailable()}</p>
  {/if}

  <section class="block" aria-labelledby="curation-export-heading">
    <h3 id="curation-export-heading" class="block-title">
      {m.settings_curation_export_heading()}
    </h3>
    <p class="intro">{m.settings_curation_export_intro()}</p>
    <div class="action-row">
      <Button onclick={runExport} disabled={exportPhase === "exporting" || unavailable}>
        {exportPhase === "exporting"
          ? m.settings_curation_exporting()
          : m.settings_curation_export_button()}
      </Button>
    </div>
    {#if exportPhase === "failed" && exportError}
      <p class="msg error" role="alert">{exportError}</p>
    {/if}
    {#if exportPhase === "exported" && exportSummary !== null}
      <div class="status-grid" role="status">
        <span class="row-label">{m.settings_curation_file_label()}</span>
        <span class="row-value mono">{exportSummary.fileName}</span>
        <span class="row-label">{m.settings_curation_export_sessions_label()}</span>
        <span class="row-value">{formatCount(exportSummary.sessions)}</span>
        <span class="row-label">{m.settings_curation_export_folders_label()}</span>
        <span class="row-value">{formatCount(exportSummary.folders)}</span>
      </div>
    {/if}
  </section>

  <section class="block" aria-labelledby="curation-import-heading">
    <h3 id="curation-import-heading" class="block-title">
      {m.settings_curation_import_heading()}
    </h3>
    <p class="intro">{m.settings_curation_import_intro()}</p>
    {#if settings.readOnly}
      <p class="msg muted">{m.settings_curation_read_only()}</p>
    {/if}
    <div class="action-row">
      <Button
        onclick={chooseFile}
        disabled={importDisabled || importPhase === "checking" || importPhase === "importing"}
      >
        {importPhase === "checking"
          ? m.settings_curation_checking()
          : m.settings_curation_choose_file()}
      </Button>
      <input
        bind:this={fileInput}
        type="file"
        accept=".json,application/json"
        class="kit-sr-only"
        tabindex="-1"
        aria-hidden="true"
        onchange={onFileChange}
      />
    </div>

    {#if importError}
      <p class="msg error" role="alert">{importError}</p>
    {/if}

    {#if shown !== null && (importPhase === "previewed" || importPhase === "importing" || importPhase === "imported")}
      <div class="status-grid">
        <span class="row-label">{m.settings_curation_file_label()}</span>
        <span class="row-value mono">{fileName}</span>
        <span class="row-label">{m.settings_curation_matched_label()}</span>
        <span class="row-value">
          {m.settings_curation_matched_value({
            matched: formatCount(shown.matched),
            total: formatCount(shown.sessions),
          })}
        </span>
        {#if shown.matched_by_prefix > 0}
          <span class="row-label">{m.settings_curation_prefixed_label()}</span>
          <span class="row-value">{formatCount(shown.matched_by_prefix)}</span>
        {/if}
        <span class="row-label">
          {importPhase === "imported"
            ? m.settings_curation_folders_created_label()
            : m.settings_curation_folders_to_create_label()}
        </span>
        <span class="row-value">{formatCount(shown.folders_created)}</span>
      </div>

      <Table
        ariaLabel={m.settings_curation_table_label()}
        stickyHeader={false}
        zebra={false}
        class="curation-table"
      >
        {#snippet header()}
          <TableHeaderCell label={m.settings_curation_column_item()} />
          <TableHeaderCell
            label={importPhase === "imported"
              ? m.settings_curation_column_added()
              : m.settings_curation_column_to_add()}
          />
          <TableHeaderCell label={m.settings_curation_column_unchanged()} />
          <TableHeaderCell label={m.settings_curation_column_kept()} />
        {/snippet}
        {#snippet children()}
          {#each rows as row (row.key)}
            <tr data-row={row.key}>
              <td>{row.label}</td>
              <td>{formatCount(row.counts.added)}</td>
              <td>{formatCount(row.counts.unchanged)}</td>
              <td>{formatCount(row.counts.kept)}</td>
            </tr>
          {/each}
        {/snippet}
      </Table>
      <p class="note">{m.settings_curation_kept_note()}</p>

      {#each idGroups as group (group.key)}
        <div class="id-group" data-group={group.key}>
          <p class="subsection-title">{group.label}</p>
          <ul class="id-list">
            {#each group.ids.slice(0, CURATION_ID_PREVIEW_LIMIT) as id (id)}
              <li class="mono">{id}</li>
            {/each}
          </ul>
          {#if group.ids.length > CURATION_ID_PREVIEW_LIMIT}
            <p class="note">
              {m.settings_curation_ids_more({
                count: formatCount(group.ids.length - CURATION_ID_PREVIEW_LIMIT),
              })}
            </p>
          {/if}
          <p class="note">{group.hint}</p>
        </div>
      {/each}

      {#if importPhase === "imported"}
        <p class="msg success" role="status">{m.settings_curation_imported()}</p>
      {:else if addsNothing(shown)}
        <p class="msg muted">{m.settings_curation_nothing_to_import()}</p>
      {:else}
        <div class="action-row">
          <Button
            surface="solid"
            onclick={runImport}
            disabled={importDisabled || importPhase === "importing"}
          >
            {importPhase === "importing"
              ? m.settings_curation_importing()
              : m.settings_curation_import_button()}
          </Button>
        </div>
      {/if}
    {/if}
  </section>
</div>

<style>
  .curation-transfer {
    display: flex;
    flex-direction: column;
    gap: var(--space-7);
  }

  .block {
    display: flex;
    flex-direction: column;
    gap: var(--space-5);
  }

  .block-title,
  .subsection-title {
    margin: 0;
    font-size: 12px;
    font-weight: 600;
    color: var(--text-secondary);
  }

  .intro,
  .note {
    margin: 0;
    font-size: 12px;
    color: var(--text-secondary);
  }

  .note {
    font-size: 11px;
    color: var(--text-muted);
  }

  .action-row {
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .msg {
    margin: 0;
    font-size: 11px;
  }

  .msg.muted {
    color: var(--text-muted);
  }

  .msg.error {
    color: var(--accent-red, #ef4444);
  }

  .msg.success {
    color: var(--text-primary);
  }

  .status-grid {
    display: grid;
    grid-template-columns: auto 1fr;
    column-gap: 16px;
    row-gap: 6px;
    align-items: start;
  }

  .row-label {
    font-size: 12px;
    font-weight: 500;
    color: var(--text-secondary);
  }

  .row-value {
    font-size: 12px;
    color: var(--text-primary);
    overflow-wrap: anywhere;
  }

  .mono {
    font-family: var(--font-mono, monospace);
  }

  .id-group {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  .id-list {
    margin: 0;
    padding-left: 16px;
    font-size: 11px;
    color: var(--text-primary);
  }
</style>
