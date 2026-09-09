import { test, expect, type Page } from "@playwright/test";
import { createHmac } from "node:crypto";
import { FIXTURE_PASSWORD, resetFixtures, signIn, signOut } from "./amici";

/**
 * The second factor, and closing your own account.
 *
 * Both are reached from settings, both ask for the password on the way, and
 * both are the kind of thing nobody finds out is broken until they need it. So
 * they get driven end to end: enrol, sign out, sign back in with a code the
 * test computes the way a phone would.
 */

test.beforeEach(async ({ request }) => {
  await resetFixtures(request);
});

/**
 * Compute a TOTP code, which is what the member's authenticator app is doing.
 *
 * This is RFC 6238 in about fifteen lines, and it is deliberately a second
 * implementation rather than a call into the Go one. A test that shared an
 * implementation with the code under test would pass just as happily if both
 * were wrong.
 */
function totp(secret: string, at = new Date()): string {
  const key = base32Decode(secret.replace(/\s+/g, "").toUpperCase());
  const counter = Math.floor(at.getTime() / 1000 / 30);

  const buf = Buffer.alloc(8);
  buf.writeUInt32BE(Math.floor(counter / 2 ** 32), 0);
  buf.writeUInt32BE(counter >>> 0, 4);

  const digest = createHmac("sha1", key).update(buf).digest();
  const offset = digest[digest.length - 1] & 0x0f;
  const value = digest.readUInt32BE(offset) & 0x7fffffff;
  return String(value % 1_000_000).padStart(6, "0");
}

function base32Decode(input: string): Buffer {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const char of input) {
    const index = alphabet.indexOf(char);
    expect(index, `${char} is not base32`).toBeGreaterThanOrEqual(0);
    value = (value << 5) | index;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(out);
}

/** Open a collapsed section of the settings page. */
async function reveal(page: Page, summary: string | RegExp): Promise<void> {
  await page.locator("summary").filter({ hasText: summary }).click();
}

/** The secret currently on the enrolment page, as a member would read it. */
async function enrolmentSecret(page: Page): Promise<string> {
  const secret = (await page.locator(".amici-code").first().innerText()).trim();
  expect(secret, "the enrolment page showed no secret").not.toBe("");
  return secret;
}

/** Enrol whoever is signed in, and return their secret and recovery codes. */
async function enrolSecondFactor(page: Page) {
  await page.goto("/settings/two-factor");
  const secret = await enrolmentSecret(page);

  await page.getByLabel("Six digit code").fill(totp(secret));
  await page.getByRole("button", { name: "Turn on the second step" }).click();

  await expect(page.getByRole("heading", { name: "Your recovery codes" })).toBeVisible();
  const codes = await page.locator(".amici-codes .amici-code").allInnerTexts();
  expect(codes).toHaveLength(10);

  await page.getByRole("link", { name: "I have written them down" }).click();
  await expect(page).toHaveURL(/\/settings$/);
  return { secret, codes: codes.map((code) => code.trim()) };
}

test("a second factor is not switched on until a code proves the app works", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/settings/two-factor");
  await enrolmentSecret(page);

  // A wrong code changes nothing. If it did, somebody who opened this page and
  // wandered off would have locked themselves out with a secret they never
  // wrote down.
  await page.getByLabel("Six digit code").fill("000000");
  await page.getByRole("button", { name: "Turn on the second step" }).click();
  await expect(page.locator(".amici-flash--bad")).toBeVisible();

  // The refusal lands back on the enrolment page, so one typo does not send
  // her back to settings to start again.
  await expect(page).toHaveURL(/\/settings\/two-factor$/);
  const secret = await enrolmentSecret(page);
  await page.getByLabel("Six digit code").fill(totp(secret));
  await page.getByRole("button", { name: "Turn on the second step" }).click();
  await expect(page.getByRole("heading", { name: "Your recovery codes" })).toBeVisible();
});

test("signing in with a second factor asks for a code, and the code lets you in", async ({
  page,
}) => {
  await signIn(page, "rosa@example.test");
  const { secret } = await enrolSecondFactor(page);
  await expect(page.getByText(/Signing in needs a code/)).toBeVisible();
  await signOut(page);

  // The password alone now stops at the code page rather than the feed.
  await page.goto("/signin");
  await page.getByLabel("Email address").fill("rosa@example.test");
  await page.getByLabel("Password").fill(FIXTURE_PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/signin\/code$/);
  await expect(page.getByRole("heading", { name: "One more step" })).toBeVisible();
  await expect(page.getByText("Rosa Marchetti")).toBeVisible();

  // A wrong code keeps her on the page rather than making her type her
  // password again for a mistyped digit.
  await page.getByLabel("Six digit code").fill("000000");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/signin\/code$/);
  await expect(page.locator(".amici-flash--bad")).toBeVisible();

  await page.getByLabel("Six digit code").fill(totp(secret));
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/feed$/);
});

