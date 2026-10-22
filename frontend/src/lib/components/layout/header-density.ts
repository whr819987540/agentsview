/**
 * Session header layouts, roomiest first. Each lists the CSS steps it turns
 * on through the header's `data-density` attribute, and each keeps the steps
 * of the one before it, so a tighter layout never shows what a roomier one
 * hid.
 *
 * - `compact`: hides the start time, session ID, and usage breakdown, shortens
 *   the token badge, and moves the scroll and bulk block buttons into the
 *   session actions menu.
 * - `tight`: hides the agent badges and narrows the gaps.
 * - `no-usage`: hides the token and cost badges.
 * - `actions-only`: hides every badge, leaving the action buttons.
 */
export const HEADER_DENSITIES = [
  "",
  "compact",
  "compact tight",
  "compact tight no-usage",
  "compact tight no-usage actions-only",
] as const;

/**
 * Give `header` the roomiest density whose layout keeps the right edge of
 * `actions` inside `row`, and return its index. Each try lays the header out
 * again from scratch, so the choice depends only on the space and content,
 * never on the density shown before. When nothing fits, the tightest density
 * stays.
 */
export function chooseHeaderDensity(
  header: HTMLElement,
  row: HTMLElement,
  actions: HTMLElement,
  densities: readonly string[] = HEADER_DENSITIES,
): number {
  for (let i = 0; i < densities.length; i++) {
    header.dataset.density = densities[i];
    if (actions.getBoundingClientRect().right <= row.getBoundingClientRect().right + 0.5) {
      return i;
    }
  }
  return densities.length - 1;
}

/**
 * Attachment for the session header's badge row. It picks the density again
 * whenever the header changes size, as when the window, sidebar, or analysis
 * panel resizes, and whenever the header's content changes, as when another
 * session opens or a cost updates. Viewport breakpoints cannot do this: the
 * space left for the header depends on the panels beside it.
 */
export function fitHeaderDensity(getActions: () => HTMLElement | null | undefined) {
  return (row: HTMLElement) => {
    const header = row.parentElement;
    if (!header) return;
    let active = true;
    const fit = () => {
      const actions = getActions();
      if (active && actions?.isConnected) chooseHeaderDensity(header, row, actions);
    };
    const resize = new ResizeObserver(fit);
    resize.observe(header);
    // Class changes can change a badge's width. The density attribute is not
    // a class, so setting it does not call fit again.
    const mutations = new MutationObserver(fit);
    mutations.observe(header, {
      subtree: true,
      childList: true,
      characterData: true,
      attributes: true,
      attributeFilter: ["class"],
    });
    void document.fonts?.ready.then(fit);
    return () => {
      active = false;
      resize.disconnect();
      mutations.disconnect();
      delete header.dataset.density;
    };
  };
}
