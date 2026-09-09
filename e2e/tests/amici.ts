import { expect, type APIRequestContext, type Page } from "@playwright/test";

/**
 * Helpers shared by the specs.
 *
 * The rule these follow: drive the application the way a person would. Sign
 * in by filling the form, not by minting a session cookie. Accept a friend
 * request by clicking Accept, not by writing a row. A test that takes a
 * shortcut past the interface stops being able to tell you the interface
 * works.
 *
 * The single exception is resetFixtures, which exists so that a spec about
 * reactions does not have to spend twenty seconds arranging a friendship
 * first. It rebuilds exactly the cast that `go run ./cmd/amiciseed` produces,
 * so what CI asserts and what a developer sees locally cannot drift apart.
 */

/** The password every fixture account shares. Mirrors fixtures.Password. */
export const FIXTURE_PASSWORD = "friends-and-family";

/** The fixture cast, as returned by the reset endpoint. */
export type Fixtures = {
  ok: boolean;
  accounts: Record<string, string>;
  password: string;
  invite_codes: Record<string, string>;
  posts: number;
  people: Array<{
    handle: string;
    display_name: string;
    email: string;
    role: string;
    reachable_by_email: boolean;
    email_confirmed: boolean;
    note: string;
  }>;
};

/** One message the development sender recorded instead of delivering. */
export type OutboxMessage = {
  to: string;
  subject: string;
  body: string;
  at: string;
};

/**
 * Read a CSRF token out of a rendered page.
 *
 * Amici protects every state-changing request with a double-submit token and
 * an origin check, and the fixture endpoint is not exempt. Nothing here is
 * allowed to switch that off for the sake of the tests: an endpoint that only
 * behaves in tests is not the endpoint being tested. So the helpers do what a
 * browser does, which is load a page and submit the token it was given.
 */
async function csrfToken(request: APIRequestContext): Promise<string> {
  const page = await request.get("/signin");
  expect(page.ok(), `could not load the sign-in page: ${page.status()}`).toBeTruthy();
  const html = await page.text();
  const match = html.match(/name="csrf_token" value="([^"]+)"/);
  expect(match, "no CSRF token on the sign-in page").not.toBeNull();
  return match![1];
}

/**
 * Rebuild the fixture world. Every spec calls this first so that specs cannot
 * leak state into each other.
 */
export async function resetFixtures(request: APIRequestContext): Promise<Fixtures> {
  const token = await csrfToken(request);
  const response = await request.post("/fixtures/reset", {
    form: { csrf_token: token },
  });
  expect(
    response.ok(),
    `fixture reset failed with ${response.status()}: ${await response.text()}`,
  ).toBeTruthy();
  const body = (await response.json()) as Fixtures;
  expect(body.ok).toBeTruthy();
  return body;
}

/**
 * Wait for a message to arrive for an address, and return the newest one.
 *
 * There is no mail server in front of a test, so the development sender
 * records instead of delivering and this reads it back. Deliberately no way to
 * ask the application for a token directly: a spec that minted its own link
 * would stop being able to tell you that the link in the email works.
 */
export async function waitForMessage(
  request: APIRequestContext,
  to: string,
): Promise<OutboxMessage> {
  let seen: OutboxMessage[] = [];
  for (let attempt = 0; attempt < 20; attempt++) {
    const response = await request.get("/fixtures/outbox");
    expect(response.ok(), `could not read the outbox: ${response.status()}`).toBeTruthy();
    seen = (await response.json()) as OutboxMessage[];
    const mine = seen.filter((m) => m.to.toLowerCase() === to.toLowerCase());
    if (mine.length > 0) {
      return mine[mine.length - 1];
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(
    `nothing arrived for ${to}. The outbox holds: ${seen.map((m) => m.to).join(", ") || "nothing"}`,
  );
}

/**
 * Follow the link in the newest message sent to an address, which is what a
 * person does when they open their inbox.
 */
export async function followLinkFor(
  page: Page,
  request: APIRequestContext,
  to: string,
): Promise<void> {
  const message = await waitForMessage(request, to);
  const match = message.body.match(/https?:\/\/\S*[?&]token=\S+/);
  expect(match, `no link in the message to ${to}:\n${message.body}`).not.toBeNull();
  await page.goto(match![0]);
}

/** Sign in through the form, and wait to land on the feed. */
export async function signIn(page: Page, email: string): Promise<void> {
  await page.goto("/signin");
  await page.getByLabel("Email address").fill(email);
  await page.getByLabel("Password").fill(FIXTURE_PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/feed$/);
}

/**
 * Join through the sign-up form, and land signed in.
 *
 * Joining puts a new member on the friends page rather than the feed, because
 * a feed with nobody in it is not a useful first screen and the only thing
 * worth doing on day one is reaching somebody you know.
 *
 * Takes an age rather than a date of birth, because every rule that cares
 * about a birth date is really a rule about how old somebody is, and a spec
 * that says 15 reads better than one that says a date which stops meaning 15
 * next year.
 */
export async function signUp(
  page: Page,
  person: { handle: string; displayName: string; email: string; age: number },
): Promise<void> {
  const born = new Date();
  born.setFullYear(born.getFullYear() - person.age);
  born.setDate(born.getDate() - 1);

  await page.goto("/signup");
  await page.getByLabel("Your name").fill(person.displayName);
  await page.getByLabel("Handle").fill(person.handle);
  await page.getByLabel("Email address").fill(person.email);
  await page.getByLabel("Date of birth").fill(born.toISOString().slice(0, 10));
  await page.getByLabel("Password").fill(FIXTURE_PASSWORD);
  await page.getByRole("button", { name: "Create my account" }).click();
  await expect(page).toHaveURL(/\/friends$/);
}

/**
 * Sign out through the header, wherever the browser currently is.
 *
 * Exact, because the settings page also offers "Sign out everywhere", and that
 * one closes every session on the account rather than this one.
 */
export async function signOut(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page).toHaveURL("/");
}

/** Write a post as whoever is currently signed in. */
export async function writePost(
  page: Page,
  body: string,
  audience: "Friends" | "Only me" = "Friends",
): Promise<void> {
  await page.goto("/feed");
  await page.getByPlaceholder(/What would you like to tell/).fill(body);
  await page.getByLabel("Who can see this").selectOption({ label: audience });
  await page.getByRole("button", { name: "Post", exact: true }).click();
  await expect(page.getByText(body)).toBeVisible();
}

/** The post article containing a given body, for scoping reaction clicks. */
export function postCard(page: Page, body: string) {
  return page.locator("article.amici-post").filter({ hasText: body });
}

/**
 * Send a friend request by email address and have the recipient accept it.
 * Leaves the browser signed in as the recipient.
 */
export async function befriend(
  page: Page,
  fromEmail: string,
  toEmail: string,
  note = "it is me",
): Promise<void> {
  await signIn(page, fromEmail);
  await page.goto("/friends");
  await page.getByLabel("Their email address").fill(toEmail);
  await page.getByLabel("A line so they know it is you").fill(note);
  await page.getByRole("button", { name: "Send the request" }).click();
  await signOut(page);

  await signIn(page, toEmail);
  await page.goto("/friends");
  await page.getByRole("button", { name: "Accept" }).first().click();
  await expect(page.getByText(/You are friends/)).toBeVisible();
}
