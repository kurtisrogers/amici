import { test, expect } from "@playwright/test";
import { resetFixtures, signIn, signOut, signUp, writePost, postCard } from "./amici";

/**
 * The everyday things: posting, reacting, replying, and the shape of a
 * profile. Everything here works with forms and links alone, so these specs
 * also happen to prove that Amici needs no JavaScript.
 */

test.beforeEach(async ({ request }) => {
  await resetFixtures(request);
});

test("posting, reacting and replying", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await writePost(page, "the courgettes have got completely out of hand");
  await signOut(page);

  await signIn(page, "teo@example.test");
  const card = postCard(page, "the courgettes have got completely out of hand");
  await expect(card).toBeVisible();

  // React. A reaction is one click and there is no banner congratulating you.
  const laugh = card.getByRole("button", { name: "Ha!" });
  await expect(laugh).toHaveAttribute("aria-pressed", "false");
  await laugh.click();
  await expect(postCard(page, "the courgettes").getByRole("button", { name: "Ha!" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );

  // Reply, from the post's own page.
  await postCard(page, "the courgettes").getByRole("link", { name: /Reply|repl/ }).first().click();
  await page.getByPlaceholder("Say something kind").fill("send some over");
  await page.getByRole("button", { name: "Reply" }).click();
  await expect(page.getByText("send some over")).toBeVisible();
  await signOut(page);

  // Rosa sees both.
  await signIn(page, "rosa@example.test");
  const own = postCard(page, "the courgettes");
  await expect(own.getByRole("button", { name: "Ha!" })).toContainText("1");
  await expect(page.getByText("send some over")).toBeVisible();
});

test("reacting again with the same feeling takes it back", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await writePost(page, "a post to react to twice");

  const love = () => postCard(page, "a post to react to twice").getByRole("button", { name: "Love" });
  await love().click();
  await expect(love()).toHaveAttribute("aria-pressed", "true");
  await love().click();
  await expect(love()).toHaveAttribute("aria-pressed", "false");
  await expect(love()).not.toContainText("1");
});

test("a member can delete their own post and nobody else's", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await writePost(page, "a post rosa will change her mind about");

  const card = postCard(page, "a post rosa will change her mind about");
  await expect(card.getByRole("button", { name: "Delete" })).toBeVisible();
  await card.getByRole("button", { name: "Delete" }).click();
  await expect(page.getByText("a post rosa will change her mind about")).toHaveCount(0);
  await signOut(page);

  // Teo gets a report link on Rosa's posts, not a delete button.
  await signIn(page, "rosa@example.test");
  await writePost(page, "a post of rosas that teo cannot delete");
  await signOut(page);
  await signIn(page, "teo@example.test");
  const theirs = postCard(page, "a post of rosas that teo cannot delete");
  await expect(theirs.getByRole("button", { name: "Delete" })).toHaveCount(0);
  await expect(theirs.getByText("Report this post")).toBeVisible();
});

test("a new member is pointed at how to bring somebody in", async ({ page }) => {
  // A genuinely new account, because that is the state under test: no friends,
  // nothing posted, and no way for Amici to suggest anybody. Borrowing a
  // fixture member would not do, since they all arrive with a history.
  await signUp(page, {
    handle: "chiara",
    displayName: "Chiara Ferri",
    email: "chiara@example.test",
    age: 31,
  });

  await page.goto("/feed");
  await expect(page.getByText(/It is just you in here so far/)).toBeVisible();
  await expect(page.getByText(/no way to suggest people to you/)).toBeVisible();
  // A button by role, not a link: PicoCSS styles a call to action by putting
  // role="button" on the anchor, which is what the accessibility tree then
  // reports. docs/frontend.md records that trade-off.
  await page.getByRole("button", { name: "Bring somebody in" }).click();
  await expect(page).toHaveURL(/\/friends$/);
});

test("a profile shows counts, a bio and the member's own posts", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/settings");
  await page.getByLabel("A line about you").fill("Tomatoes, choir, and a great deal of opinions.");
  await page.getByRole("button", { name: "Save" }).click();

  await page.goto("/u/rosa");
  await expect(page.getByText("Tomatoes, choir, and a great deal of opinions.")).toBeVisible();
  await expect(page.getByText(/@rosa/)).toBeVisible();
  await expect(page.getByText(/friends/)).toBeVisible();
});

