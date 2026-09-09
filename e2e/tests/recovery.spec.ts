import { test, expect } from "@playwright/test";
import {
  FIXTURE_PASSWORD,
  followLinkFor,
  resetFixtures,
  signIn,
  signOut,
  waitForMessage,
  type Fixtures,
} from "./amici";

/**
 * The paths somebody walks when they cannot simply sign in: confirming an
 * address, forgetting a password, and moving to a new address.
 *
 * These specs read the development outbox rather than a mail server, but they
 * read it the way a person reads their inbox: find the message, follow the
 * link in it. Nothing here reaches past the interface to mint a token.
 */

let fixtures: Fixtures;

test.beforeEach(async ({ request }) => {
  fixtures = await resetFixtures(request);
});

test("a new member is asked to confirm their address, and is unreachable until they do", async ({
  page,
  request,
}) => {
  // Pia registered and never followed the link, which is the state most
  // people are in for the first few minutes of their account.
  await signIn(page, "pia@example.test");

  // The banner follows her around rather than sitting on one page she might
  // never visit, and it says exactly what the unconfirmed address costs.
  await expect(page.getByText(/nobody can reach you by typing it in/)).toBeVisible();
  await page.goto("/settings");
  await expect(page.getByText(/not confirmed yet/).first()).toBeVisible();

  // And meanwhile a friend who knows the address cannot reach her. The reply
  // gives nothing away, which is the point: "not confirmed" is not something
  // a sender is told either.
  await signOut(page);
  await signIn(page, "bruno@example.test");
  await page.goto("/friends");
  await page.getByLabel("Their email address").fill("pia@example.test");
  await page.getByLabel("A line so they know it is you").fill("Bruno here");
  await page.getByRole("button", { name: "Send the request" }).click();
  await expect(page.getByText(/If that address belongs to somebody on Amici/)).toBeVisible();
  await signOut(page);

  await signIn(page, "pia@example.test");
  await page.goto("/friends");
  await expect(page.getByText("Bruno here")).toHaveCount(0);

  // She asks for another link and follows it.
  await page.goto("/settings");
  await page.getByRole("button", { name: "Send me another link" }).click();
  await followLinkFor(page, request, "pia@example.test");
  await page.getByRole("button", { name: "Yes, confirm this address" }).click();
  await expect(page.getByText(/Your address is confirmed/)).toBeVisible();
  await expect(page.getByText(/nobody can reach you by typing it in/)).toHaveCount(0);

  // Now the same request arrives.
  await signOut(page);
  await signIn(page, "bruno@example.test");
  await page.goto("/friends");
  await page.getByLabel("Their email address").fill("pia@example.test");
  await page.getByLabel("A line so they know it is you").fill("Bruno, second try");
  await page.getByRole("button", { name: "Send the request" }).click();
  await signOut(page);

  await signIn(page, "pia@example.test");
  await page.goto("/friends");
  await expect(page.getByText("Bruno, second try")).toBeVisible();
});

test("registering sends a confirmation link, and following it does not sign you in", async ({
  page,
  request,
}) => {
  const born = new Date();
  born.setFullYear(born.getFullYear() - 30);

  await page.goto("/signup");
  await page.getByLabel("Your name").fill("Lucia Bruni");
  await page.getByLabel("Handle").fill("lucia");
  await page.getByLabel("Email address").fill("lucia@example.test");
  await page.getByLabel("Date of birth").fill(born.toISOString().slice(0, 10));
  await page.getByLabel("Password").fill(FIXTURE_PASSWORD);
  await page.getByRole("button", { name: "Create my account" }).click();
  await expect(page).toHaveURL(/\/friends$/);

  const message = await waitForMessage(request, "lucia@example.test");
  expect(message.subject).toContain("Confirm your email address");
  // No HTML part, no remote image, nothing that reports back that it was
  // opened.
  expect(message.body).not.toContain("<img");
  expect(message.body).not.toContain("<html");

  // Following the link from a browser with no session must not open one.
  // A confirmation link that signed you in would mean anybody who read the
  // email once could get into the account.
  await signOut(page);
  await followLinkFor(page, request, "lucia@example.test");
  await page.getByRole("button", { name: "Yes, confirm this address" }).click();
  await expect(page).toHaveURL(/\/signin$/);
  await expect(page.getByText(/That address is confirmed/)).toBeVisible();
});

test("a forgotten password can be set again from a link, and nothing is revealed on the way", async ({
  page,
  request,
}) => {
  const ask = async (email: string) => {
    await page.goto("/forgot-password");
    await page.getByLabel("Email address").fill(email);
    await page.getByRole("button", { name: "Send me a link" }).click();
    await expect(page.getByRole("heading", { name: "Check your email" })).toBeVisible();
    return page.locator(".amici-card").innerText();
  };

  // The same answer for an address that is here and one that is not. This is
  // the same rule as the friend request form: a page that answered would be a
  // way of finding out who is on Amici.
  const known = await ask("rosa@example.test");
  const unknown = await ask("nobody-at-all@example.test");
  expect(known).toBe(unknown);

  const nothing = await request.get("/fixtures/outbox");
  const messages = (await nothing.json()) as Array<{ to: string }>;
  expect(messages.filter((m) => m.to === "nobody-at-all@example.test")).toHaveLength(0);

  await followLinkFor(page, request, "rosa@example.test");
  await page.getByLabel("New password").fill("a-brand-new-secret-phrase");
  await page.getByRole("button", { name: "Set my password" }).click();
  await expect(page).toHaveURL(/\/feed$/);
  await expect(page.getByText(/you are signed in/)).toBeVisible();

  // The old password is gone.
  await signOut(page);
  await page.goto("/signin");
  await page.getByLabel("Email address").fill("rosa@example.test");
  await page.getByLabel("Password").fill(FIXTURE_PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/signin$/);
  await expect(page.getByText(/do not go together/)).toBeVisible();
});

