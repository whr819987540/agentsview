import { afterEach, describe, expect, it } from "vite-plus/test";
import { chooseHeaderDensity, fitHeaderDensity, HEADER_DENSITIES } from "./header-density.js";

/** Content widths, in pixels, of a header row at each density in
 *  HEADER_DENSITIES order. */
const WIDTHS = [900, 700, 640, 560, 200];

/** A header whose action buttons end where the row's content ends at the
 *  current density, as a browser lays it out. */
function setup(rowWidth: number) {
  const header = document.createElement("div");
  const row = document.createElement("span");
  const actions = document.createElement("div");
  row.append(actions);
  header.append(row);
  document.body.append(header);
  const box = (right: number) => ({ right }) as DOMRect;
  row.getBoundingClientRect = () => box(rowWidth);
  actions.getBoundingClientRect = () => {
    const index = HEADER_DENSITIES.indexOf(header.dataset.density as never);
    return box(WIDTHS[index] ?? Infinity);
  };
  return { header, row, actions };
}

afterEach(() => {
  document.body.replaceChildren();
});

describe("chooseHeaderDensity", () => {
  it("chooses the roomiest density that keeps the actions inside the row", () => {
    const { header, row, actions } = setup(650);

    expect(chooseHeaderDensity(header, row, actions)).toBe(2);
    expect(header.dataset.density).toBe("compact tight");
  });

  it("keeps everything when the full layout fits", () => {
    const { header, row, actions } = setup(900);
    header.dataset.density = "compact tight";

    expect(chooseHeaderDensity(header, row, actions)).toBe(0);
    expect(header.dataset.density).toBe("");
  });

  it("stays at the tightest density when nothing fits", () => {
    const { header, row, actions } = setup(150);

    expect(chooseHeaderDensity(header, row, actions)).toBe(4);
    expect(header.dataset.density).toBe("compact tight no-usage actions-only");
  });
});

describe("fitHeaderDensity", () => {
  it("fits again when the header's content changes and lets go on cleanup", async () => {
    const { header, row, actions } = setup(650);
    const cleanup = fitHeaderDensity(() => actions)(row)!;
    header.dataset.density = "";

    // A session with more badges opens in the same header.
    row.append(document.createTextNode("More badges"));
    await Promise.resolve();
    expect(header.dataset.density).toBe("compact tight");

    cleanup();
    expect(header.hasAttribute("data-density")).toBe(false);
    row.append(document.createTextNode("Even more"));
    await Promise.resolve();
    expect(header.hasAttribute("data-density")).toBe(false);
  });
});
