import { EventSource } from "eventsource";
import { afterEach, beforeEach, describe, expect, it, vi } from "vite-plus/test";
// Import the store statically: it pulls in the generated API client, which
// can take longer than a hook's 10 s limit to transform while the full suite
// runs in parallel. Collection has no such limit.
import { createEventsStore, events, EVENTS_STORE_HEAL_INTERVAL_MS } from "./events.svelte.js";
import { VISIBLE_CATCH_UP_DELAY_MS } from "../utils/page-visibility.js";

vi.mock("eventsource", () => ({ EventSource: vi.fn() }));

// Minimal EventSource stub. Tests control when events fire and
// assert on the number of instances created.
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  public url: string;
  public readyState = 1;
  private listeners: Record<string, ((ev: MessageEvent) => void)[]> = {};
  public onerror: ((ev: Event) => void) | null = null;
  public closed = false;

  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }

  addEventListener(name: string, cb: (ev: MessageEvent) => void) {
    (this.listeners[name] ||= []).push(cb);
  }

  close() {
    this.closed = true;
    this.readyState = 2; // EventSource.CLOSED per HTML spec
  }

  fireError() {
    if (this.onerror) this.onerror(new Event("error"));
  }

  fire(name: string, data: unknown) {
    const payload = { data: JSON.stringify(data) } as MessageEvent;
    (this.listeners[name] || []).forEach((cb) => cb(payload));
  }

  fireOpen() {
    (this.listeners["open"] || []).forEach((cb) => cb(new Event("open") as MessageEvent));
  }

  static reset() {
    FakeEventSource.instances = [];
  }
}

beforeEach(() => {
  FakeEventSource.reset();
  vi.mocked(EventSource).mockImplementation(function (url) {
    return new FakeEventSource(String(url)) as unknown as EventSource;
  });
  events.setAvailable(true);
});

let visibilityChanged = false;
function setVisibility(state: DocumentVisibilityState) {
  visibilityChanged = true;
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  Object.defineProperty(document, "hidden", { configurable: true, get: () => state === "hidden" });
  document.dispatchEvent(new Event("visibilitychange"));
}

const pause = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/** Show the tab and wait for the catch-up that follows. */
async function showTab() {
  setVisibility("visible");
  await pause(VISIBLE_CATCH_UP_DELAY_MS + 20);
}

afterEach(async () => {
  vi.useRealTimers();
  if (visibilityChanged) {
    visibilityChanged = false;
    await showTab();
  }
  vi.unstubAllGlobals();
});

