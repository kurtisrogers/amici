import { test, expect } from "@playwright/test";
import { resetFixtures, signIn, signOut, type Fixtures } from "./amici";

/**
 * The two routes in, and nothing else.
 */

let fixtures: Fixtures;

test.beforeEach(async ({ request }) => {
  fixtures = await resetFixtures(request);
});

test("a request by email address arrives, and accepting it opens the feed both ways", async ({
  page,
}) => {
  // Bruno and Rosa are strangers at the start.
  await signIn(page, "bruno@example.test");
  await page.goto("/friends");
  await page.getByLabel("Their email address").fill("rosa@example.test");
  await page.getByLabel("A line so they know it is you").fill("Bruno from the allotment");
  await page.getByRole("button", { name: "Send the request" }).click();

  // The confirmation says what we did, and refuses to say what we found.
  await expect(page.getByText(/If that address belongs to somebody on Amici/)).toBeVisible();
  await expect(page.getByText(/We will not tell you either way/)).toBeVisible();
  await signOut(page);

  await signIn(page, "rosa@example.test");
  await page.goto("/friends");
  await expect(page.getByText("Bruno from the allotment")).toBeVisible();
  await page.getByRole("button", { name: "Accept" }).first().click();
  await expect(page.getByText(/You are friends/)).toBeVisible();

  // Rosa can now open Bruno's page, where a moment ago she got a 404.
  const profile = await page.goto("/u/bruno");
  expect(profile?.status()).toBe(200);
});

test("the reply to a request by email is identical whoever it was sent to", async ({ page }) => {
  await signIn(page, "bruno@example.test");
  await page.goto("/friends");

  const send = async (email: string) => {
    await page.getByLabel("Their email address").fill(email);
    await page.getByRole("button", { name: "Send the request" }).click();
    await expect(page).toHaveURL(/\/friends$/);
    return page.locator(".amici-flash").innerText();
  };

  // A real member, an address with no account at all, somebody who has turned
  // email requests off, and a fifteen year old who cannot be reached this way
  // whatever her settings say. All four have to look the same, or this form
  // becomes a way to check whether a particular person is on Amici.
  const real = await send("nina@example.test");
  const missing = await send("no-such-person@example.test");
  const optedOut = await send("marco@example.test");
  const child = await send("sofia@example.test");

  expect(missing).toBe(real);
  expect(optedOut).toBe(real);
  expect(child).toBe(real);

  // And the two who should not have received anything did not.
  await signOut(page);
  await signIn(page, "sofia@example.test");
  await page.goto("/friends");
  await expect(page.getByText("Waiting for you")).toHaveCount(0);
});

test("a request code works once and then is spent", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/friends");
  await page.getByLabel("Who is it for? (only you see this)").fill("Bruno");
  await page.getByRole("button", { name: "Make a code" }).click();

  const code = (await page.locator("code.amici-code").first().innerText()).trim();
  expect(code).toBeTruthy();
  // Shown once, and said so plainly, because only a hash of it is stored.
  await expect(page.getByText(/Copy it now/)).toBeVisible();
  await signOut(page);

  await signIn(page, "bruno@example.test");
  await page.goto("/friends/redeem");
  await page.getByLabel("The code").fill(code);
  await page.getByLabel("A line so they know it is you").fill("Bruno here");
  await page.getByRole("button", { name: "Send my request" }).click();
  await expect(page.getByText(/Your request is with Rosa/)).toBeVisible();
  await signOut(page);

  // Nina cannot reuse the same code.
  await signIn(page, "nina@example.test");
  await page.goto("/friends/redeem");
  await page.getByLabel("The code").fill(code);
  await page.getByRole("button", { name: "Send my request" }).click();
  await expect(page.locator(".amici-flash--bad")).toBeVisible();
});

test("an expired code is refused", async ({ page }) => {
  // The fixtures mint one code that is already past its twenty four hours,
  // which is the only way to test expiry without waiting a day.
  const expired = fixtures.invite_codes.expired;
  expect(expired).toBeTruthy();

  await signIn(page, "bruno@example.test");
  await page.goto("/friends/redeem");
  await page.getByLabel("The code").fill(expired);
  await page.getByRole("button", { name: "Send my request" }).click();
  await expect(page.locator(".amici-flash--bad, .amici-flash--warn")).toBeVisible();

  await signOut(page);
  await signIn(page, "rosa@example.test");
  await page.goto("/friends");
  await expect(page.getByText("Bruno")).toHaveCount(0);
});

