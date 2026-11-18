/**
 * Page visibility for background work.
 *
 * A hidden tab shows nothing, so live refreshes, polls and tickers wait until
 * the tab is visible again and then catch up once. Browsers report a tab as
 * hidden while it is in the background or its window is minimized.
 */

type Listener = () => void;

const visibleListeners = new Set<Listener>();
const hiddenListeners = new Set<Listener>();
let installed = false;

/** True while the browser reports this tab as hidden. */
export function isPageHidden(): boolean {
  return typeof document !== "undefined" && document.visibilityState === "hidden";
}

function install() {
  if (installed || typeof document === "undefined") return;
  if (typeof document.addEventListener !== "function") return;
  installed = true;
  document.addEventListener("visibilitychange", () => {
    const listeners = isPageHidden() ? hiddenListeners : visibleListeners;
    for (const fn of [...listeners]) fn();
  });
}

/** Calls fn every time the tab becomes visible. Returns an unsubscribe. */
export function onPageVisible(fn: Listener): () => void {
  install();
  visibleListeners.add(fn);
  return () => visibleListeners.delete(fn);
}

/** Calls fn every time the tab becomes hidden. Returns an unsubscribe. */
export function onPageHidden(fn: Listener): () => void {
  install();
  hiddenListeners.add(fn);
  return () => hiddenListeners.delete(fn);
}

/**
 * Wraps fn so that calls made while the tab is hidden collapse into one call
 * when it becomes visible again. Calls made while visible run immediately.
 * `dispose` drops a pending call and stops listening.
 */
export function deferWhileHidden<Args extends unknown[]>(
  fn: (...args: Args) => void,
): { run: (...args: Args) => void; dispose: () => void } {
  let pending: Args | null = null;
  const unsubscribe = onPageVisible(() => {
    if (pending === null) return;
    const args = pending;
    pending = null;
    fn(...args);
  });
  return {
    run: (...args: Args) => {
      if (isPageHidden()) {
        pending = args;
        return;
      }
      fn(...args);
    },
    dispose: () => {
      pending = null;
      unsubscribe();
    },
  };
}
