<script lang="ts">
  import { TextInput, showFlash } from "@kenn-io/kit-ui";
  import { m } from "../../i18n/index.js";
  import {
    ChevronDownIcon,
    ChevronRightIcon,
    FolderIcon,
    FolderOpenIcon,
  } from "../../icons.js";
  import {
    sessionFolders,
    SessionFolderNameTakenError,
  } from "../../stores/sessionFolders.svelte.js";
  import type { SidebarFolder } from "./session-list-utils.js";

  interface Props {
    folder: SidebarFolder;
    /** Session rows filed in the folder under the current filters. */
    count: number;
    expanded: boolean;
    editable: boolean;
    /** Show the name editor; the list opens it for a new folder. */
    renaming: boolean;
    /** A dragged session would land in this folder. */
    dropActive: boolean;
    onToggle: () => void;
    onRenameStart: () => void;
    onRenameEnd: () => void;
  }

  let {
    folder,
    count,
    expanded,
    editable,
    renaming,
    dropActive,
    onToggle,
    onRenameStart,
    onRenameEnd,
  }: Props = $props();

  let renameValue = $state("");
  let renameInput: HTMLInputElement | undefined = $state(undefined);
  let renameSubmitting = false;
  let contextMenu: { x: number; y: number } | null = $state(null);

  let toggleLabel = $derived(
    expanded
      ? m.sidebar_collapse_folder({ name: folder.name })
      : m.sidebar_expand_folder({ name: folder.name }),
  );

  $effect(() => {
    if (!renaming) return;
    renameValue = folder.name;
    requestAnimationFrame(() => renameInput?.select());
  });

  function mountOverlay(node: HTMLElement) {
    document.body.appendChild(node);
    return {
      destroy() {
        node.remove();
      },
    };
  }

  $effect(() => {
    if (!contextMenu) return;
    function close() {
      contextMenu = null;
    }
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") close();
    }
    const id = setTimeout(() => {
      document.addEventListener("click", close, { once: true });
      document.addEventListener("contextmenu", close, { once: true });
    }, 0);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      clearTimeout(id);
      document.removeEventListener("click", close);
      document.removeEventListener("contextmenu", close);
      document.removeEventListener("keydown", onKeyDown);
    };
  });

  function handleContextMenu(e: MouseEvent) {
    if (!editable) return;
    e.preventDefault();
    contextMenu = { x: e.clientX, y: e.clientY };
  }

  async function submitRename() {
    if (!renaming || renameSubmitting) return;
    const name = renameValue.trim();
    if (!name || name === folder.name) {
      onRenameEnd();
      return;
    }
    renameSubmitting = true;
    try {
      await sessionFolders.rename(folder.id, name);
    } catch (error) {
      showFlash(
        error instanceof SessionFolderNameTakenError
          ? m.sidebar_folder_name_taken({ name })
          : m.sidebar_folder_save_failed(),
        { tone: "danger" },
      );
    } finally {
      renameSubmitting = false;
      onRenameEnd();
    }
  }

  async function deleteFolder() {
    contextMenu = null;
    try {
      await sessionFolders.remove(folder.id);
      showFlash(m.sidebar_folder_deleted({ name: folder.name }), { tone: "success" });
    } catch {
      showFlash(m.sidebar_folder_delete_failed(), { tone: "danger" });
    }
  }
</script>

{#if renaming}
  <div class="folder-row editing">
    <span class="folder-chevron"></span>
    <FolderIcon class="folder-icon" size="12" strokeWidth="1.8" aria-hidden="true" />
    <TextInput
      size="sm"
      block
      autofocus
      bind:value={renameValue}
      bind:inputEl={renameInput}
      ariaLabel={m.sidebar_folder_name_label()}
      onblur={() => void submitRename()}
      onkeydown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          void submitRename();
        } else if (e.key === "Escape") {
          e.preventDefault();
          onRenameEnd();
        }
      }}
    />
  </div>
{:else}
  <button
    type="button"
    class="folder-row"
    class:drop-active={dropActive}
    aria-expanded={expanded}
    title={toggleLabel}
    aria-label={toggleLabel}
    onclick={onToggle}
    ondblclick={(e) => {
      if (!editable) return;
      e.preventDefault();
      onRenameStart();
    }}
    oncontextmenu={handleContextMenu}
  >
    <span class="folder-chevron">
      {#if expanded}
        <ChevronDownIcon size="10" strokeWidth="2.5" aria-hidden="true" />
      {:else}
        <ChevronRightIcon size="10" strokeWidth="2.5" aria-hidden="true" />
      {/if}
    </span>
    {#if expanded}
      <FolderOpenIcon class="folder-icon" size="12" strokeWidth="1.8" aria-hidden="true" />
    {:else}
      <FolderIcon class="folder-icon" size="12" strokeWidth="1.8" aria-hidden="true" />
    {/if}
    <span class="folder-name">{folder.name}</span>
    <span class="folder-count">{count}</span>
  </button>
{/if}

{#if contextMenu}
  <div
    class="context-menu"
    use:mountOverlay
    style="left: {contextMenu.x}px; top: {contextMenu.y}px;"
  >
    <button
      class="context-menu-item"
      onclick={() => {
        contextMenu = null;
        onRenameStart();
      }}
    >
      {m.sidebar_folder_rename()}
    </button>
    <button class="context-menu-item danger" onclick={() => void deleteFolder()}>
      {m.sidebar_folder_delete()}
    </button>
  </div>
{/if}

<style>
  .folder-row {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    height: 28px;
    padding: 0 10px 0 8px;
    font-size: 11px;
    font-weight: 600;
    color: var(--text-secondary);
    text-align: left;
    user-select: none;
    transition: color 0.1s, background 0.1s, box-shadow 0.1s;
  }

  button.folder-row:hover {
    color: var(--text-primary);
    background: var(--bg-surface-hover);
  }

  .folder-row.drop-active {
    background: color-mix(in srgb, var(--accent-blue) 12%, transparent);
    box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--accent-blue) 45%, transparent);
    color: var(--text-primary);
  }

  .folder-chevron {
    display: flex;
    justify-content: center;
    width: 16px;
    flex-shrink: 0;
    color: var(--text-muted);
  }

  :global(.folder-row .folder-icon) {
    flex-shrink: 0;
    color: var(--accent-amber);
  }

  .folder-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .folder-count {
    flex-shrink: 0;
    font-size: 9px;
    font-weight: 500;
    color: var(--text-muted);
    background: var(--bg-inset);
    padding: 0 5px;
    border-radius: 8px;
    line-height: 16px;
  }
</style>
