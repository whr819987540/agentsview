// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vite-plus/test";
import { createRawSnippet, flushSync, mount, tick, unmount } from "svelte";
import LazyMount from "./LazyMount.svelte";

const content = createRawSnippet(() => ({
  render: () => `<p class="lazy-content">loaded</p>`,
}));

class FakeObserver {
  static instances: FakeObserver[] = [];
  observed: Element[] = [];
  disconnected = false;

  constructor(
    readonly callback: IntersectionObserverCallback,
    readonly options: IntersectionObserverInit,
  ) {
    FakeObserver.instances.push(this);
  }

  observe(target: Element) {
    this.observed.push(target);
  }

  unobserve() {}

  disconnect() {
    this.disconnected = true;
  }

  fire(isIntersecting: boolean) {
    const entries = this.observed.map(
      (target) => ({ target, isIntersecting }) as IntersectionObserverEntry,
    );
    flushSync(() => this.callback(entries, this as unknown as IntersectionObserver));
  }
}

describe("LazyMount", () => {
  let component: ReturnType<typeof mount> | undefined;

  afterEach(() => {
    if (component) unmount(component);
    component = undefined;
    FakeObserver.instances = [];
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
  });

  it("mounts right away where IntersectionObserver is missing", async () => {
    vi.stubGlobal("IntersectionObserver", undefined);
    component = mount(LazyMount, {
      target: document.body,
      props: { root: undefined, children: content },
    });
    await tick();

    expect(document.querySelector(".lazy-content")?.textContent).toBe("loaded");
    expect(document.querySelector(".lazy-mount-placeholder")).toBeNull();
  });

  it("holds a placeholder until the content nears the root, then keeps it", async () => {
    vi.stubGlobal("IntersectionObserver", FakeObserver);
    const root = document.createElement("div");
    component = mount(LazyMount, {
      target: document.body,
      props: { root, placeholderHeight: 300, rootMargin: "100px 0px", children: content },
    });
    await tick();

    const placeholder = document.querySelector<HTMLElement>(".lazy-mount-placeholder")!;
    expect(placeholder.style.minHeight).toBe("300px");
    expect(document.querySelector(".lazy-content")).toBeNull();
    expect(FakeObserver.instances).toHaveLength(1);
    const observer = FakeObserver.instances[0]!;
    expect(observer.options).toEqual({ root, rootMargin: "100px 0px" });
    expect(observer.observed).toEqual([placeholder]);

    observer.fire(false);
    await tick();
    expect(document.querySelector(".lazy-content")).toBeNull();

    observer.fire(true);
    await tick();
    expect(document.querySelector(".lazy-content")?.textContent).toBe("loaded");
    expect(document.querySelector(".lazy-mount-placeholder")).toBeNull();
    expect(observer.disconnected).toBe(true);

    // Scrolling away again must not unmount what the reader has seen.
    observer.fire(false);
    await tick();
    expect(document.querySelector(".lazy-content")?.textContent).toBe("loaded");
  });

  it("waits for the root before observing", async () => {
    vi.stubGlobal("IntersectionObserver", FakeObserver);
    component = mount(LazyMount, {
      target: document.body,
      props: { root: undefined, children: content },
    });
    await tick();

    expect(FakeObserver.instances).toHaveLength(0);
    expect(document.querySelector(".lazy-content")).toBeNull();
  });
});