describe("events store", () => {
  it("does not open an EventSource while live events are unavailable", () => {
    events.setAvailable(false);

    const unsub = events.subscribe(() => {});

    expect(FakeEventSource.instances).toHaveLength(0);

    events.setAvailable(true);
    expect(FakeEventSource.instances).toHaveLength(1);

    unsub();
  });

  it("opens a single EventSource on first subscribe", () => {
    const unsub1 = events.subscribe(() => {});
    const unsub2 = events.subscribe(() => {});
    expect(FakeEventSource.instances).toHaveLength(1);
    unsub1();
    unsub2();
  });

  it("closes the EventSource when the last subscriber leaves", () => {
    const unsub = events.subscribe(() => {});
    const es = FakeEventSource.instances[0]!;
    expect(es.closed).toBe(false);
    unsub();
    expect(es.closed).toBe(true);
  });

  it("delivers events to every subscriber", () => {
    const received: string[] = [];
    const unsub1 = events.subscribe((e) => received.push(`a:${e.scope}`));
    const unsub2 = events.subscribe((e) => received.push(`b:${e.scope}`));
    FakeEventSource.instances[0]!.fire("data_changed", { scope: "messages" });
    expect(received).toEqual(["a:messages", "b:messages"]);
    unsub1();
    unsub2();
  });

  it("tracks duplicate subscriptions independently", () => {
    const fn = vi.fn();
    const unsub1 = events.subscribe(fn);
    const unsub2 = events.subscribe(fn);

    expect(FakeEventSource.instances).toHaveLength(1);

    FakeEventSource.instances[0]!.fire("data_changed", { scope: "messages" });
    expect(fn).toHaveBeenCalledTimes(2);

    // First unsubscribe removes only one entry; connection stays open.
    unsub1();
    expect(FakeEventSource.instances[0]!.closed).toBe(false);

    fn.mockClear();
    FakeEventSource.instances[0]!.fire("data_changed", { scope: "sessions" });
    expect(fn).toHaveBeenCalledTimes(1);

    // Second unsubscribe closes the connection.
    unsub2();
    expect(FakeEventSource.instances[0]!.closed).toBe(true);
  });

  it("self-heals a closed EventSource after a transient failure", async () => {
    vi.useFakeTimers();
    const received: string[] = [];
    const unsub = events.subscribe((e) => received.push(e.scope));
    const first = FakeEventSource.instances[0]!;

    // Simulate a transient failure: the stream opens successfully
    // at least once, then later errors trip the circuit breaker.
    // This is the "worked once, then failed" case that should heal.
    first.fireOpen();
    for (let i = 0; i < 5; i++) first.fireError();
    expect(first.closed).toBe(true);

    // Advance to the heal-timer tick. The store should notice the
    // closed ES and rebuild while the listener is still active.
    await vi.advanceTimersByTimeAsync(EVENTS_STORE_HEAL_INTERVAL_MS);
    expect(FakeEventSource.instances.length).toBe(2);
    const second = FakeEventSource.instances[1]!;
    expect(second.closed).toBe(false);

    second.fire("data_changed", { scope: "messages" });
    expect(received).toEqual(["messages"]);

    unsub();
    vi.useRealTimers();
  });

  it("does not heal after a permanent failure (never opened)", async () => {
    vi.useFakeTimers();
    const unsub = events.subscribe(() => {});
    const first = FakeEventSource.instances[0]!;

    // Permanent failure pattern: errors fire without any open ever
    // succeeding (e.g. PG serve 503 on /api/v1/events). The circuit
    // breaker trips AND marks the store permanentlyFailed, so no
    // heal timer rebuilds.
    for (let i = 0; i < 5; i++) first.fireError();
    expect(first.closed).toBe(true);

    // Advance far past the heal interval. The store must NOT
    // rebuild — doing so would reintroduce retry churn on an
    // endpoint that has already been classified as unreachable.
    await vi.advanceTimersByTimeAsync(EVENTS_STORE_HEAL_INTERVAL_MS * 3);
    expect(FakeEventSource.instances.length).toBe(1);

    unsub();
    vi.useRealTimers();
  });

  it("reopens the EventSource after a transient close on new subscribe", () => {
    const received: string[] = [];
    const unsub = events.subscribe((e) => received.push(e.scope));
    const first = FakeEventSource.instances[0]!;

    // Transient-close pattern: open fires once so the failure is
    // classified as recoverable, then the breaker trips.
    first.fireOpen();
    for (let i = 0; i < 5; i++) first.fireError();
    expect(first.closed).toBe(true);
    expect(first.readyState).toBe(2);

    // A new subscriber should trigger a fresh EventSource rather
    // than silently returning with a stale CLOSED handle.
    const unsub2 = events.subscribe((e) => received.push(`b:${e.scope}`));
    expect(FakeEventSource.instances.length).toBe(2);
    const second = FakeEventSource.instances[1]!;
    expect(second.closed).toBe(false);

    second.fire("data_changed", { scope: "messages" });
    expect(received).toContain("messages");
    expect(received).toContain("b:messages");

    unsub();
    unsub2();
  });

  it("holds events while the tab is hidden and delivers the latest per scope once shown", async () => {
    const received: string[] = [];
    const unsub = events.subscribe((e) => received.push(e.scope));
    const es = FakeEventSource.instances[0]!;

    setVisibility("hidden");
    es.fire("data_changed", { scope: "messages" });
    es.fire("data_changed", { scope: "sessions" });
    es.fire("data_changed", { scope: "messages" });
    expect(received).toEqual([]);

    await showTab();
    expect(received).toEqual(["sessions", "messages"]);

    // Delivered once: a second visibility change has nothing left.
    setVisibility("hidden");
    await showTab();
    expect(received).toEqual(["sessions", "messages"]);
    unsub();
  });

  it("drops held events when the last subscriber leaves", async () => {
    const unsub = events.subscribe(() => {});
    setVisibility("hidden");
    FakeEventSource.instances[0]!.fire("data_changed", { scope: "sync" });
    unsub();

    const received: string[] = [];
    const unsub2 = events.subscribe((e) => received.push(e.scope));
    await showTab();
    expect(received).toEqual([]);
    unsub2();
  });

  it("reopens a stream the circuit breaker closed when the tab is shown", async () => {
    const unsub = events.subscribe(() => {});
    const first = FakeEventSource.instances[0]!;
    first.fireOpen();
    for (let i = 0; i < 5; i++) first.fireError();
    expect(first.closed).toBe(true);

    setVisibility("hidden");
    await showTab();
    expect(FakeEventSource.instances).toHaveLength(2);
    unsub();
  });

  it("debounces rapid events into one callback per debounce window", () => {
    vi.useFakeTimers();
    const received: string[] = [];
    const unsub = events.subscribeDebounced((e) => received.push(e.scope), 100);
    const es = FakeEventSource.instances[0]!;
    es.fire("data_changed", { scope: "messages" });
    es.fire("data_changed", { scope: "messages" });
    es.fire("data_changed", { scope: "sessions" });
    expect(received).toEqual([]);
    vi.advanceTimersByTime(100);
    expect(received).toEqual(["sessions"]); // last-write-wins
    unsub();
    vi.useRealTimers();
  });
});

