import { untrack } from "svelte";
import { sessionTiming } from "./sessionTiming.svelte.js";
import { HIDDEN_WATCH_GRACE_MS } from "./sync.svelte.js";
import { isPageHidden, onPageHidden, onPageVisible } from "../utils/page-visibility.js";
import type { DbSessionTiming as SessionTiming } from "../api/generated/index.js";

/** The longest the labels wait for fresh timing after the tab is shown. */
export const LIVE_TICK_TIMING_WAIT_MS = 5_000;

/** Reactive Date.now() that ticks every second while the loaded
 *  session has `running: true`. A $derived that reads
 *  `liveTick.now` re-runs on each tick, so running-duration
 *  labels can refresh at 1Hz without a per-component setInterval. */
class LiveTickStore {
  now: number = $state(Date.now());

  private timer: ReturnType<typeof setInterval> | null = null;
  private hiddenAt: number | null = null;
  // A tab hidden long enough to close its session watch (sync.svelte.ts)
  // holds a timing snapshot that may be minutes old. Until a newer one
  // arrives, the clock stays put, so no turn shows as running far longer
  // than it did.
  private staleTiming: SessionTiming | null | undefined = undefined;
  private waitTimer: ReturnType<typeof setTimeout> | null = null;

  constructor() {
    $effect.root(() => {
      $effect(() => {
        const timing = sessionTiming.timing;
        const running = timing?.running ?? false;
        untrack(() => {
          if (this.staleTiming !== undefined && timing !== this.staleTiming) this.release();
          if (running) {
            if (this.timer == null) {
              this.now = Date.now();
              this.timer = setInterval(() => this.tick(), 1000);
            }
          } else if (this.timer != null) {
            clearInterval(this.timer);
            this.timer = null;
            this.now = Date.now();
          }
        });
      });
    });
    onPageHidden(() => {
      this.hiddenAt = Date.now();
    });
    onPageVisible(() => {
      const hiddenFor = this.hiddenAt === null ? 0 : Date.now() - this.hiddenAt;
      this.hiddenAt = null;
      if (this.timer == null) return;
      if (hiddenFor < HIDDEN_WATCH_GRACE_MS) {
        this.now = Date.now();
        return;
      }
      this.staleTiming = sessionTiming.timing;
      this.waitTimer = setTimeout(() => this.release(), LIVE_TICK_TIMING_WAIT_MS);
    });
  }

  private tick() {
    // A hidden tab shows no labels; showing it catches up.
    if (isPageHidden() || this.staleTiming !== undefined) return;
    this.now = Date.now();
  }

  private release() {
    this.staleTiming = undefined;
    if (this.waitTimer !== null) {
      clearTimeout(this.waitTimer);
      this.waitTimer = null;
    }
    this.now = Date.now();
  }
}

export const liveTick = new LiveTickStore();
