/** Sessions dragged from one sidebar row onto a folder. */
export interface SessionDrag {
  project: string;
  sessionIds: string[];
}

/** A folder dragged onto another folder, or out to its project's top level. */
export interface FolderDrag {
  project: string;
  folderId: number;
}

export type SidebarDrag = SessionDrag | FolderDrag;

const SESSION_DRAG_TYPE = "application/x-agentsview-sessions";
const FOLDER_DRAG_TYPE = "application/x-agentsview-folder";

// DataTransfer contents are unreadable during dragover, so drop targets
// read the drag in progress from here to accept or refuse it.
let current: SidebarDrag | null = null;

function start(event: DragEvent, drag: SidebarDrag, type: string): void {
  current = drag;
  if (!event.dataTransfer) return;
  event.dataTransfer.effectAllowed = "move";
  event.dataTransfer.setData(type, JSON.stringify(drag));
}

export function startSessionDrag(event: DragEvent, drag: SessionDrag): void {
  start(event, drag, SESSION_DRAG_TYPE);
}

export function startFolderDrag(event: DragEvent, drag: FolderDrag): void {
  start(event, drag, FOLDER_DRAG_TYPE);
}

export function isFolderDrag(drag: SidebarDrag): drag is FolderDrag {
  return "folderId" in drag;
}

/** The sidebar drag in progress, if this event carries one. */
export function sidebarDragFor(event: DragEvent): SidebarDrag | null {
  if (!current) return null;
  const types = event.dataTransfer?.types;
  const type = isFolderDrag(current) ? FOLDER_DRAG_TYPE : SESSION_DRAG_TYPE;
  if (types && !Array.from(types).includes(type)) return null;
  return current;
}

export function endSidebarDrag(): void {
  current = null;
}
