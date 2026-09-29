import { clampStoredPaneSize } from "../layout/sidebar-width.js";

export const SESSION_TREE_HEIGHT_KEY = "agentsview-session-tree-height";
export const SESSION_TREE_HEIGHT_DEFAULT = 280;
export const SESSION_TREE_HEIGHT_MIN = 96;
export const SESSION_TREE_HEIGHT_STORAGE_MAX = 1600;
// Column height kept below the tree for its resize handle and the session
// vitals, so a tall tree cannot push the vitals out of view.
export const SESSION_TREE_VITALS_RESERVE = 160;

export function clampStoredSessionTreeHeight(value: unknown): number {
  return clampStoredPaneSize(
    value,
    SESSION_TREE_HEIGHT_DEFAULT,
    SESSION_TREE_HEIGHT_MIN,
    SESSION_TREE_HEIGHT_STORAGE_MAX,
  );
}

// Fits a tree height into the column it shares with the session vitals. The
// tree minimum wins when the column cannot hold both it and the reserve.
export function clampSessionTreeHeightForLayout(
  desiredHeight: number,
  columnHeight: number,
): number {
  const layoutMaxHeight = Math.max(
    SESSION_TREE_HEIGHT_MIN,
    Math.min(SESSION_TREE_HEIGHT_STORAGE_MAX, columnHeight - SESSION_TREE_VITALS_RESERVE),
  );

  return Math.min(layoutMaxHeight, Math.max(SESSION_TREE_HEIGHT_MIN, desiredHeight));
}
