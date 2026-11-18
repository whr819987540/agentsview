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
 */
import type { DataChangedEvent } from "../api/client.js";

const SCOPES: ReadonlySet<string> = new Set<DataChangedEvent["scope"]>([
  "messages",
  "sessions",
  "sync",
]);

interface RelayMessage {
  type: "data_changed";
  scope: DataChangedEvent["scope"];
}

export interface SharedEventsOptions {
  /** Lock and channel name. Tabs with the same name share one stream. */
  name: string;
  /** This tab now holds the lock and should open the events stream. */
  onLeader: () => void;
  /** The leader relayed an event from its stream. */
  onRelayed: (e: DataChangedEvent) => void;
  /** The browser refused the lock, so this tab must keep its own stream. */
  onUnavailable: () => void;
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

  navigator.locks
    .request(opts.name, { signal: waiting.signal }, () => {
      if (left) return undefined;
      opts.onLeader();
      // Hold the lock until this tab leaves or unloads.
      return new Promise<void>((resolve) => {
        releaseLock = resolve;
      });
    })
    .catch(() => {
      // Leaving aborts the wait. Any other rejection means this context
      // cannot hold locks, so the tab falls back to its own stream.
      if (!left) opts.onUnavailable();
    });

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
