// Walks the User panel's Quota card, Usage page and Device page as four
// Users would (ADR 0010). The Go test (internal/e2e/usage_panel_test.go)
// sets up their Quotas, Usage and Refills, runs this, and checks the
// effects in the Coordinator's store.
//
// Env: PANEL_URL, PASSWORD.
import { chromium } from "@playwright/test";

const { PANEL_URL, PASSWORD } = process.env;

const step = (name) => console.log(`- ${name}`);
const fail = (msg) => {
  throw new Error(msg);
};
// Dates in the User panel are Solar Hijri, in Farsi digits: 2026 is 1405.
const hijri = /۱۴۰[۵۶]/;

const browser = await chromium.launch();
try {
  const signIn = async (handle) => {
    const ctx = await browser.newContext({ locale: "fa-IR", timezoneId: "Asia/Tehran" });
    const page = await ctx.newPage();
    page.setDefaultTimeout(20_000);
    await page.goto(`${PANEL_URL}/#/`);
    await page.fill("input[name=handle]", handle);
    await page.fill("input[name=password]", PASSWORD);
    await page.click("button[type=submit]");
    await page.locator("[data-testid=quota-card]").waitFor();
    return page;
  };
  const card = (page) => page.locator("[data-testid=quota-card]");
  const expectCard = async (page, state, percent) => {
    await page.locator(`[data-testid=quota-card][data-state=${state}]`).waitFor();
    const got = await card(page).getAttribute("data-percent");
    if (percent !== undefined && got !== String(percent)) fail(`card at ${got}%, want ${percent}%`);
  };
  const text = async (locator) => (await locator.textContent()) ?? "";

  step("sara: the card at 0%, with no banner");
  {
    const page = await signIn("sara");
    await expectCard(page, "ok", 0);
    if ((await card(page).locator("progress").count()) !== 1) fail("sara's card has no progress bar");
    if (!(await text(card(page).locator("[data-testid=quota-used]"))).includes("۱۰ GB")) fail("sara's card does not show her 10 GB Quota");
    if (!hijri.test(await text(card(page).locator("[data-testid=quota-renews]")))) fail("the renewal date is not Solar Hijri");
    if ((await page.locator("[data-testid=quota-warning], [data-testid=quota-out]").count()) !== 0) fail("sara sees a banner at 0%");
    // The card sits above the Device list.
    const order = await page.evaluate(() => {
      const c = document.querySelector("[data-testid=quota-card]");
      const h = [...document.querySelectorAll("h1")].find((e) => e.textContent.includes("دستگاه‌ها"));
      return c && h ? c.compareDocumentPosition(h) & Node.DOCUMENT_POSITION_FOLLOWING : 0;
    });
    if (!order) fail("the Quota card is not above the Device list");
    await page.context().close();
  }

  step("maryam: the card at 85%, with the 80% banner");
  {
    const page = await signIn("maryam");
    await expectCard(page, "warn", 85);
    await page.locator("[data-testid=quota-warning]").waitFor();
    if ((await page.locator("[data-testid=quota-out]").count()) !== 0) fail("maryam sees the 100% banner at 85%");

    step("maryam: the Usage page, newest period first, split by Device");
    await page.goto(`${PANEL_URL}/#/usage`);
    const periods = page.locator("[data-testid=period]");
    await periods.first().waitFor();
    if ((await periods.count()) !== 2) fail(`maryam has ${await periods.count()} periods, want 2`);
    if (!(await text(periods.nth(0))).includes("۸٫۵ GB")) fail(`the current period reads "${await text(periods.nth(0))}"`);
    if (!(await text(periods.nth(1))).includes("۳ GB")) fail(`the last period reads "${await text(periods.nth(1))}"`);
    if (!hijri.test(await text(periods.nth(0)))) fail("the period's dates are not Solar Hijri");
    await periods.nth(0).getByRole("button").click();
    const devices = page.locator("[data-testid=period-device]");
    await devices.first().waitFor();
    if ((await devices.count()) !== 2) fail(`the current period lists ${await devices.count()} Devices, want 2`);
    if ((await page.locator("[data-testid=period-revoked]").count()) !== 0) fail("a Revoked Devices row before any Device was revoked");

    step("maryam: the Device page shows this Quota Period; revoke the laptop");
    await page.goto(`${PANEL_URL}/#/`);
    await page.getByRole("link", { name: /laptop/ }).click();
    await page.getByRole("heading", { name: "این دوره" }).waitFor();
    page.once("dialog", (d) => d.accept());
    await page.getByRole("button", { name: "باطل کردن" }).click();
    await page.locator("[data-testid=quota-card]").waitFor();

    step("maryam: the Usage page now has a Revoked Devices row");
    await page.goto(`${PANEL_URL}/#/usage`);
    await periods.first().waitFor();
    await periods.nth(0).getByRole("button").click();
    const revoked = page.locator("[data-testid=period-revoked]");
    await revoked.waitFor();
    if (!(await text(revoked)).includes("۱ GB")) fail(`the Revoked Devices row reads "${await text(revoked)}"`);
    if ((await devices.count()) !== 1) fail(`the current period lists ${await devices.count()} live Devices, want 1`);
    // Revoking keeps the Usage: the period's total is unchanged.
    if (!(await text(periods.nth(0))).includes("۸٫۵ GB")) fail("revoking changed the period's total");
    await page.context().close();
  }

  step("reza: the card at 100%, blocked, with the renewal date and Refills");
  {
    const page = await signIn("reza");
    await expectCard(page, "out", 100);
    const out = page.locator("[data-testid=quota-out]");
    await out.waitFor();
    if (!hijri.test(await text(out))) fail(`the 100% banner shows no Solar Hijri renewal date: "${await text(out)}"`);
    const refills = page.locator("[data-testid=refills]");
    if (!(await text(refills)).includes("۲")) fail(`reza's Refills read "${await text(refills)}"`);
    if (!hijri.test(await text(refills))) fail("the next Refill expiry is not a Solar Hijri date");

    step("reza: redeem a Refill after a confirmation that shows what it adds");
    let asked = "";
    page.once("dialog", (d) => {
      asked = d.message();
      d.accept();
    });
    await page.getByRole("button", { name: "استفاده از یک شارژ" }).first().click();
    await expectCard(page, "ok", 50);
    if (!asked.includes("۵ GB")) fail(`the confirmation reads "${asked}", want what the Refill adds (5 GB)`);
    if ((await page.locator("[data-testid=quota-out]").count()) !== 0) fail("the 100% banner stays after redeeming");
    if (!(await text(card(page).locator("[data-testid=quota-remedies]"))).includes("۵ GB")) fail("the card does not show the 5 GB reset");
    if (!(await text(refills)).includes("۱")) fail(`after redeeming, reza's Refills read "${await text(refills)}"`);
    await page.context().close();
  }

  step("ali: an unlimited User's card shows only what he used");
  {
    const page = await signIn("ali");
    await expectCard(page, "unlimited");
    if ((await card(page).locator("progress").count()) !== 0) fail("an unlimited card has a progress bar");
    if (!(await text(card(page).locator("[data-testid=quota-used]"))).includes("۲ GB")) fail("ali's card does not show his 2 GB");
    if ((await page.locator("[data-testid=quota-warning], [data-testid=quota-out]").count()) !== 0) fail("an unlimited User sees a banner");
    await page.context().close();
  }
  console.log("walked the Quota card, the Usage page and the Device page");
} finally {
  await browser.close();
}
