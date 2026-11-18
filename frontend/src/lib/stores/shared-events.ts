/**
 * One /api/v1/events stream per browser instead of one per tab.
 *
 * Browsers allow about six HTTP/1.1 connections per host, shared by every tab,
 * and the daemon serves plain HTTP on localhost. Each tab used to keep its own
 * events stream open for its whole life, so a few open tabs could take every
 * connection and leave new requests from all tabs waiting. Tabs that talk to
 * the same backend now elect one leader with the Web Locks API. The leader
 * holds the only events stream and relays each event to the other tabs over a
 * BroadcastChannel. When the leader closes, the browser releases its lock and
 * the next waiting tab takes over.
 *
 * Browsers offer Web Locks only in secure contexts: localhost, 127.0.0.1 and
 * HTTPS. Over plain HTTP from another machine each tab keeps its own stream.
 */
import type { DataChangedEvent } from "../api/client.js";

const SCOPES: ReadonlySet<string> = new Set<DataChangedEvent["scope"]>([
  "messages",
  "sessions",
  "sync",
]);

// Rejections that mean this context cannot use locks at all. Anything else,
// such as WebKit dropping the locks of a page it moves into the back/forward
// cache, ends this membership but not the tab's ability to rejoin.
const UNSUPPORTED_ERRORS: ReadonlySet<string> = new Set([
  "SecurityError",
  "NotSupportedError",
  "InvalidStateError",
]);

interface RelayMessage {
  type: "data_changed";
  scope: DataChangedEvent["scope"];
}

export interface SharedEventsOptions {
  /** Lock and channel name. Tabs with the same name share one stream. */
  name: string;
  /**
   * This tab now holds the lock and should open the events stream.
   * `handover` is true when it waited for another tab to let go, so events
   * from the gap may be missing.
   */
  onLeader: (handover: boolean) => void;
  /** The leader relayed an event from its stream. */
  onRelayed: (e: DataChangedEvent) => void;
  /** The browser refused the lock, so this tab must keep its own stream. */
  onUnavailable: () => void;
  /** The browser took the lock away; this membership is over. */
  onLost: () => void;
}

export interface SharedEvents {
  /** Pass an event from this tab's stream to the other tabs. */
  relay: (e: DataChangedEvent) => void;
  /** Stop leading or waiting, and close the channel. */
  leave: () => void;
}

/** True when the browser can share one stream across tabs. */
export function sharedEventsSupported(): boolean {
  return (
    typeof navigator !== "undefined" &&
    typeof navigator.locks?.request === "function" &&
    typeof BroadcastChannel === "function"
  );
}

function isRelayMessage(data: unknown): data is RelayMessage {
  if (typeof data !== "object" || data === null) return false;
  const msg = data as { type?: unknown; scope?: unknown };
  return msg.type === "data_changed" && typeof msg.scope === "string" && SCOPES.has(msg.scope);
}

export function joinSharedEvents(opts: SharedEventsOptions): SharedEvents {
  const channel = new BroadcastChannel(opts.name);
  channel.onmessage = (ev: MessageEvent) => {
    if (isRelayMessage(ev.data)) opts.onRelayed({ scope: ev.data.scope });
  };

  const waiting = new AbortController();
  let releaseLock: (() => void) | null = null;
  let left = false;

  const lead = (handover: boolean) => {
    if (left) return undefined;
    opts.onLeader(handover);
    // Hold the lock until this tab leaves or unloads.
    return new Promise<void>((resolve) => {
      releaseLock = resolve;
    });
  };
  const failed = (err: unknown) => {
    if (left) return;
    // DOMException is not an Error subclass everywhere, so read its name.
    const name =
      typeof err === "object" && err !== null && "name" in err ? String(err.name) : "";
    if (UNSUPPORTED_ERRORS.has(name)) opts.onUnavailable();
    else opts.onLost();
  };

  // Take the lock at once when nobody holds it. Otherwise wait in line: by
  // the time the lock comes free, the previous leader's stream has closed.
  navigator.locks
    .request(opts.name, { ifAvailable: true }, (lock) => {
      if (lock !== null) return lead(false);
      navigator.locks
        .request(opts.name, { signal: waiting.signal }, () => lead(true))
        .catch(failed);
      return undefined;
    })
    .catch(failed);

  return {
    relay(e) {
      if (left) return;
      const msg: RelayMessage = { type: "data_changed", scope: e.scope };
      channel.postMessage(msg);
    },
    leave() {
      if (left) return;
      left = true;
      waiting.abort();
      releaseLock?.();
      releaseLock = null;
      channel.close();
    },
  };
}