test("a reset link stops working once it has been used", async ({ page, request }) => {
  await page.goto("/forgot-password");
  await page.getByLabel("Email address").fill("rosa@example.test");
  await page.getByRole("button", { name: "Send me a link" }).click();

  const message = await waitForMessage(request, "rosa@example.test");
  const link = resetLinkIn(message.body);

  await page.goto(link);
  await page.getByLabel("New password").fill("the-first-new-phrase");
  await page.getByRole("button", { name: "Set my password" }).click();
  await expect(page).toHaveURL(/\/feed$/);

  // Somebody who kept a copy of the email cannot use it a second time.
  await signOut(page);
  await page.goto(link);
  await expect(page.getByText(/not valid|already been used/)).toBeVisible();
});

test("a password we refuse leaves the reset link usable", async ({ page, request }) => {
  await page.goto("/forgot-password");
  await page.getByLabel("Email address").fill("rosa@example.test");
  await page.getByRole("button", { name: "Send me a link" }).click();

  await followLinkFor(page, request, "rosa@example.test");
  // A breached password, which the embedded corpus catches. The refusal has
  // to cost another attempt rather than the only link she has.
  await page.getByLabel("New password").fill("password1234");
  await page.getByRole("button", { name: "Set my password" }).click();
  await expect(page.locator(".amici-flash--bad")).toBeVisible();

  await page.getByLabel("New password").fill("a-phrase-nobody-has-used");
  await page.getByRole("button", { name: "Set my password" }).click();
  await expect(page).toHaveURL(/\/feed$/);
});

test("moving to a new address keeps the old one working until the new one answers", async ({
  page,
  request,
}) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/settings");
  await page.locator("summary").filter({ hasText: "Move to a different address" }).click();

  const move = page.locator('form[action="/settings/email"]');
  await move.getByLabel("New email address").fill("rosa@elsewhere.test");
  await move.getByLabel("Your password").fill(FIXTURE_PASSWORD);
  await move.getByRole("button", { name: "Send a confirmation link" }).click();

  await expect(page.getByText(/Waiting for/)).toBeVisible();
  await expect(page.getByText("rosa@elsewhere.test")).toBeVisible();

  // A typo should cost a wasted email, not an account nobody can get into,
  // so the old address still signs in while the change is pending.
  await signOut(page);
  await signIn(page, "rosa@example.test");

  await followLinkFor(page, request, "rosa@elsewhere.test");
  await page.getByRole("button", { name: "Yes, confirm this address" }).click();
  await page.goto("/settings");
  await expect(page.getByText("rosa@elsewhere.test, confirmed")).toBeVisible();

  await signOut(page);
  await signIn(page, "rosa@elsewhere.test");
});

test("a pending address change can be called off", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/settings");
  await page.locator("summary").filter({ hasText: "Move to a different address" }).click();

  const move = page.locator('form[action="/settings/email"]');
  await move.getByLabel("New email address").fill("typo@elsewhere.test");
  await move.getByLabel("Your password").fill(FIXTURE_PASSWORD);
  await move.getByRole("button", { name: "Send a confirmation link" }).click();
  await expect(page.getByText(/Waiting for/)).toBeVisible();

  await page.getByRole("button", { name: /keep my old address/ }).click();
  await expect(page.getByText(/Waiting for/)).toHaveCount(0);
  await expect(page.getByText(/rosa@example\.test, confirmed/)).toBeVisible();
});

test("the mail Amici sends carries no tracking and promises no more", async ({ page, request }) => {
  await page.goto("/forgot-password");
  await page.getByLabel("Email address").fill("rosa@example.test");
  await page.getByRole("button", { name: "Send me a link" }).click();

  const message = await waitForMessage(request, "rosa@example.test");
  // The promise is in the footer of every message, so it is worth checking it
  // is actually there rather than only in the documentation.
  expect(message.body).toContain("This is the only kind of message we send");
  expect(message.body).not.toContain("unsubscribe");
  expect(message.body).not.toContain("utm_");
  // No mention of who anybody's friends are or what they posted. An inbox is
  // read on shared computers and scanned by whoever runs it.
  for (const handle of fixtures.people.map((p) => p.display_name)) {
    if (handle === "Rosa Marchetti") continue;
    expect(message.body).not.toContain(handle);
  }
});

/** Pull the reset link out of a message body. */
function resetLinkIn(body: string): string {
  const match = body.match(/https?:\/\/\S*[?&]token=\S+/);
  expect(match, `no link in the message:\n${body}`).not.toBeNull();
  return match![0];
}
