import { test, expect } from "@playwright/test";
import { navButton } from "./helpers";

// Smoke test: the Requests nav item exists and its empty state renders.

test("Requests nav item is reachable and shows the empty state", async ({ page }) => {
  await page.goto("/admin/");

  await navButton(page, "Requests").click();
  await expect(page).toHaveURL(/#\/requests$/);

  await expect(page.getByRole("heading", { name: "Requests" })).toBeVisible();
  await expect(page.getByText("No estimate requests yet.")).toBeVisible();
});
