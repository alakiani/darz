// Walks every admin screen against a running lab, as an Admin would. The
// Go lab test (internal/e2e/admin_test.go) sets the lab up, runs this, and
// checks the effects in the Coordinator's store.
//
// Env: PANEL_URL, ADMIN_HANDLE, ADMIN_PASSWORD, TOTP_SECRET.
import { createHmac } from "node:crypto";
import { chromium } from "@playwright/test";

const { PANEL_URL, ADMIN_HANDLE, ADMIN_PASSWORD, TOTP_SECRET } = process.env;
// LIVE=0 skips what needs running Agents: for a quick run without the lab.
const LIVE = process.env.LIVE !== "0";

function totp(secret) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const c of secret.toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, "0");
  const key = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const mac = createHmac("sha1", key).update(counter).digest();
  const off = mac[mac.length - 1] & 15;
  return String((mac.readUInt32BE(off) & 0x7fffffff) % 1_000_000).padStart(6, "0");
}

const step = (name) => console.log(`- ${name}`);
const browser = await chromium.launch();
try {
  const page = await browser.newPage();
  page.setDefaultTimeout(30_000);
  // Typed confirmations answer with the name asked for.
  page.on("dialog", (d) => d.accept(d.message().match(/Type "([^"]+)"/)?.[1]));
  const screen = async (path, heading) => {
    await page.goto(`${PANEL_URL}/#${path}`);
    await page.getByRole("heading", { name: heading, exact: true }).waitFor();
  };

  step("sign in with TOTP");
  await page.goto(`${PANEL_URL}/#/admin`);
  await page.fill("input[name=handle]", ADMIN_HANDLE);
  await page.fill("input[name=password]", ADMIN_PASSWORD);
  await page.click("button[type=submit]");
  await page.fill("input[name=code]", totp(TOTP_SECRET));
  await page.click("button[type=submit]");
  if ((await page.locator("html").getAttribute("dir")) !== "ltr") throw new Error("the admin panel is not left to right");

  step("overview: Node tiles and the Link matrix are live");
  await page.getByRole("heading", { name: "Overview" }).waitFor();
  await page.locator("[data-testid=node-ab1]").waitFor();
  if (LIVE) {
    await page.locator("[data-testid=node-ir1]").getByText(/contact \d+s ago/).waitFor();
    await page.locator("[data-testid=link-matrix] td.up").first().waitFor({ timeout: 90_000 });
  }

  step("users: suspend, resume, cap, recovery Invite, delete");
  await screen("/admin/users", "Users");
  const user = page.locator("[data-testid=user-maryam]");
  await user.getByRole("button", { name: "Suspend" }).click();
  await user.locator(".status", { hasText: "Suspended" }).waitFor();
  await user.getByRole("button", { name: "Resume" }).click();
  await user.locator(".status", { hasText: "Active" }).waitFor();
  await user.getByLabel("Device cap").fill("5");
  await user.getByLabel("Device cap").blur();
  await page.waitForResponse((r) => r.url().endsWith("/v1/admin/users") && r.request().method() === "GET");
  await user.getByRole("button", { name: "Recovery Invite" }).click();
  await page.locator("[data-testid=recovery-link]", { hasText: "/#/invite/" }).waitFor();
  await page.locator("[data-testid=user-reza]").getByRole("button", { name: "Delete" }).click();
  await page.locator("[data-testid=user-reza]").waitFor({ state: "detached" });

  step("invites: create and withdraw");
  await screen("/admin/invites", "Invites");
  await page.fill("input[name=cap]", "2");
  await page.getByRole("button", { name: "Create Invite" }).click();
  await page.locator("[data-testid=invite-url]", { hasText: "/#/invite/" }).waitFor();
  const before = await page.locator("[data-testid=invite-withdrawn]").count();
  await page.locator("[data-testid=invite-open]").first().getByRole("button", { name: "Withdraw" }).click();
  await page.waitForFunction((n) => document.querySelectorAll("[data-testid=invite-withdrawn]").length > n, before);

  step("nodes: add with the enroll command, open a Bridge's Paths, edit, delete");
  await screen("/admin/nodes", "Nodes");
  await page.fill("input[name=name]", "ab9");
  await page.fill("input[name=tags]", "DE");
  await page.fill("input[name=host]", "203.0.113.9");
  await page.getByRole("button", { name: "Add Node" }).click();
  const command = await page.locator("[data-testid=enroll-command]").textContent();
  if (!command.includes("enroll.yml") || !command.includes("enroll_token=")) throw new Error(`bad command: ${command}`);
  if (LIVE) {
    await page.locator("[data-testid=node-row-ir1] a").click();
    await page.locator("[data-testid=paths]", { hasText: "Default" }).waitFor();
    // ab1 is a candidate once its first egress checks have passed.
    await page.locator("[data-testid=paths]", { hasText: "ab1" }).waitFor({ timeout: 90_000 });
    await page.goto(`${PANEL_URL}/#/admin/nodes`);
  }
  await page.locator("[data-testid=node-row-ab9] a").click();
  await page.locator("[data-testid=node-name]", { hasText: "ab9" }).waitFor();
  await page.fill("input[name=tags]", "NL");
  await page.getByRole("button", { name: "Save" }).click();
  await page.waitForResponse((r) => r.url().includes("/v1/admin/nodes/") && r.request().method() === "GET");
  await page.getByRole("button", { name: "Delete Node" }).click();
  await page.getByRole("heading", { name: "Nodes", exact: true }).waitFor();
  await page.locator("[data-testid=node-row-ab9]").waitFor({ state: "detached" });

  step("links: health and the Transport list");
  await screen("/admin/links", "Links");
  if (LIVE) await page.locator("[data-testid=links] .status", { hasText: "up" }).first().waitFor();
  await page.uncheck("input[name=transport-wireguard]");
  await page.waitForResponse((r) => r.url().endsWith("/v1/admin/transports") && r.request().method() === "POST");
  await page.check("input[name=transport-wireguard]");
  await page.waitForResponse((r) => r.url().endsWith("/v1/admin/transports") && r.request().method() === "POST");

  step("rules: change the IR Rule; the default Rule is locked");
  await screen("/admin/rules", "Rules");
  const ir = page.locator("[data-testid=rule-IR]");
  await ir.locator("select[name=fallback]").selectOption("drop");
  await ir.getByRole("button", { name: "Save" }).click();
  await ir.locator(".status", { hasText: "drop" }).waitFor();
  await ir.locator("select[name=fallback]").selectOption("next");
  await ir.getByRole("button", { name: "Save" }).click();
  await ir.locator(".status", { hasText: "next" }).waitFor();
  await page.locator("[data-testid=rule-Default]", { hasText: "locked" }).waitFor();
  if ((await page.locator("[data-testid=rule-Default] input, [data-testid=rule-Default] select").count()) > 0) {
    throw new Error("the default Rule can be edited");
  }

  step("destination sets: fetch now, overrides, roll back");
  await screen("/admin/sets", "Destination Sets");
  const iran = page.locator("[data-testid=set-Iran]");
  // The feed now serves one prefix more than when the lab started.
  const prefixes = (n) => iran.locator("[data-testid=prefixes]", { hasText: new RegExp(`^${n}$`) }).waitFor();
  await iran.getByRole("button", { name: "Fetch now" }).click();
  await prefixes(4);
  await iran.locator("textarea[name=additions]").fill("185.0.0.0/24");
  await iran.getByRole("button", { name: "Save overrides" }).click();
  await prefixes(5);
  // Roll back restores the previous feed update; overrides stay.
  await iran.getByRole("button", { name: "Roll back" }).click();
  await prefixes(4);

  step("API tokens: create, see the secret once, revoke");
  await screen("/admin/tokens", "API tokens");
  await page.fill("input[name=name]", "laptop");
  await page.getByRole("button", { name: "Make a nodes:enroll token" }).click();
  await page.locator("[data-testid=token-secret]", { hasText: "darz_" }).waitFor();
  await page.locator("[data-testid=token-laptop]").getByRole("button", { name: "Revoke" }).click();
  await page.locator("[data-testid=token-laptop] .status", { hasText: "revoked" }).waitFor();

  console.log("walked every admin screen");
} catch (e) {
  console.error(e);
  process.exitCode = 1;
} finally {
  await browser.close();
}
