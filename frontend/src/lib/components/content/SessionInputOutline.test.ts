// @vitest-environment jsdom
import {
  afterEach,
  beforeEach,
  describe,
  expect,
  it,
  vi,
} from "vitest";
import { mount, tick, unmount } from "svelte";

const mocks = vi.hoisted(() => ({
  fetchSessionInputOutline: vi.fn(),
}));

vi.mock("../../api/inputOutline.js", () => ({
  fetchSessionInputOutline: mocks.fetchSessionInputOutline,
}));

import { ui } from "../../stores/ui.svelte.js";
import { m } from "../../i18n/index.js";
// @ts-ignore
import SessionInputOutline from "./SessionInputOutline.svelte";

async function flush() {
  await tick();
  await Promise.resolve();
  await tick();
}

function makeItems(count: number) {
  return Array.from({ length: count }, (_, i) => ({
    ordinal: i * 2,
    preview: `prompt ${i}`,
    is_shell: false,
  }));
}

// jsdom has no layout, so tests supply the sizes the component reads.
function mockRenderedHeight(element: HTMLElement, height: number) {
  Object.defineProperty(element, "offsetHeight", {
    configurable: true,
    value: height,
  });
}

function mockPanelHeight(panel: HTMLElement, height: number) {
  Object.defineProperty(panel, "clientHeight", {
    configurable: true,
    value: height,
  });
}

function getSection() {
  const section = document.querySelector<HTMLElement>(".outline-section");
  expect(section).not.toBeNull();
  return section!;
}

function getResizeHandle() {
  return document.querySelector<HTMLElement>(
    `[aria-label="${m.session_input_outline_resize()}"]`,
  );
}

function pointerEvent(type: string, clientY: number) {
  return new PointerEvent(type, {
    bubbles: true,
    clientY,
    pointerId: 1,
  });
}

async function dragHandle(startY: number, endY: number) {
  const handle = getResizeHandle();
  expect(handle).not.toBeNull();

  handle!.dispatchEvent(pointerEvent("pointerdown", startY));
  handle!.dispatchEvent(pointerEvent("pointermove", endY));
  await tick();
  handle!.dispatchEvent(pointerEvent("pointerup", endY));
  await tick();
}

async function pressKey(key: string) {
  getResizeHandle()!.dispatchEvent(
    new KeyboardEvent("keydown", { bubbles: true, key }),
  );
  await tick();
}

