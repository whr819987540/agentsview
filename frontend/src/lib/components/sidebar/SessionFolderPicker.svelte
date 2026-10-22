<script lang="ts">
  import { onMount } from "svelte";
  import { Button, IconButton, TextInput, showFlash } from "@kenn-io/kit-ui";
  import { m } from "../../i18n/index.js";
  import { CheckIcon, FolderIcon, XIcon } from "../../icons.js";
  import {
    sessionFolders,
    SessionFolderNameTakenError,
  } from "../../stores/sessionFolders.svelte.js";

  interface Props {
    /** Viewport position of the popover's top-left corner. */
    x: number;
    y: number;
    project: string;
    /** The row's sessions, primary first. */
    sessionIds: string[];
    onclose: () => void;
  }

  let { x, y, project, sessionIds, onclose }: Props = $props();

  let newName = $state("");
  let saving = $state(false);
  let error = $state("");
  let card: HTMLDivElement | undefined = $state(undefined);

  let folders = $derived(sessionFolders.foldersFor(project));
  let currentId = $derived(
    sessionFolders.folderIdForSessions(project, sessionIds),
  );

  function mountOverlay(node: HTMLElement) {
    document.body.appendChild(node);
    return {
      destroy() {
        node.remove();
      },
    };
  }

  onMount(() => {
    function onPointerDown(e: PointerEvent) {
      if (card && e.target instanceof Node && !card.contains(e.target)) {
        onclose();
      }
    }
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        // Claim the key so the global Escape does not also leave the session.
        e.preventDefault();
        onclose();
      }
    }
    document.addEventListener("pointerdown", onPointerDown, true);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      document.removeEventListener("keydown", onKeyDown);
    };
  });

  async function run(action: () => Promise<string>) {
    if (saving) return;
    saving = true;
    error = "";
    try {
      const message = await action();
      onclose();
      showFlash(message, { tone: "success" });
    } catch (e) {
      if (e instanceof SessionFolderNameTakenError) {
        error = m.sidebar_folder_name_taken({ name: newName.trim() });
      } else {
        // A move hides the row at once, which can close this picker
        // before the request fails, so report it where it stays visible.
        onclose();
        showFlash(m.sidebar_folder_move_failed(), { tone: "danger" });
      }
    } finally {
      saving = false;
    }
  }

  function moveTo(folderId: number, name: string) {
    if (folderId === currentId) {
      onclose();
      return;
    }
    void run(async () => {
      await sessionFolders.moveSessions(folderId, sessionIds);
      return m.sidebar_folder_moved({ name });
    });
  }

  function removeFromFolder() {
    const name = folders.find((f) => f.id === currentId)?.name ?? "";
    void run(async () => {
      await sessionFolders.removeSessions(sessionIds);
      return m.sidebar_folder_removed({ name });
    });
  }

  function createAndMove() {
    const name = newName.trim();
    if (!name) return;
    void run(async () => {
      const folder = await sessionFolders.create(project, name);
      await sessionFolders.moveSessions(folder.id, sessionIds);
      return m.sidebar_folder_moved({ name: folder.name });
    });
  }
</script>

<div
  bind:this={card}
  class="folder-picker kit-popover-card"
  use:mountOverlay
  role="dialog"
  aria-label={m.sidebar_folder_picker_heading()}
  style="left: {Math.max(8, Math.min(x, window.innerWidth - 268))}px; top: {Math.max(8, Math.min(y, window.innerHeight - 320))}px;"
>
  <div class="folder-picker-header">
    <strong>{m.sidebar_folder_picker_heading()}</strong>
    <IconButton size="sm" ariaLabel={m.sidebar_folder_picker_close()} onclick={onclose}>
      <XIcon size="13" aria-hidden="true" />
    </IconButton>
  </div>
  <div class="folder-picker-project" title={project}>{project}</div>

  {#if folders.length > 0 || currentId !== null}
    <div class="folder-picker-list" role="list">
      {#if currentId !== null}
        <button
          type="button"
          class="folder-picker-option"
          disabled={saving}
          onclick={removeFromFolder}
        >
          <span class="folder-picker-icon"></span>
          <span class="folder-picker-name">{m.sidebar_folder_none()}</span>
        </button>
      {/if}
      {#each folders as folder (folder.id)}
        <button
          type="button"
          class="folder-picker-option"
          class:current={folder.id === currentId}
          aria-current={folder.id === currentId ? "true" : undefined}
          disabled={saving}
          onclick={() => moveTo(folder.id, folder.name)}
        >
          <span class="folder-picker-icon">
            <FolderIcon size="12" strokeWidth="1.8" aria-hidden="true" />
          </span>
          <span class="folder-picker-name" title={folder.name}>{folder.name}</span>
          {#if folder.id === currentId}
            <CheckIcon size="12" strokeWidth="2" aria-hidden="true" />
          {/if}
        </button>
      {/each}
    </div>
  {/if}

  <div class="folder-picker-create">
    <div class="folder-picker-input">
      <TextInput
        size="sm"
        block
        autofocus
        bind:value={newName}
        placeholder={m.sidebar_folder_new_placeholder()}
        ariaLabel={m.sidebar_folder_new_placeholder()}
        invalid={!!error}
        oninput={() => (error = "")}
        onkeydown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            createAndMove();
          }
        }}
      />
    </div>
    <Button
      size="sm"
      label={m.sidebar_folder_create_submit()}
      disabled={!newName.trim() || saving}
      onclick={createAndMove}
    />
  </div>
  {#if error}
    <p class="folder-picker-error" role="alert">{error}</p>
  {/if}
</div>

<style>
  .folder-picker {
    position: fixed;
    z-index: var(--z-popover);
    display: flex;
    width: 260px;
    flex-direction: column;
    gap: 8px;
    padding: 10px;
  }

  .folder-picker-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
  }

  .folder-picker-header strong {
    color: var(--text-primary);
    font-size: 12px;
  }

  .folder-picker-project {
    overflow: hidden;
    color: var(--text-muted);
    font-size: 11px;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .folder-picker-list {
    display: flex;
    max-height: 180px;
    flex-direction: column;
    overflow-y: auto;
    margin: 0 -4px;
  }

  .folder-picker-option {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 5px 6px;
    border-radius: var(--radius-sm);
    color: var(--text-primary);
    font-size: 12px;
    text-align: left;
    cursor: pointer;
  }

  .folder-picker-option:hover:not(:disabled) {
    background: var(--bg-surface-hover);
  }

  .folder-picker-option:disabled {
    cursor: default;
    opacity: var(--opacity-disabled);
  }

  .folder-picker-option.current {
    color: var(--accent-blue);
  }

  .folder-picker-icon {
    display: flex;
    width: 12px;
    flex-shrink: 0;
    color: var(--text-muted);
  }

  .folder-picker-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .folder-picker-create {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .folder-picker-input {
    flex: 1;
    min-width: 0;
  }

  .folder-picker-error {
    margin: 0;
    color: var(--accent-red);
    font-size: 10px;
  }
</style>
