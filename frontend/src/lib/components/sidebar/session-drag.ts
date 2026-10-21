/** Sessions dragged from one sidebar row onto a folder. */
export interface SessionDrag {
  project: string;
  sessionIds: string[];
}

const DRAG_TYPE = "application/x-agentsview-sessions";

// DataTransfer contents are unreadable during dragover, so drop targets
// read the drag in progress from here to accept or refuse it.
let current: SessionDrag | null = null;

export function startSessionDrag(event: DragEvent, drag: SessionDrag): void {
  current = drag;
  if (!event.dataTransfer) return;
  event.dataTransfer.effectAllowed = "move";
  event.dataTransfer.setData(DRAG_TYPE, JSON.stringify(drag));
}

/** The sidebar drag in progress, if this event carries one. */
export function sessionDragFor(event: DragEvent): SessionDrag | null {
  if (!current) return null;
  const types = event.dataTransfer?.types;
  if (types && !Array.from(types).includes(DRAG_TYPE)) return null;
  return current;
}

export function endSessionDrag(): void {
  current = null;
}
