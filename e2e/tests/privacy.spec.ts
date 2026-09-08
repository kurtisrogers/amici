import { test, expect } from "@playwright/test";
import { resetFixtures, signIn, signOut, writePost, type Fixtures } from "./amici";

/**
 * The promises, checked through a browser.
 *
 * internal/service already proves these rules against the database. This file
 * proves the same things end to end, because a rule that is correct in the
 * service and bypassed by a route is still a broken promise. Between them,
 * these two suites cover the two ways this could go wrong: the rule being
 * wrong, and the rule not being asked.
 */

let fixtures: Fixtures;

test.beforeEach(async ({ request }) => {
  fixtures = await resetFixtures(request);
});

test("a stranger's profile is indistinguishable from a handle nobody registered", async ({
  page,
}) => {
  // Bruno is not Rosa's friend. He must not be able to tell that @rosa is a
  // real account, which means her profile has to answer exactly like a handle
  // that was never taken.
  await signIn(page, "bruno@example.test");

  const stranger = await page.goto("/u/rosa");
  expect(stranger?.status()).toBe(404);
  const strangerBody = await page.locator("main").innerText();

  const nobody = await page.goto("/u/nobodyatall");
  expect(nobody?.status()).toBe(404);
  const nobodyBody = await page.locator("main").innerText();

  expect(strangerBody).toBe(nobodyBody);
  expect(strangerBody).not.toContain("Rosa");
});

test("a friend sees the profile a stranger cannot", async ({ page }) => {
  await signIn(page, "teo@example.test");
  await page.goto("/u/rosa");
  await expect(page.getByRole("heading", { name: "Rosa Marchetti" })).toBeVisible();
});

test("only friends see friends' posts", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await writePost(page, "the greenhouse is open for the season");
  await signOut(page);

  // Teo is a friend.
  await signIn(page, "teo@example.test");
  await expect(page.getByText("the greenhouse is open for the season")).toBeVisible();
  await signOut(page);

  // Bruno is not.
  await signIn(page, "bruno@example.test");
  await expect(page.getByText("the greenhouse is open for the season")).toHaveCount(0);
});

test("an only-me post is invisible to friends", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await writePost(page, "a private note about the tomatoes", "Only me");
  // The author sees it, marked so there is no doubt about who else can.
  await expect(page.getByText("a private note about the tomatoes")).toBeVisible();
  await signOut(page);

  await signIn(page, "teo@example.test");
  await expect(page.getByText("a private note about the tomatoes")).toHaveCount(0);
});

test("a post URL is useless to somebody who is not a friend", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await writePost(page, "a post with a shareable looking url");
  const link = page
    .locator("article.amici-post")
    .filter({ hasText: "a post with a shareable looking url" })
    .locator('a[href^="/posts/"]')
    .first();
  const href = await link.getAttribute("href");
  expect(href).toBeTruthy();
  await signOut(page);

  await signIn(page, "bruno@example.test");
  const response = await page.goto(href!);
  expect(response?.status()).toBe(404);
});

test("signing out makes every page private again", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await signOut(page);

  for (const path of ["/feed", "/friends", "/settings", "/u/rosa"]) {
    await page.goto(path);
    // Sent to sign in rather than shown anything, and told where to come back
    // to afterwards.
    await expect(page, `${path} should require a session`).toHaveURL(/\/signin/);
  }
});

test("support can find an account but cannot read anything in it", async ({ page }) => {
  await signIn(page, "support@example.test");

  await page.goto("/support");
  await page.getByLabel("Email address or handle").fill("rosa@example.test");
  await page.getByRole("button", { name: "Look up" }).click();

  // Metadata for account recovery: counts, status, dates.
  await expect(page.getByRole("heading", { name: /Rosa Marchetti/ })).toBeVisible();
  await expect(page.getByText(/posts/).first()).toBeVisible();

  // And nothing else. Rosa's profile and her posts answer the same way they do
  // for any other non-friend.
  const profile = await page.goto("/u/rosa");
  expect(profile?.status()).toBe(404);

  const canvas = await page.goto("/u/rosa/canvas");
  expect(canvas?.status()).toBe(404);
});

test("a member cannot reach the support or developer consoles", async ({ page }) => {
  await signIn(page, "rosa@example.test");

  for (const path of ["/support", "/developer"]) {
    const response = await page.goto(path);
    // Not 403. A 403 would confirm the console is there to be found.
    expect(response?.status(), `${path} should be indistinguishable from missing`).toBe(404);
  }

  // And the links are not in the header either.
  await page.goto("/feed");
  await expect(page.getByRole("link", { name: "Support" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Developer" })).toHaveCount(0);
});

test("support and developers see their own console and not each other's", async ({ page }) => {
  await signIn(page, "support@example.test");
  await expect(page.getByRole("link", { name: "Support" })).toBeVisible();
  expect((await page.goto("/developer"))?.status()).toBe(404);
  await page.goto("/feed");
  await signOut(page);

  await signIn(page, "dev@example.test");
  await expect(page.getByRole("link", { name: "Developer" })).toBeVisible();
  expect((await page.goto("/support"))?.status()).toBe(404);
});

test("every page tells crawlers to go away", async ({ page }) => {
  const landing = await page.goto("/");
  expect(landing?.headers()["x-robots-tag"]).toContain("noindex");

  await signIn(page, "rosa@example.test");
  const feed = await page.goto("/feed");
  expect(feed?.headers()["x-robots-tag"]).toContain("noindex");
  const profile = await page.goto("/u/rosa");
  expect(profile?.headers()["x-robots-tag"]).toContain("noindex");

  const robots = await page.goto("/robots.txt");
  const text = await robots!.text();
  expect(text).toContain("Disallow: /");
  // The training crawlers are named individually, because "User-agent: *"
  // has not stopped any of them.
  expect(text).toContain("GPTBot");
});

test("there is nothing to search and nowhere to browse people", async ({ page }) => {
  await signIn(page, "rosa@example.test");

  // If any of these ever return something, a stranger has a way to find
  // somebody, and the whole product is a lie.
  for (const path of ["/search", "/discover", "/people", "/suggestions", "/u"]) {
    const response = await page.goto(path);
    expect(response?.status(), `${path} must not exist`).toBe(404);
  }

  await page.goto("/friends");
  await expect(page.getByRole("searchbox")).toHaveCount(0);
});

test("the fixture cast is the one the specs expect", async () => {
  // A guard on the helpers rather than on the product. If somebody edits the
  // fixtures, this fails here with a clear message instead of failing as a
  // confusing "element not visible" in ten other specs.
  expect(fixtures.password).toBe("friends-and-family");
  const handles = fixtures.people.map((p) => p.handle);
  expect(handles).toEqual(
    expect.arrayContaining(["rosa", "teo", "nina", "bruno", "sofia", "quiet", "help-desk", "dev"]),
  );
  expect(fixtures.invite_codes.rosa).toBeTruthy();
  expect(fixtures.invite_codes.expired).toBeTruthy();
});