describe("SessionInputOutline", () => {
  let component: ReturnType<typeof mount> | undefined;

  beforeEach(() => {
    mocks.fetchSessionInputOutline.mockReset();
    ui.selectedOrdinal = null;
    ui.pendingScrollOrdinal = null;
    ui.followLatest = true;
  });

  afterEach(() => {
    if (component) {
      unmount(component);
      component = undefined;
    }
    ui.setSessionInputOutlineHeight(240);
    vi.restoreAllMocks();
    document.body.innerHTML = "";
  });

  it("loads and renders input outline rows", async () => {
    mocks.fetchSessionInputOutline.mockResolvedValue({
      items: [
        {
          ordinal: 0,
          timestamp: "2026-07-01T10:00:00Z",
          preview: "first prompt",
          is_shell: false,
        },
        {
          ordinal: 2,
          timestamp: "2026-07-01T10:02:00Z",
          preview: "npm test",
          is_shell: true,
        },
      ],
      count: 2,
    });

    component = mount(SessionInputOutline, {
      target: document.body,
      props: { sessionId: "sess-1" },
    });
    await flush();

    expect(mocks.fetchSessionInputOutline).toHaveBeenCalledWith(
      "sess-1",
      expect.objectContaining({ includeForkContext: true }),
    );
    expect(document.body.textContent).toContain(
      m.session_input_outline_title(),
    );
    expect(document.body.textContent).toContain("first prompt");
    expect(document.body.textContent).toContain("npm test");
  });

  it("shows empty and error states", async () => {
    mocks.fetchSessionInputOutline.mockResolvedValue({
      items: [],
      count: 0,
    });
    component = mount(SessionInputOutline, {
      target: document.body,
      props: { sessionId: "empty" },
    });
    await flush();
    expect(document.body.textContent).toContain(
      m.session_input_outline_empty(),
    );
    expect(getResizeHandle()).toBeNull();

    unmount(component);
    component = undefined;
    document.body.innerHTML = "";
    mocks.fetchSessionInputOutline.mockRejectedValue(new Error("boom"));
    component = mount(SessionInputOutline, {
      target: document.body,
      props: { sessionId: "error" },
    });
    await flush();
    expect(document.body.textContent).toContain(
      m.session_input_outline_error(),
    );
  });

  it("jumps to the selected input ordinal", async () => {
    mocks.fetchSessionInputOutline.mockResolvedValue({
      items: [
        {
          ordinal: 7,
          preview: "jump target",
          is_shell: false,
        },
      ],
      count: 1,
    });
    component = mount(SessionInputOutline, {
      target: document.body,
      props: { sessionId: "sess-1" },
    });
    await flush();

    document.querySelector<HTMLButtonElement>(".outline-item")!.click();
    await tick();

    expect(ui.selectedOrdinal).toBe(7);
    expect(ui.pendingScrollOrdinal).toBe(7);
    expect(ui.followLatest).toBe(false);
  });

  describe("height", () => {
    // Stands in for the vitals panel the outline renders into.
    let panel: HTMLElement;

    beforeEach(() => {
      mocks.fetchSessionInputOutline.mockResolvedValue({
        items: makeItems(3),
        count: 3,
      });
      panel = document.createElement("div");
      document.body.appendChild(panel);
    });

    async function mountInPanel() {
      component = mount(SessionInputOutline, {
        target: panel,
        props: { sessionId: "sess-1" },
      });
      await flush();
    }

    it("limits the outline to the stored height", async () => {
      ui.setSessionInputOutlineHeight(360);

      await mountInPanel();

      expect(getSection().style.maxHeight).toBe("360px");
      expect(getResizeHandle()?.getAttribute("aria-valuenow")).toBe(
        "360",
      );
    });

    it("grows a short outline from its rendered height when dragged down", async () => {
      await mountInPanel();
      mockRenderedHeight(getSection(), 150);

      await dragHandle(400, 460);

      expect(ui.sessionInputOutlineHeight).toBe(210);
      expect(getSection().style.maxHeight).toBe("210px");
    });

    it("shrinks from arrow keys and stops at the minimum", async () => {
      await mountInPanel();
      mockRenderedHeight(getSection(), 240);

      await pressKey("ArrowUp");
      expect(ui.sessionInputOutlineHeight).toBe(216);

      await dragHandle(400, 0);
      expect(ui.sessionInputOutlineHeight).toBe(96);
      expect(getSection().style.maxHeight).toBe("96px");
    });

    it("keeps room for the rest of the vitals panel", async () => {
      mockPanelHeight(panel, 500);
      await mountInPanel();
      mockRenderedHeight(getSection(), 240);

      await dragHandle(400, 700);

      // A 500px panel keeps 160px for its title bar, the handle, and the
      // next section.
      expect(ui.sessionInputOutlineHeight).toBe(340);
      expect(getSection().style.maxHeight).toBe("340px");
      expect(getResizeHandle()?.getAttribute("aria-valuemax")).toBe(
        "340",
      );
    });

    it("fits a taller stored height to a short panel without forgetting it", async () => {
      ui.setSessionInputOutlineHeight(600);
      mockPanelHeight(panel, 500);

      await mountInPanel();
      expect(getSection().style.maxHeight).toBe("340px");

      mockRenderedHeight(getSection(), 340);
      await dragHandle(400, 480);

      expect(getSection().style.maxHeight).toBe("340px");
      expect(ui.sessionInputOutlineHeight).toBe(600);
    });
  });

  describe("active item scrolling", () => {
    const ITEM_HEIGHT = 30;
    const LIST_HEIGHT = 120;

    // Lays items out top to bottom at ITEM_HEIGHT each inside a list that
    // shows LIST_HEIGHT pixels.
    beforeEach(() => {
      vi.spyOn(HTMLElement.prototype, "offsetTop", "get").mockImplementation(
        function (this: HTMLElement) {
          if (!this.classList.contains("outline-item")) return 0;
          const items = Array.from(this.parentElement?.children ?? []);
          return items.indexOf(this) * ITEM_HEIGHT;
        },
      );
      vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(
        function (this: HTMLElement) {
          return this.classList.contains("outline-item") ? ITEM_HEIGHT : 0;
        },
      );
      vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(
        function (this: HTMLElement) {
          return this.classList.contains("outline-list") ? LIST_HEIGHT : 0;
        },
      );
      mocks.fetchSessionInputOutline.mockResolvedValue({
        items: makeItems(12),
        count: 12,
      });
    });

    function getList() {
      const list = document.querySelector<HTMLElement>(".outline-list");
      expect(list).not.toBeNull();
      return list!;
    }

    async function mountOutline() {
      component = mount(SessionInputOutline, {
        target: document.body,
        props: { sessionId: "sess-1" },
      });
      await flush();
      await tick();
    }

    it("centers a selected input that loads below the visible items", async () => {
      // Item 9 has ordinal 18.
      ui.selectedOrdinal = 18;

      await mountOutline();

      // Item 9 spans 270-300px; centering it in a 120px list puts the top
      // of the view at 270 - (120 - 30) / 2.
      expect(getList().scrollTop).toBe(225);
    });

    it("scrolls to an input selected after the outline loads", async () => {
      await mountOutline();
      expect(getList().scrollTop).toBe(0);

      // Item 6 has ordinal 12 and spans 180-210px.
      ui.selectedOrdinal = 12;
      await flush();

      expect(getList().scrollTop).toBe(135);
    });

    it("leaves the list in place when the selected input is visible", async () => {
      // Item 2 has ordinal 4 and spans 60-90px.
      ui.selectedOrdinal = 4;

      await mountOutline();

      expect(getList().scrollTop).toBe(0);
    });
  });
});
