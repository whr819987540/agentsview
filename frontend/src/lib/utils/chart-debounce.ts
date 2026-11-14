import { useDebounce } from "runed";

// Kept for the page's lifetime so its function shape stays the first one.
let primed: ReturnType<typeof useDebounce> | undefined;

/**
 * Create runed's debounce once, before any chart mounts.
 *
 * useDebounce returns a function and gives it `cancel`, `runScheduledNow`
 * and a `pending` getter. V8 shares one descriptor array among functions
 * that gain the same properties in the same order, and that array keeps the
 * first `pending` getter it saw. Every layerchart Chart calls useDebounce,
 * and the getter's closure reaches the chart's component context. Without
 * this, the first chart a page showed, and the whole dashboard around it,
 * stayed reachable for the rest of the page's life, because any other
 * function with a `cancel` property, such as the transcript virtualizer's
 * debounce, keeps that descriptor array alive.
 *
 * A debounce created here, outside any component, takes that first slot
 * and holds nothing.
 */
export function primeChartDebounce(): void {
  primed ??= useDebounce(() => {}, 0);
}
