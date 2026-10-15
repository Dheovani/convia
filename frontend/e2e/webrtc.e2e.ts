import { expect, test, type Page } from "@playwright/test";
import { createHmac, randomBytes } from "node:crypto";

// coturn's use-auth-secret credential (ADR 0023), made here as the rendezvous service makes it.
function relay(secret: string): RTCIceServer {
  const username = `${Math.floor(Date.now() / 1000) + 3600}:${randomBytes(8).toString("hex")}`;
  const credential = createHmac("sha1", secret)
    .update(username)
    .digest("base64");
  const urls =
    process.env.CONVIA_TURN_URL ?? "turn:127.0.0.1:3478?transport=udp";
  return { urls, username, credential };
}

async function connect(page: Page, ice: RTCIceServer[], relayOnly: boolean) {
  await page.goto("/e2e/harness.html");
  await page.evaluate(
    ([ice, relayOnly]) => window.harness.start(ice, relayOnly),
    [ice, relayOnly] as const,
  );
  await expect
    .poll(() => page.evaluate(() => window.harness.opened()), {
      timeout: 15_000,
    })
    .toEqual([true, true]);
}

async function exchange(page: Page, message: number[]) {
  await page.evaluate((m) => window.harness.send(0, m), message);
  await page.evaluate((m) => window.harness.send(1, m), [...message].reverse());
  await expect
    .poll(() => page.evaluate(() => window.harness.received(1).at(-1)))
    .toEqual(message);
  await expect
    .poll(() => page.evaluate(() => window.harness.received(0).at(-1)))
    .toEqual([...message].reverse());
}

test("two connections open their channel and exchange messages", async ({
  page,
}) => {
  await connect(page, [], false);
  await exchange(page, [1, 2, 3]);
  const large = Array.from({ length: 60_000 }, (_, i) => i % 256);
  await exchange(page, large);
});

test.describe("through coturn's relay", () => {
  const secret = process.env.TURN_SECRET ?? "";
  test.skip(secret === "", "needs TURN_SECRET and coturn running");

  test("a relayed connection carries messages", async ({ page }) => {
    await connect(page, [relay(secret)], true);
    expect(
      await page.evaluate(() => window.harness.candidateTypes(0)),
    ).toContain("relay");
    await exchange(page, [4, 5, 6]);
  });

  test("a new credential moves the relayed connection with an ICE restart", async ({
    page,
  }) => {
    await connect(page, [relay(secret)], true);
    await page.evaluate(
      (ice) => window.harness.configure(0, ice),
      [relay(secret)],
    );
    await expect
      .poll(() => page.evaluate(() => window.harness.offers(0)))
      .toBe(2);
    await exchange(page, [7, 8, 9]);
    expect(await page.evaluate(() => window.harness.closed())).toEqual([
      false,
      false,
    ]);
  });

  test("a credential coturn does not accept never opens", async ({ page }) => {
    await page.goto("/e2e/harness.html");
    await page.evaluate(
      (ice) => window.harness.start(ice, true),
      [{ ...relay(secret), credential: "wrong" }],
    );
    await page.waitForTimeout(5_000);
    expect(await page.evaluate(() => window.harness.opened())).toEqual([
      false,
      false,
    ]);
  });
});
