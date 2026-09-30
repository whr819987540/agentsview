import { clampStoredPaneSize } from "../layout/sidebar-width.js";

export const SESSION_INPUT_OUTLINE_HEIGHT_KEY = "agentsview-session-input-outline-height";
export const SESSION_INPUT_OUTLINE_HEIGHT_DEFAULT = 240;
export const SESSION_INPUT_OUTLINE_HEIGHT_MIN = 96;
export const SESSION_INPUT_OUTLINE_HEIGHT_STORAGE_MAX = 1600;
// Panel height kept for the vitals title bar, the resize handle, and the
// start of the next vitals section, so a long outline cannot fill the panel.
export const SESSION_INPUT_OUTLINE_PANEL_RESERVE = 160;

export function clampStoredSessionInputOutlineHeight(value: unknown): number {
  return clampStoredPaneSize(
    value,
    SESSION_INPUT_OUTLINE_HEIGHT_DEFAULT,
    SESSION_INPUT_OUTLINE_HEIGHT_MIN,
    SESSION_INPUT_OUTLINE_HEIGHT_STORAGE_MAX,
  );
}

// Fits an outline height into the vitals panel it scrolls in. The outline
// minimum wins when the panel cannot hold both it and the reserve.
export function clampSessionInputOutlineHeightForLayout(
  desiredHeight: number,
  panelHeight: number,
): number {
  const layoutMaxHeight = Math.max(
    SESSION_INPUT_OUTLINE_HEIGHT_MIN,
    Math.min(
      SESSION_INPUT_OUTLINE_HEIGHT_STORAGE_MAX,
      panelHeight - SESSION_INPUT_OUTLINE_PANEL_RESERVE,
    ),
  );

  return Math.min(layoutMaxHeight, Math.max(SESSION_INPUT_OUTLINE_HEIGHT_MIN, desiredHeight));
}
