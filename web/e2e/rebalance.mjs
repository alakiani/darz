// Rebalances the Endpoint Slots from the overview, as an Admin would:
// preview the moves, apply them, and see the slots spread. The Go lab test
// (TestRebalanceSlots in internal/e2e/slots_test.go) forces every slot onto
// one of three Bridges, runs this, and checks ArvanCloud's records.
//
// Env: PANEL_URL, ADMIN_HANDLE, ADMIN_PASSWORD, TOTP_SECRET.
import { chromium } from "@playwright/test";
import { totp } from "./totp.mjs";

const { PANEL_URL, ADMIN_HANDLE, ADMIN_PASSWORD, TOTP_SECRET } = process.env;

const step = (name) => console.log(`- ${name}`);
const browser = await chromium.launch();
try {
  const page = await browser.newPage();
  page.setDefaultTimeout(30_000);

  step("sign in with TOTP");
  await page.goto(`${PANEL_URL}/#/admin`);
  await page.fill("input[name=handle]", ADMIN_HANDLE);
  await page.fill("input[name=password]", ADMIN_PASSWORD);
  await page.click("button[type=submit]");
  await page.fill("input[name=code]", totp(TOTP_SECRET));
  await page.click("button[type=submit]");
  await page.getByRole("heading", { name: "Overview" }).waitFor();

  const slots = page.locator("[data-testid=endpoint-slots] tr");
  const bridges = async () => new Set(await slots.locator("td:nth-child(2)").allTextContents());
  await page.waitForFunction(() => document.querySelectorAll("[data-testid=endpoint-slots] tr").length === 3);
  const before = await bridges();
  if (before.size !== 1) throw new Error(`the slots start on ${[...before]}, want one Bridge`);

  step("preview: two moves and the note for Users");
  const box = page.locator("[data-testid=slot-rebalance]");
  await box.getByRole("button", { name: "Rebalance slots" }).click();
  const plan = box.locator("[data-testid=rebalance-plan]");
  await plan.waitFor();
  if ((await plan.locator("li").count()) !== 2) throw new Error(`plan: ${await plan.textContent()}`);
  await plan.getByText("toggle their tunnel").waitFor();
  // The preview moves nothing.
  if ((await bridges()).size !== 1) throw new Error("the preview moved a slot");

  step("apply: the slots spread over three Bridges");
  await plan.getByRole("button", { name: "Apply" }).click();
  await box.locator("[data-testid=rebalance-moved]", { hasText: "Moved 2 slots" }).waitFor();
  await page.waitForFunction(() => {
    const cells = [...document.querySelectorAll("[data-testid=endpoint-slots] tr td:nth-child(2)")];
    return new Set(cells.map((c) => c.textContent)).size === 3;
  });

  step("preview again: already balanced");
  await box.getByRole("button", { name: "Rebalance slots" }).click();
  await box.locator("[data-testid=rebalance-balanced]").waitFor();

  console.log("rebalanced the slots from the panel");
} catch (e) {
  console.error(e);
  process.exitCode = 1;
} finally {
  await browser.close();
}
