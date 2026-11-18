/**
 * Page visibility for background work.
 *
 * A hidden tab shows nothing, so live refreshes, polls and tickers wait until
 * the tab is visible again and then catch up once. Browsers report a tab as
 * hidden while it is in the background or its window is minimized.
 */

type Listener = () => void;

/**
 * How long after the tab becomes visible the catch-up starts. The visibility
 * health check (health.ts) and the desktop app probe the daemon the moment a
 * tab is shown and reload the page when the probe takes more than 3 s, so
 * their requests go first rather than queueing behind the catch-up burst for
 * the browser's few connections to the daemon.
 */
export const VISIBLE_CATCH_UP_DELAY_MS = 250;

const visibleListeners = new Set<Listener>();
const hiddenListeners = new Set<Listener>();
let installed = false;
let catchUpTimer: ReturnType<typeof setTimeout> | null = null;

/** True while the browser reports this tab as hidden. */
export function isPageHidden(): boolean {
  return typeof document !== "undefined" && document.visibilityState === "hidden";
}

function install() {
  if (installed || typeof document === "undefined") return;
  if (typeof document.addEventListener !== "function") return;
  installed = true;
  document.addEventListener("visibilitychange", () => {
    if (isPageHidden()) {
      if (catchUpTimer !== null) {
        clearTimeout(catchUpTimer);
        catchUpTimer = null;
      }
      for (const fn of [...hiddenListeners]) fn();
      return;
    }
    if (catchUpTimer !== null) return;
    catchUpTimer = setTimeout(() => {
      catchUpTimer = null;
      if (isPageHidden()) return;
      for (const fn of [...visibleListeners]) fn();
    }, VISIBLE_CATCH_UP_DELAY_MS);
  });
}

/**
 * Calls fn every time the tab becomes visible, VISIBLE_CATCH_UP_DELAY_MS
 * later, unless the tab was hidden again meanwhile. Returns an unsubscribe.
 */
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
