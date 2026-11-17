import { test, expect } from '@playwright/test';

for (const width of [1920, 1440, 1024, 768, 390]) {
  for (const theme of ['light', 'dark']) {
    test(`workbench visual system: ${width}px ${theme}`, async ({ page }) => {
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      await page.setViewportSize({ width, height: 900 });
      await page.goto('/?view=board&project=p-harness');
      await expect(page.locator('.card')).toHaveCount(3);
      await page.selectOption('#theme-select', theme);
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
      await expect(page.locator('.card[data-status="blocked"] .badge')).toHaveClass(/badge--blocked/);
      await expect(page.locator('#view-toggle svg')).toHaveCount(4);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
      expect(overflow).toBe(false);
      const filterButton = page.locator('#filter-toggle');
      if (width <= 1100) {
        await expect(page.locator('#filters')).toBeHidden();
        await filterButton.click();
      }
      await expect(page.locator('#filters')).toBeVisible();
      await page.selectOption('#f-status', 'blocked');
      await expect(filterButton.locator('.filter-count')).toHaveText('1');
      await expect(page).toHaveURL(/status=blocked/);
      await page.locator('#f-reset').click();
      await expect(filterButton.locator('.filter-count')).toHaveCount(0);
      await filterButton.click();
      await expect(page.locator('#filters')).toBeHidden();
      await page.keyboard.press('/');
      await expect(page.locator('#task-search')).toBeFocused();
      await page.locator('#task-search').fill('Harness ready');
      await expect(page.locator('.card')).toHaveCount(1);
      await page.locator('#task-search').fill('');
      await expect(page.locator('.card')).toHaveCount(3);
      await page.locator('.card').first().click();
      await expect(page.locator('#drawer.open')).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(page.locator('#drawer.open')).toHaveCount(0);
      await page.locator('#view-topo').click();
      await expect(page.locator('.topo-x6-node:not(.stub)')).toHaveCount(3);
      await expect(page.locator('.topo-x6-node[data-status="blocked"]')).toBeVisible();
      await expect(page.locator('#page-h1')).toBeVisible();
      await expect(page.locator('#theme-select')).toHaveValue(theme);
      expect(errors).toEqual([]);
      if (width > 1100) await expect(page.locator('.sidebar-close')).toBeHidden();
    });
  }
}

test('settings shares theme and stays readable at desktop and tablet widths', async ({ page }) => {
  await page.route('**/api/runtime', route => route.fulfill({ json: {
    auto_execution: { auto_enabled: false, managed_execution_limit: 2, node_capacity: 3,
      max_parallel_reference: 4, retention: { days: 30, runs: 50 }, revision: 1 },
  } }));
  await page.goto('/?view=settings');
  await expect(page.locator('.settings-current')).toBeVisible();
  for (const width of [1920, 1440, 768, 390]) {
    await page.setViewportSize({ width, height: 900 });
    await expect(page.locator('#page-h1')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
    if (width > 1100) {
      const section = await page.locator('.settings-section').first().boundingBox();
      expect(section!.width).toBeLessThanOrEqual(960);
    }
  }
});