// Web Locks with exclusive locks granted in request order, as browsers do.
class FakeLocks {
  refuse = false;
  private holders = new Map<string, { reject: (err: unknown) => void; release: () => void }>();
  private queues = new Map<string, { run: () => void }[]>();

  request(
    name: string,
    options: { signal?: AbortSignal; ifAvailable?: boolean },
    callback: (lock: { name: string } | null) => unknown,
  ): Promise<unknown> {
    if (this.refuse) return Promise.reject(new DOMException("denied", "SecurityError"));
    if (options.ifAvailable && this.holders.has(name)) {
      return Promise.resolve().then(() => callback(null));
    }
    return new Promise((resolve, reject) => {
      const queue = this.queues.get(name) ?? [];
      this.queues.set(name, queue);
      const entry = {
        run: () => {
          let settled = false;
          const release = () => {
            if (settled) return;
            settled = true;
            this.holders.delete(name);
            this.queues.get(name)?.shift()?.run();
          };
          this.holders.set(name, {
            reject: (err) => {
              release();
              reject(err);
            },
            release,
          });
          Promise.resolve()
            .then(() => callback({ name }))
            .then(
              (value) => {
                if (settled) return;
                release();
                resolve(value);
              },
              (err: unknown) => {
                if (settled) return;
                release();
                reject(err);
              },
            );
        },
      };
      options.signal?.addEventListener("abort", () => {
        const index = queue.indexOf(entry);
        if (index === -1) return;
        queue.splice(index, 1);
        reject(new DOMException("aborted", "AbortError"));
      });
      queue.push(entry);
      if (!this.holders.has(name)) queue.shift()!.run();
    });
  }

  /** Take the lock away from its holder, as WebKit does for a cached page. */
  drop(name: string) {
    this.holders.get(name)?.reject(new DOMException("going away", "AbortError"));
  }
}

// BroadcastChannel that delivers to the other same-named channels later.
class FakeBroadcastChannel {
  static open = new Set<FakeBroadcastChannel>();
  onmessage: ((ev: MessageEvent) => void) | null = null;

  constructor(public name: string) {
    FakeBroadcastChannel.open.add(this);
  }

  postMessage(data: unknown) {
    for (const other of FakeBroadcastChannel.open) {
      if (other === this || other.name !== this.name) continue;
      queueMicrotask(() => other.onmessage?.({ data } as MessageEvent));
    }
  }

  close() {
    FakeBroadcastChannel.open.delete(this);
  }
}

