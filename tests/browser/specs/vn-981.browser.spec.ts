import { test, expect, type Page } from '@playwright/test';

type Snapshot = Record<string, unknown>;

const PROJECT = {
  id: 'p-harness',
  name: 'Harness',
  path: '/tmp/harness',
  main_branch: 'main',
  max_parallel: 2,
  created_at: '',
  last_seen: '',
};

function task(id: string, title: string, status: string) {
  return {
    project_id: 'p-harness',
    project_name: 'Harness',
    id,
    title,
    status,
    priority: 10,
    merge_order: 10,
    owner: 'trae-goalagents-1',
    deps: [],
    write_scope: [],
    read_scope: [],
    acceptance: [],
    branch: '',
    worktree: '',
    attempts: 1,
    blocked_reason: '',
    heartbeat: '2026-10-23T04:00:00Z',
    prev_status: '',
    revision: 1,
  };
}

function snapshot(tasks: ReturnType<typeof task>[]): Snapshot {
  const counts: Record<string, number> = {};
  for (const item of tasks) counts[item.status] = (counts[item.status] || 0) + 1;
  const done = counts.done || 0;
  return {
    updated_at: '2026-10-23 12:00:00',
    max_parallel: 2,
    total: tasks.length,
    done,
    active: 0,
    blocked: counts.blocked || 0,
    percent: tasks.length ? Math.floor((100 * done) / tasks.length) : 0,
    next_hint: 'Fixture injected at the browser test boundary',
    risks: [],
    counts,
    tasks,
    agents: [
      {
        project_id: 'p-harness',
        project_name: 'Harness',
        id: 'trae-goalagents-1',
        role: 'worker',
        status: 'working',
        task_id: 'VN-981',
        registered_at: '',
        last_seen: '2026-10-23T04:00:00Z',
      },
    ],
    projects: [PROJECT],
    filter_project: '',
    filter_agent: '',
    filter_status: '',
    global: true,
  };
}

async function waitForBoard(page: Page, count: number) {
  await expect(page.locator('#stats')).toContainText(`/ ${count}`);
}

test.describe('VN-981 real browser harness', () => {
  test('VN-981 loads production assets with injected API and SSE data', async ({ page }) => {
    const stream = page.waitForRequest('**/api/events');
    await page.goto('/');
    await waitForBoard(page, 3);
    await expect(page.locator('#next')).toContainText('Fixture injected');
    await expect(page.locator('#agents')).toContainText('trae-goalagents-1');
    await expect(page.locator('#project-nav')).toContainText('Harness');
    await stream;

    await page.screenshot({ path: 'reports/screenshots/vn-981-board.png', fullPage: true });
  });

  test('VN-981 supports keyboard focus across search and views', async ({ page }) => {
    await page.goto('/');
    await waitForBoard(page, 3);

    await page.keyboard.press('/');
    await expect(page.locator('#task-search')).toBeFocused();

    await page.locator('#view-topo').focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#view-topo')).toHaveAttribute('aria-pressed', 'true');

    await page.locator('#view-board').focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#view-board')).toHaveAttribute('aria-pressed', 'true');
  });

  test('VN-981 ignores stale data when requests resolve out of order', async ({ page }) => {
    let ordering = false;
    let sequence = 0;

    await page.route('**/api/board**', async route => {
      if (!ordering) {
        await route.continue();
        return;
      }
      sequence += 1;
      const stale = sequence === 1;
      await new Promise(resolve => setTimeout(resolve, stale ? 400 : 10));
      await route.fulfill({
        json: stale
          ? snapshot([task('VN-OLD', 'stale snapshot', 'ready')])
          : snapshot([task('VN-981', 'latest one', 'ready'), task('VN-982', 'latest two', 'done')]),
      });
    });

    await page.goto('/');
    await waitForBoard(page, 3);

    await page.evaluate(() => {
      (window as any).syncBoard = async () => {};
    });
    ordering = true;
    await page.evaluate(() => {
      (window as any).tick();
      (window as any).tick();
    });

    await waitForBoard(page, 2);
    await page.waitForTimeout(600);
    await expect(page.locator('#stats')).toContainText('50%');
    await expect(page.locator('#stats')).not.toContainText('/ 1');
  });
});