test("a code creates a request rather than a friendship", async ({ page }) => {
  const code = fixtures.invite_codes.rosa;

  await signIn(page, "bruno@example.test");
  await page.goto("/friends/redeem");
  await page.getByLabel("The code").fill(code);
  await page.getByRole("button", { name: "Send my request" }).click();

  // Redeeming does not let Bruno in. A code can be forwarded, so the owner
  // still gets the last word about who ends up in their life.
  const profile = await page.goto("/u/rosa");
  expect(profile?.status()).toBe(404);
});

test("revoking a code stops it working", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/friends");
  await page.getByLabel("Who is it for? (only you see this)").fill("Someone");
  await page.getByRole("button", { name: "Make a code" }).click();
  const code = (await page.locator("code.amici-code").first().innerText()).trim();

  await page.goto("/friends");
  await page
    .locator("li.amici-invite")
    .filter({ hasText: "Someone" })
    .getByRole("button", { name: "Revoke" })
    .click();
  await expect(page.getByText(/will not work any more/)).toBeVisible();
  await signOut(page);

  await signIn(page, "bruno@example.test");
  await page.goto("/friends/redeem");
  await page.getByLabel("The code").fill(code);
  await page.getByRole("button", { name: "Send my request" }).click();
  await expect(page.locator(".amici-flash--bad")).toBeVisible();
});

test("declining a request tells the sender nothing", async ({ page }) => {
  await signIn(page, "bruno@example.test");
  await page.goto("/friends");
  await page.getByLabel("Their email address").fill("rosa@example.test");
  await page.getByRole("button", { name: "Send the request" }).click();
  await signOut(page);

  await signIn(page, "rosa@example.test");
  await page.goto("/friends");
  await page.getByRole("button", { name: "No thanks" }).first().click();
  await expect(page.getByText(/Turned down/)).toBeVisible();
  await signOut(page);

  // Bruno's screen simply no longer shows it waiting. There is no "declined"
  // notice, because being told you were turned down is worse for everybody
  // than never hearing back.
  await signIn(page, "bruno@example.test");
  await page.goto("/friends");
  await expect(page.getByText(/declined/i)).toHaveCount(0);
  // Rosa is simply not in the list any more. Scoped to her rather than to the
  // whole section, because Bruno has another request out to Nina and the
  // section is legitimately still there.
  await expect(page.locator("li.amici-invite").filter({ hasText: "Rosa" })).toHaveCount(0);
  const profile = await page.goto("/u/rosa");
  expect(profile?.status()).toBe(404);
});

test("unfriending closes the door in both directions", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/friends");
  await page
    .locator("li.amici-person")
    .filter({ hasText: "Teo Marchetti" })
    .getByRole("button", { name: "Unfriend" })
    .click();
  await expect(page.getByText(/no longer friends/)).toBeVisible();

  expect((await page.goto("/u/teo"))?.status()).toBe(404);
  await signOut(page);

  await signIn(page, "teo@example.test");
  expect((await page.goto("/u/rosa"))?.status()).toBe(404);
});

test("blocking ends the friendship and closes both routes back", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/friends");
  await page
    .locator("li.amici-person")
    .filter({ hasText: "Teo Marchetti" })
    .getByRole("button", { name: "Block" })
    .click();
  await expect(page.getByText("Blocked.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Blocked" })).toBeVisible();
  await signOut(page);

  // Neither route works for Teo any more.
  await signIn(page, "teo@example.test");
  await page.goto("/friends");
  await page.getByLabel("Their email address").fill("rosa@example.test");
  await page.getByRole("button", { name: "Send the request" }).click();
  await signOut(page);

  await signIn(page, "rosa@example.test");
  await page.goto("/friends");
  await expect(page.getByText("Waiting for you")).toHaveCount(0);
});

test("a young member's codes expire sooner and are said to", async ({ page }) => {
  await signIn(page, "sofia@example.test");
  await page.goto("/friends");
  // Four hours rather than twenty four, and the interface says so rather than
  // quietly applying a different rule.
  await expect(page.getByText(/Codes last 4 hours/)).toBeVisible();

  await page.goto("/settings");
  await expect(page.getByText(/You are under eighteen/)).toBeVisible();
  // There is no control to switch it on, not even a disabled one.
  await expect(page.getByLabel(/Let somebody who already knows my email/)).toHaveCount(0);
});