describe("events store shared across tabs", () => {
  let locks: FakeLocks;

  beforeEach(() => {
    locks = new FakeLocks();
    Object.defineProperty(navigator, "locks", { configurable: true, value: locks });
    vi.stubGlobal("BroadcastChannel", FakeBroadcastChannel);
  });

  afterEach(() => {
    delete (navigator as { locks?: unknown }).locks;
    FakeBroadcastChannel.open.clear();
  });

  function openTab(received: string[]) {
    const tab = createEventsStore();
    tab.setAvailable(true);
    return tab.subscribe((e) => received.push(e.scope));
  }

  it("opens one stream for two tabs and delivers its events to both", async () => {
    const a: string[] = [];
    const b: string[] = [];
    const unsubA = openTab(a);
    const unsubB = openTab(b);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));

    FakeEventSource.instances[0]!.fire("data_changed", { scope: "sessions" });
    await vi.waitFor(() => {
      expect(a).toEqual(["sessions"]);
      expect(b).toEqual(["sessions"]);
    });
    expect(FakeEventSource.instances).toHaveLength(1);
    unsubA();
    unsubB();
  });

  it("hands the stream to the next tab when the leader stops listening", async () => {
    const a: string[] = [];
    const b: string[] = [];
    const unsubA = openTab(a);
    const unsubB = openTab(b);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));

    unsubA();
    expect(FakeEventSource.instances[0]!.closed).toBe(true);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));

    FakeEventSource.instances[1]!.fire("data_changed", { scope: "messages" });
    await vi.waitFor(() => expect(b).toEqual(["messages"]));
    expect(a).toEqual([]);
    unsubB();
  });

  it("holds relayed events while the tab is hidden", async () => {
    const a: string[] = [];
    const b: string[] = [];
    const unsubA = openTab(a);
    const unsubB = openTab(b);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));

    setVisibility("hidden");
    FakeEventSource.instances[0]!.fire("data_changed", { scope: "sync" });
    await pause(0);
    expect(b).toEqual([]);

    await showTab();
    expect(a).toEqual(["sync"]);
    expect(b).toEqual(["sync"]);
    unsubA();
    unsubB();
  });

  it("refreshes every tab once after another tab takes the stream over", async () => {
    const a: string[] = [];
    const b: string[] = [];
    const c: string[] = [];
    const unsubA = openTab(a);
    const unsubB = openTab(b);
    const unsubC = openTab(c);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    // The first leader found the lock free, so it has nothing to catch up.
    FakeEventSource.instances[0]!.fireOpen();
    await pause(0);
    expect([a, b, c]).toEqual([[], [], []]);

    unsubA();
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
    FakeEventSource.instances[1]!.fireOpen();
    await vi.waitFor(() => {
      expect(b).toEqual(["sync"]);
      expect(c).toEqual(["sync"]);
    });
    unsubB();
    unsubC();
  });

  it("lets another tab lead when the leader's stream fails for good", async () => {
    const b: string[] = [];
    const unsubA = openTab([]);
    const unsubB = openTab(b);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));

    // Errors before any successful open: the endpoint looks unreachable.
    for (let i = 0; i < 5; i++) FakeEventSource.instances[0]!.fireError();
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
    FakeEventSource.instances[1]!.fire("data_changed", { scope: "messages" });
    await vi.waitFor(() => expect(b).toEqual(["messages"]));
    unsubA();
    unsubB();
  });

  it("rejoins when shown after the browser takes the lock away", async () => {
    const unsub = openTab([]);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));

    locks.drop("agentsview-events:");
    await vi.waitFor(() => expect(FakeEventSource.instances[0]!.closed).toBe(true));
    setVisibility("hidden");
    await showTab();
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
    unsub();
  });

  it("catches up after the page comes back from the back/forward cache", async () => {
    const received: string[] = [];
    const unsub = openTab(received);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));

    window.dispatchEvent(new Event("pagehide"));
    expect(FakeEventSource.instances[0]!.closed).toBe(true);
    const restored = new Event("pageshow");
    Object.defineProperty(restored, "persisted", { value: true });
    window.dispatchEvent(restored);

    expect(received).toEqual(["sync"]);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(2));
    unsub();
  });

  it("keeps a stream of its own when the browser refuses the lock", async () => {
    locks.refuse = true;
    const unsub = openTab([]);
    await vi.waitFor(() => expect(FakeEventSource.instances).toHaveLength(1));
    unsub();
  });
});