test("a recovery code gets you in without the phone, once", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  const { codes } = await enrolSecondFactor(page);
  await signOut(page);

  const attempt = async (code: string) => {
    await page.goto("/signin");
    await page.getByLabel("Email address").fill("rosa@example.test");
    await page.getByLabel("Password").fill(FIXTURE_PASSWORD);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).toHaveURL(/\/signin\/code$/);
    await page.getByLabel("Six digit code").fill(code);
    await page.getByRole("button", { name: "Sign in" }).click();
  };

  await attempt(codes[0]);
  await expect(page).toHaveURL(/\/feed$/);
  await page.goto("/settings");
  await expect(page.getByText("9 recovery codes left")).toBeVisible();
  await signOut(page);

  // The same code a second time is refused, in the same words a wrong code
  // gets: which of the two somebody got wrong is not worth telling them.
  await attempt(codes[0]);
  await expect(page).toHaveURL(/\/signin\/code$/);
  await expect(page.locator(".amici-flash--bad")).toBeVisible();
});

test("turning the second factor off needs the password, and takes the codes with it", async ({
  page,
}) => {
  await signIn(page, "rosa@example.test");
  await enrolSecondFactor(page);

  const disable = page.locator('form[action="/settings/two-factor/disable"]');
  await reveal(page, "Turn the second step off");
  await disable.getByLabel("Your password").fill("not-her-password");
  await disable.getByRole("button", { name: "Turn it off" }).click();
  await expect(page.locator(".amici-flash--bad")).toBeVisible();
  await expect(page.getByText(/Signing in needs a code/)).toBeVisible();

  await reveal(page, "Turn the second step off");
  await disable.getByLabel("Your password").fill(FIXTURE_PASSWORD);
  await disable.getByRole("button", { name: "Turn it off" }).click();
  await expect(page.getByText(/no longer work/)).toBeVisible();

  // And signing in is back to one step.
  await signOut(page);
  await signIn(page, "rosa@example.test");
});

test("closing an account says what it costs, takes effect at once, and can be undone", async ({
  page,
}) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/settings");
  await page.getByRole("link", { name: "Close my account" }).click();

  // Nobody should discover afterwards what they agreed to, so the page counts
  // it out: this many posts, this many friends, this many days to change your
  // mind.
  await expect(page.getByRole("heading", { name: /Close your Amici account/ })).toBeVisible();
  await expect(page.getByText(/your friends' feeds/)).toBeVisible();
  await expect(page.getByRole("heading", { name: /What happens after 30 days/ })).toBeVisible();
  await expect(page.getByText(/Sign in at any point in the next 30 days/)).toBeVisible();

  await page.getByLabel("Your password").fill("not-her-password");
  await page.getByRole("button", { name: "Close my account" }).click();
  await expect(page.locator(".amici-flash--bad")).toBeVisible();

  await page.getByLabel("Your password").fill(FIXTURE_PASSWORD);
  await page.getByRole("button", { name: "Close my account" }).click();
  await expect(page).toHaveURL("/");
  await expect(page.getByText(/Your account is closed/)).toBeVisible();

  // Signed out everywhere, immediately.
  const feed = await page.goto("/feed");
  expect(feed?.url()).toMatch(/\/signin$/);

  // And her posts have left her friend's feed while the account is closed.
  await signIn(page, "teo@example.test");
  await expect(
    page.locator("article.amici-post").filter({ hasText: "Rosa Marchetti" }),
  ).toHaveCount(0);
  await signOut(page);

  // Signing in inside the window brings it all back, with no form to fill in
  // and nobody to ask.
  await signIn(page, "rosa@example.test");
  await expect(page.getByText(/Your account is open again/)).toBeVisible();
  await expect(page.getByText(/Nothing was deleted/)).toBeVisible();
});

test("support and developer accounts cannot be closed from settings", async ({ page }) => {
  for (const email of ["support@example.test", "dev@example.test"]) {
    await signIn(page, email);
    await page.goto("/settings/close");
    await page.getByLabel("Your password").fill(FIXTURE_PASSWORD);
    await page.getByRole("button", { name: "Close my account" }).click();
    await expect(page.locator(".amici-flash--bad")).toBeVisible();
    await signOut(page);
  }
});
