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
});
