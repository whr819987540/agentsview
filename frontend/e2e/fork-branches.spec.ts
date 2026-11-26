import { expect, test, type Page } from "@playwright/test";

// The fixture's fork starts a week after every message of its parent, so
// it inherits the parent's whole transcript: 8 messages over 4 turns.
const PARENT = "test-session-medium-8";
const FORK = "test-session-fork-1";

async function openTranscript(page: Page, sessionId: string) {
  await page.goto(`/sessions/${sessionId}`);
  const list = page.locator(".message-list-scroll");
  await expect(list).toHaveAttribute("data-messages-session-id", sessionId);
  await expect(list).toHaveAttribute("data-loaded", "true");
  return list;
}

test("a fork starts with its inherited messages collapsed behind the boundary", async ({
  page,
}) => {
  const list = await openTranscript(page, FORK);

  const note = list.locator(".fork-note");
  await expect(note).toContainText(/Forked from .+ after turn 4/);
  await expect(note).toContainText("History inherited from the parent session is hidden.");
  await expect(list.locator('.virtual-row[data-ordinal="-2"]')).toHaveCount(0);

  await note.getByRole("button", { name: "Show 8 inherited messages" }).click();

  await expect(list.locator('.virtual-row[data-ordinal="-2"]')).toHaveCount(1);
  await expect(note.getByRole("button", { name: "Hide inherited messages" })).toBeVisible();
});

test("a parent marks where its fork branched off and links to it", async ({ page }) => {
  const list = await openTranscript(page, PARENT);
  await list.evaluate((el) => {
    el.scrollTop = el.scrollHeight;
    el.dispatchEvent(new Event("scroll"));
  });

  const marker = list.locator('[data-branch-kind="fork"]');
  await expect(marker).toHaveCount(1);
  await expect(marker).toContainText("Forked here:");
  await expect(marker).toContainText("7 turns");
  await expect(
    list.locator('.virtual-row[data-ordinal="7"] [data-branch-kind="fork"]'),
  ).toHaveCount(1);

  await marker.getByRole("link").click();
  await expect(page).toHaveURL(new RegExp(`/sessions/${FORK}`));
  await expect(page.locator(".message-list-scroll")).toHaveAttribute(
    "data-messages-session-id",
    FORK,
  );
});