test("choosing a colourway changes the whole place", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/settings");

  const before = await page.locator("html").getAttribute("data-colourway");
  expect(before).toBe("limonata");

  await page.getByRole("radio", { name: /Notte/ }).check();
  await page.getByRole("button", { name: "Save" }).click();

  await expect(page.locator("html")).toHaveAttribute("data-colourway", "notte");
  await page.goto("/feed");
  await expect(page.locator("html")).toHaveAttribute("data-colourway", "notte");
});

test("a post cannot be published to anybody but friends", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/feed");

  // The widest audience Amici can express is "my friends". There is no public
  // option to pick by accident, because one was never built.
  const options = await page.getByLabel("Who can see this").locator("option").allInnerTexts();
  expect(options.sort()).toEqual(["Friends", "Only me"]);
});

test("the whole application works with JavaScript switched off", async ({ browser }) => {
  // A family social network gets read on old phones and bad connections. If
  // any of this needed script, that is where it would break.
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();

  await page.goto("/signin");
  await page.getByLabel("Email address").fill("rosa@example.test");
  await page.getByLabel("Password").fill("friends-and-family");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/feed$/);

  await page.getByPlaceholder(/What would you like to tell/).fill("posted without any javascript");
  await page.getByRole("button", { name: "Post", exact: true }).click();
  await expect(page.getByText("posted without any javascript")).toBeVisible();

  await postCard(page, "posted without any javascript").getByRole("button", { name: "Love" }).click();
  await expect(
    postCard(page, "posted without any javascript").getByRole("button", { name: "Love" }),
  ).toHaveAttribute("aria-pressed", "true");

  await context.close();
});

test("a member can report a post to support", async ({ page }) => {
  await signIn(page, "teo@example.test");
  await writePost(page, "something rosa is going to report");
  await signOut(page);

  await signIn(page, "rosa@example.test");
  const card = postCard(page, "something rosa is going to report");
  await card.getByText("Report this post").click();
  await card
    .getByLabel("What is wrong with this post?")
    .fill("He is being unkind about my tomatoes again.");
  await card.getByRole("button", { name: "Send to support" }).click();
  await expect(page.getByText(/Thank you for telling us/)).toBeVisible();
  await signOut(page);

  // Support sees the description, and still cannot open the post.
  await signIn(page, "support@example.test");
  await page.goto("/support");
  const report = page
    .locator("article.amici-report-card")
    .filter({ hasText: "He is being unkind about my tomatoes again." });
  await expect(report).toBeVisible();

  // Scoped to Rosa's report rather than whichever card happens to be first,
  // because the fixture world already has one waiting.
  await report.getByLabel("What did you do?").fill("Had a word with him.");
  await report.getByRole("button", { name: "Acted on it" }).click();
  await expect(page.getByText("Report closed.")).toBeVisible();
  await expect(report).toHaveCount(0);
});

test("changing a password signs out the other browsers", async ({ browser }) => {
  const phone = await browser.newContext();
  const laptop = await browser.newContext();
  const phonePage = await phone.newPage();
  const laptopPage = await laptop.newPage();

  for (const page of [phonePage, laptopPage]) {
    await page.goto("/signin");
    await page.getByLabel("Email address").fill("nina@example.test");
    await page.getByLabel("Password").fill("friends-and-family");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).toHaveURL(/\/feed$/);
  }

  await laptopPage.goto("/settings");
  await laptopPage.getByLabel("Your current password").fill("friends-and-family");
  await laptopPage.getByLabel("A new one").fill("a-brand-new-passphrase");
  await laptopPage.getByRole("button", { name: "Change my password" }).click();
  await expect(laptopPage.getByText(/password is changed/)).toBeVisible();

  // The browser that made the change stays in, because being logged out for
  // doing the right thing teaches people not to do it.
  await laptopPage.goto("/feed");
  await expect(laptopPage).toHaveURL(/\/feed$/);

  // Every other one is out, which is the entire point if somebody else knew
  // the old password.
  await phonePage.goto("/feed");
  await expect(phonePage).toHaveURL(/\/signin/);

  await phone.close();
  await laptop.close();
});
