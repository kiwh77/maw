import { test, expect } from '@playwright/test';

for (const width of [1100, 1024, 768, 390]) {
  test(`compact board navigation and canvas at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 820 });
    await page.goto('/?view=topo&project=p-harness');
    const menu = page.locator('#sidebar-toggle');
    const sidebar = page.locator('#app-shell-aside');
    await expect(page.locator('#stats .stat')).toHaveCount(5);
    await expect(sidebar).toBeHidden();
    await expect(menu).toHaveAttribute('aria-expanded', 'false');
    const dimensions = await page.evaluate(() => ({
      stats: document.querySelector('#stats')!.getBoundingClientRect().height,
      topbar: document.querySelector('.topbar')!.getBoundingClientRect().height,
      canvas: document.querySelector('#topology')!.getBoundingClientRect().height,
      overflow: document.documentElement.scrollWidth > innerWidth,
    }));
    expect(dimensions.stats).toBeLessThan(52);
    expect(dimensions.topbar).toBeLessThanOrEqual(48);
    expect(dimensions.canvas).toBeGreaterThan(400);
    expect(dimensions.overflow).toBe(false);
    await menu.click();
    await expect(sidebar).toBeVisible();
    await expect(menu).toHaveAttribute('aria-expanded', 'true');
    await page.keyboard.press('Escape');
    await expect(sidebar).toBeHidden();
    await expect(menu).toBeFocused();
    await menu.click();
    await page.locator('#vnext-nav [data-view="board"]').click();
    await expect(sidebar).toBeHidden();
    await expect(page.locator('.board')).toBeVisible();
  });
}

test('desktop sidebar can collapse and viewport changes restore compact defaults', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/?view=topo&project=p-harness');
  const sidebar = page.locator('#app-shell-aside');
  const menu = page.locator('#sidebar-toggle');
  await expect(sidebar).toBeVisible();
  const before = await page.locator('.workspace').boundingBox();
  await menu.click();
  await expect(sidebar).toBeHidden();
  const after = await page.locator('.workspace').boundingBox();
  expect(after!.width).toBeGreaterThan(before!.width);
  await menu.click();
  await expect(sidebar).toBeVisible();
  await page.setViewportSize({ width: 768, height: 1024 });
  await expect(sidebar).toBeHidden();
  await menu.click();
  await page.locator('.sidebar-backdrop').click({ position: { x: 500, y: 200 } });
  await expect(sidebar).toBeHidden();
});
