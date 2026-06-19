import { test, expect } from "@playwright/test";

test.describe("Dashboard e2e", () => {
  test("login → create endpoint → create subscription → send test event → delivery succeeded", async ({
    page,
  }) => {
    // 1. Navigate to dashboard — should see login page
    await page.goto("/");
    await expect(page.locator('input[type="password"]')).toBeVisible({
      timeout: 10_000,
    });

    // 2. Log in with dev password
    await page.fill('input[type="password"]', "dev-dashboard-pw");
    await page.click('button:has-text("Sign in")');

    // 3. Wait for redirect to endpoints list
    await expect(page).toHaveURL(/\/endpoints/, { timeout: 10_000 });

    // 4. Create an endpoint
    await page.click('a:has-text("New endpoint")');
    await expect(page).toHaveURL(/\/endpoints\/new/);

    await page.fill('#description', "e2e-test-endpoint");
    await page.fill('#url', "http://test-receiver:9090/succeed");
    await page.click('button:has-text("Create")');

    // 5. SecretReveal modal appears after creating an endpoint — dismiss it
    await expect(page.locator('button:has-text("I saved it")')).toBeVisible({ timeout: 10_000 });
    await page.click('button:has-text("I saved it")');

    // 6. Wait for redirect to endpoints list, then click into the new endpoint
    await expect(page).toHaveURL(/\/endpoints$/, { timeout: 10_000 });
    const endpointRow = page.locator('tr', { hasText: "e2e-test-endpoint" });
    await endpointRow.locator('a').first().click();
    await expect(page).toHaveURL(/\/endpoints\/[a-f0-9-]+/, { timeout: 10_000 });
    const endpointUrl = page.url();
    const endpointId = endpointUrl.split("/").pop()!;

    // 8. Navigate to subscriptions list
    await page.goto('/subscriptions');
    await expect(page).toHaveURL(/\/subscriptions/);

    // 9. Create a subscription for the endpoint
    await page.click('a:has-text("New Subscription")');
    await expect(page).toHaveURL(/\/subscriptions\/new/);

    await page.fill('#topic_pattern', "demo.*");
    // Select the endpoint we just created
    await page.fill('#endpoint_id', endpointId);
    await page.click('button:has-text("Create")');

    // 10. Wait for redirect to subscriptions list, then click into the new subscription
    await expect(page).toHaveURL(/\/subscriptions$/, { timeout: 10_000 });
    const subRow = page.locator('tr', { hasText: "demo.*" });
    await subRow.locator('a').first().click();
    await expect(page).toHaveURL(/\/subscriptions\/[a-f0-9-]+/, { timeout: 10_000 });

    // 11. Navigate to test event page
    await page.goto('/test-event');
    await expect(page).toHaveURL(/\/test-event/);

    // 12. Send a test event
    await page.fill('#topic', "demo.e2e");
    await page.fill('textarea', JSON.stringify({ ok: true }));
    await page.click('button:has-text("Send")');

    // 13. Wait for event ID to appear (success)
    await expect(page.getByText(/Event ID:/i)).toBeVisible({ timeout: 15_000 });

    // 14. Navigate to deliveries
    await page.goto('/deliveries');
    await expect(page).toHaveURL(/\/deliveries/);

    // 15. Poll the deliveries view until a delivery shows "succeeded".
    //     The Deliveries view fetches once on mount and does not auto-refetch,
    //     so we reload on each attempt to observe the pending→succeeded
    //     transition (the plan's "poll the deliveries view" intent).
    await expect(async () => {
      await page.reload();
      await expect(page.locator("text=succeeded").first()).toBeVisible({
        timeout: 2_000,
      });
    }).toPass({ timeout: 45_000 });
  });
});
