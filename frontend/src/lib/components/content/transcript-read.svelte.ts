import { messages } from "../../stores/messages.svelte.js";

type Read<T> = (sessionId: string, signal: AbortSignal) => Promise<T>;

/**
 * Data read for a session that follows the transcript on screen. It reads
 * again whenever the message list moves that session to a new transcript
 * revision, so inputs a live update or a Sync brings in show up without
 * switching sessions. A re-read keeps the current data on screen until the new
 * data lands, and a failed re-read leaves it in place.
 *
 * Construct it during component initialization: it owns an effect.
 */
export class TranscriptRead<T> {
  data: T | null = $state(null);
  loading: boolean = $state(false);
  failed: boolean = $state(false);

  // The session the data on screen was read for.
  #shownFor: string | null = null;

  constructor(sessionId: () => string, read: Read<T>) {
    const revision = $derived(messages.revisionShownFor(sessionId()));

    $effect(() => {
      const id = sessionId();
      const shown = revision;
      const refresh = this.#shownFor === id;
      if (!refresh) {
        this.#shownFor = null;
        this.data = null;
        this.loading = true;
        this.failed = false;
      }
      // The message list is replacing its rows; read once they land so the
      // data matches them.
      if (shown === undefined) return;

      const controller = new AbortController();
      read(id, controller.signal)
        .then((next) => {
          if (controller.signal.aborted) return;
          this.data = next;
          this.failed = false;
        })
        .catch(() => {
          if (controller.signal.aborted) return;
          if (!refresh) this.failed = true;
        })
        .finally(() => {
          if (controller.signal.aborted) return;
          this.#shownFor = id;
          this.loading = false;
        });

      return () => controller.abort();
    });
  }
}
