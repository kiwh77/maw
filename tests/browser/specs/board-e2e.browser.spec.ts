import { test, expect, type Page, type APIRequestContext } from '@playwright/test';

/**
 * 看板真实浏览器 E2E（B 组）。
 *
 * 目标是一个**已启动的隔离实例**（默认 127.0.0.1:7421，MAW_GLOBAL 指向副本库）。
 * 不依赖 tests/browser 的 fixture harness，因此断言的是真实数据与真实 SSE。
 *
 *   E2E_BASE=http://127.0.0.1:7421 E2E_ALLOW_WRITE=1 \
 *     npx --prefix tests/browser playwright test specs/board-e2e.browser.spec.ts
 */
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:7421';

type Snapshot = {
  total: number;
  done: number;
  active: number;
  blocked: number;
  percent: number;
  tasks: Array<Record<string, unknown>>;
  agents: Array<Record<string, unknown>>;
  projects: Array<{ id: string; name?: string }>;
  counts: Record<string, number>;
};

const VIEWS = [
  'board', 'factory', 'topo', 'nodes', 'tasks', 'runs', 'deliveries',
  'timeline', 'activity', 'sessions', 'analytics', 'projects', 'settings',
];
const NO_PROJECT_VIEWS = ['timeline', 'activity', 'sessions', 'analytics'];

let global: Snapshot;
let scoped: Snapshot;
let pid: string;
let firstTask: Record<string, unknown>;

function collectErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') errors.push(msg.text());
  });
  page.on('pageerror', (err) => errors.push('pageerror: ' + err.message));
  return errors;
}

async function open(page: Page, path = '/') {
  await page.goto(BASE + path, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('#page-h1', { timeout: 20_000 });
}

async function openView(page: Page, view: string, project = pid) {
  const q = new URLSearchParams({ view });
  if (project) q.set('project', project);
  await open(page, '/?' + q.toString());
  await page.waitForFunction(() => (document.querySelector('#page-h1')?.textContent || '').trim().length > 0);
}

async function apiBoard(request: APIRequestContext, query = ''): Promise<Snapshot> {
  const res = await request.get(BASE + '/api/board' + query);
  expect(res.status()).toBe(200);
  return (await res.json()) as Snapshot;
}

test.beforeAll(async ({ request }) => {
  const probe = await request.get(BASE + '/api/board').catch(() => null);
  if (!probe || !probe.ok()) {
    test.skip(true, `隔离实例 ${BASE} 不可用，先启动 maw serve（MAW_GLOBAL 指向副本）`);
  }
  global = await apiBoard(request);
  pid = global.projects[0].id;
  scoped = await apiBoard(request, '?project=' + pid);
  firstTask = (scoped.tasks || [])[0] || global.tasks[0];
});

test.describe('B 组：看板真实渲染', () => {
  test('B-01 无工程首屏落到执行节点视图且无脚本错误', async ({ page }) => {
    const errors = collectErrors(page);
    await open(page);
    await expect(page.locator('#page-h1')).toHaveText(/Execution nodes|执行节点/i);
    await expect(page.locator('#vnext-view')).toBeVisible();
    await expect(page.locator('#app-shell-aside')).toBeVisible();
    expect(errors, errors.join('\n')).toEqual([]);
  });

  test('B-02 侧栏导航齐全且当前项可辨识', async ({ page }) => {
    await open(page);
    const items = page.locator('#vnext-nav .nav-item');
    await expect(items).toHaveCount(10);
    await expect(page.locator('#vnext-nav .nav-item.active')).toHaveCount(1);

    await page.locator('#vnext-nav .nav-item[data-view="tasks"]').click();
    await expect(page.locator('#vnext-nav .nav-item[data-view="tasks"]')).toHaveAttribute('aria-current', 'page');
    await expect(page.locator('#vnext-nav .nav-item[data-view="tasks"]')).toHaveClass(/active/);
  });

  test('B-03 13 个视图都能渲染出标题、说明与正文', async ({ page }) => {
    const errors = collectErrors(page);
    const problems: string[] = [];
    for (const view of VIEWS) {
      await openView(page, view);
      await page.waitForTimeout(400);
      const info = await page.evaluate(() => ({
        h1: (document.querySelector('#page-h1')?.textContent || '').trim(),
        desc: (document.querySelector('#page-desc')?.textContent || '').trim(),
        body: (document.querySelector('.content')?.textContent || '').replace(/\s+/g, ' ').trim(),
      }));
      if (!info.h1) problems.push(`${view}: 缺 h1`);
      if (!info.desc) problems.push(`${view}: 缺说明`);
      if (info.body.length < 40) problems.push(`${view}: 正文过短(${info.body.length})`);
      if (/unavailable|undefined|\[object Object\]/.test(info.body)) problems.push(`${view}: 出现异常占位文本`);
    }
    expect(problems, problems.join(' | ')).toEqual([]);
    expect(errors, errors.join('\n')).toEqual([]);
  });

  test('B-04 观测视图未选工程时应说明「先选工程」', async ({ page }) => {
    const states: Record<string, string> = {};
    const bad: string[] = [];
    for (const view of NO_PROJECT_VIEWS) {
      await open(page, '/?view=' + view);
      await page.waitForTimeout(600);
      states[view] = ((await page.locator('#vnext-view').innerText()) || '').replace(/\s+/g, ' ').trim();
      if (!/工程|project/i.test(states[view])) bad.push(`${view}: ${states[view].slice(0, 90)}`);
    }
    expect(bad, '这些视图把「没选工程」渲染成了「没有数据」' + JSON.stringify(states)).toEqual([]);
  });

  test('B-05 工程下拉与数据联动', async ({ page }) => {
    await openView(page, 'board');

    const options = page.locator('#f-project option');
    await expect(options).toHaveCount(global.projects.length + 1);
    await expect(page.locator('#f-project option[value=""]')).toHaveText(/All projects|全部工程/);
    await expect(page.locator(`#f-project option[value="${pid}"]`)).toContainText(/tasks|任务/);

    // BOARD_DESIGN §Views：选「全部工程」应强制看板视图，而不是把用户踢去别的视图。
    await page.selectOption('#f-project', '');
    await expect(page.locator('#page-h1')).toHaveText(/Task board|任务看板/i);

    await page.selectOption('#f-project', pid);
    // 设计约定：工程选择写 URL（旧链接形态为 hash 锚点）。
    await expect.poll(async () => decodeURIComponent(page.url())).toContain(pid);
    await expect(page.locator('#stats')).toContainText(String(scoped.total));
    await expect(page.locator('#board-count')).toContainText(String(scoped.total));
  });

  test('B-06 看板统计与接口一致', async ({ page }) => {
    await openView(page, 'board');
    const stats = (await page.locator('#stats').innerText()).replace(/\s+/g, ' ');
    expect(stats).toContain(String(scoped.total));
    expect(stats).toContain(String(scoped.done));
    expect(stats).toContain(`${scoped.percent}%`);
    await expect(page.locator('.card').first()).toBeVisible();

    const cards = await page.locator('.card').count();
    expect(cards).toBeLessThanOrEqual(scoped.total);
    expect(cards).toBeGreaterThan(0);
  });

  test('B-07 侧栏只列工作中的 Agent', async ({ page }) => {
    await openView(page, 'board');
    const working = (scoped.agents || []).filter((a) => ['working', 'busy'].includes(String(a.status || '')));
    const rail = page.locator('#agents');
    await expect(rail.locator('.agent-row')).toHaveCount(working.length);
    if (!working.length) await expect(rail).toContainText(/No working agents|暂无工作中 Agent/);
  });

  test('B-08 Agent 与状态筛选即时生效', async ({ page }) => {
    const owners = [...new Set((scoped.tasks || []).map((t) => String(t.owner || '')).filter(Boolean))].sort();
    test.skip(!owners.length, '数据里没有 owner，无法测 Agent 筛选');
    await openView(page, 'board');

    await page.selectOption('#f-agent', owners[0]);
    await expect
      .poll(async () => {
        const texts = await page.locator('.card .owner').allInnerTexts();
        return texts.length > 0 && texts.every((t) => t.trim() === owners[0]);
      }, { timeout: 10_000 })
      .toBeTruthy();

    await page.selectOption('#f-agent', '');
    await page.selectOption('#f-status', 'done');
    await expect
      .poll(async () => {
        const badges = await page.locator('.card .badge').allInnerTexts();
        return badges.length > 0 && badges.every((b) => b.trim() === 'done');
      }, { timeout: 10_000 })
      .toBeTruthy();
  });

  test('B-09 重置清空 Agent、状态与搜索', async ({ page }) => {
    await openView(page, 'board');
    const owners = [...new Set((scoped.tasks || []).map((t) => String(t.owner || '')).filter(Boolean))].sort();
    await page.selectOption('#f-status', 'done');
    await page.fill('#task-search', 'zzz');
    await expect(page.locator('#task-search')).toHaveValue('zzz');

    await page.click('#f-reset');
    await expect(page.locator('#task-search')).toHaveValue('');
    await expect(page.locator('#f-status')).toHaveValue('');
    if (owners.length) await expect(page.locator('#f-agent')).toHaveValue('');
    await expect(page.locator('.card').first()).toBeVisible();
  });

  test('B-10 斜杠快捷键聚焦搜索框', async ({ page }) => {
    await openView(page, 'board');
    await page.locator('body').click({ position: { x: 5, y: 5 } });
    await page.keyboard.press('/');
    await expect(page.locator('#task-search')).toBeFocused();
  });

  test('B-11 搜索即时过滤看板卡片', async ({ page }) => {
    await openView(page, 'board');
    const total = await page.locator('.card').count();
    const needle = String(firstTask.id);
    await page.fill('#task-search', needle);
    await expect
      .poll(async () => page.locator('.card').count(), { timeout: 8000 })
      .toBeLessThan(total);
    const ids = await page.locator('.card').evaluateAll((els) => els.map((e) => e.getAttribute('data-tid')));
    expect(ids.length).toBeGreaterThan(0);
    expect(ids.every((id) => String(id).toLowerCase().includes(needle.toLowerCase()))).toBeTruthy();
  });

  test('B-12 任务抽屉可开可关且不影响列表', async ({ page }) => {
    await openView(page, 'board');
    const card = page.locator('.card').first();
    const tid = await card.getAttribute('data-tid');
    await card.click();
    await expect(page.locator('#drawer')).toBeVisible();
    await expect(page.locator('#drawer')).toHaveAttribute('aria-modal', 'true');
    await expect(page.locator('#d-id')).toHaveText(String(tid));
    await expect(page.locator('#drawer-body')).not.toBeEmpty();

    await page.keyboard.press('Escape');
    await expect(page.locator('#drawer-mask')).toBeHidden();
    await expect(page.locator('.card').first()).toBeVisible();
  });

  test('B-13 抽屉打开后焦点进入对话框', async ({ page }) => {
    await openView(page, 'board');
    await page.locator('.card').first().click();
    await expect(page.locator('#drawer')).toBeVisible();
    const inside = await page.evaluate(() => {
      const drawer = document.querySelector('#drawer');
      return Boolean(drawer && document.activeElement && drawer.contains(document.activeElement));
    });
    expect(inside, '焦点仍停在卡片上：抽屉没有接管焦点（BOARD_DESIGN 承诺 trap focus）').toBeTruthy();
  });

  test('B-14 深链刷新后视图与列表仍在', async ({ page }) => {
    await openView(page, 'tasks');
    await expect(page.locator('#vnext-view table')).toBeVisible();
    await expect.poll(async () => page.locator('#vnext-view tbody tr').count()).toBeGreaterThan(0);
    const rows = await page.locator('#vnext-view tbody tr').count();

    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.locator('#page-h1')).toHaveText(/Task queue|任务队列/i);
    await expect.poll(async () => page.locator('#vnext-view tbody tr').count()).toBe(rows);
  });

  test('B-15 主题切换即时生效并持久化', async ({ page }) => {
    await openView(page, 'board');
    await page.selectOption('#theme-select', 'dark');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    expect(await page.evaluate(() => localStorage.getItem('maw-theme'))).toBe('dark');

    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.waitForSelector('#theme-select');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
    await expect(page.locator('#theme-select')).toHaveValue('dark');

    await page.selectOption('#theme-select', 'light');
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  });

  test('B-16 语言切换改变文案并持久化', async ({ page }) => {
    await openView(page, 'board');
    const enH1 = await page.locator('#page-h1').innerText();
    await page.selectOption('#lang-select', 'zh');
    await expect(page.locator('html')).toHaveAttribute('lang', /^zh/);
    const zhH1 = await page.locator('#page-h1').innerText();
    expect(zhH1).not.toBe(enH1);
    expect(await page.evaluate(() => localStorage.getItem('maw-board-lang'))).toBe('zh');

    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.waitForSelector('#page-h1');
    await expect(page.locator('#page-h1')).toHaveText(zhH1);
  });

  test('B-17 外部写入后看板自动更新（SSE 或轮询）', async ({ page }) => {
    test.skip(!process.env.E2E_ALLOW_WRITE, '未设置 E2E_ALLOW_WRITE=1，跳过副本写库用例');
    await openView(page, 'board');
    await expect(page.locator('#agents')).toBeVisible();
    const actor = 'e2e-live-' + Date.now();
    const before = await page.locator('#agents .agent-row').count();

    // 同源写入口：会话 + CSRF，写的是隔离副本，用来制造一次真实的数据变更。
    const write = await page.evaluate(
      async ({ pid, actor }) => {
        const s = await fetch('/api/session', { method: 'POST' }).then((r) => r.json());
        const r = await fetch('/api/status', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': String(s.csrf) },
          body: JSON.stringify({ project_id: pid, actor, message: 'live probe', status: 'working' }),
        });
        return { status: r.status, body: await r.text() };
      },
      { pid, actor },
    );
    expect(write.status, write.body).toBe(200);

    await expect
      .poll(async () => page.locator('#agents .agent-row').count(), { timeout: 12_000 })
      .toBeGreaterThan(before);

    // 收尾：置回 idle，避免污染后续「工作中」列表。
    await page.evaluate(
      async ({ pid, actor }) => {
        const s = await fetch('/api/session', { method: 'POST' }).then((r) => r.json());
        await fetch('/api/status', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': String(s.csrf) },
          body: JSON.stringify({ project_id: pid, actor, message: '', status: 'idle' }),
        });
      },
      { pid, actor },
    );
  });

  test('B-18 服务不可达时降级为离线并保留旧数据', async ({ page }) => {
    await openView(page, 'board');
    const cards = await page.locator('.card').count();
    // 两条取数路径都要断：/api/board 与静态导出兼容的 ./snapshot.json。
    await page.route('**/api/board**', (route) => route.abort());
    await page.route('**/snapshot.json**', (route) => route.abort());
    await expect
      .poll(async () => (await page.locator('#subtitle').innerText()).toLowerCase(), { timeout: 15_000 })
      .toMatch(/offline|离线/);
    expect(await page.locator('.card').count()).toBeGreaterThan(0);
    expect(await page.locator('.card').count()).toBeLessThanOrEqual(cards);

    await page.unroute('**/api/board**');
    await page.unroute('**/snapshot.json**');
    await expect
      .poll(async () => (await page.locator('#subtitle').innerText()).toLowerCase(), { timeout: 15_000 })
      .not.toMatch(/offline|离线/);
  });

  test('B-19 390px 窄屏无横向溢出', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await openView(page, 'board');
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow, `窄屏横向溢出 ${overflow}px`).toBeLessThanOrEqual(1);
    const aside = await page.locator('#app-shell-aside').isVisible();
    expect(await page.locator('#page-h1').isVisible()).toBeTruthy();
    expect(aside, '设计约定窄屏隐藏侧栏').toBeFalsy();
  });

  test('B-20 拓扑视图渲染依赖节点', async ({ page }) => {
    await openView(page, 'topo');
    await expect(page.locator('#topology')).toBeVisible();
    await expect
      .poll(async () => page.locator('#topology .x6-node').count(), { timeout: 20_000 })
      .toBeGreaterThan(0);
    await expect(page.locator('#view-topo')).toHaveAttribute('aria-pressed', 'true');
  });

  test('B-21 未选工程时不提供拓扑入口', async ({ page }) => {
    await open(page, '/?view=board');
    await expect(page.locator('#view-topo')).toHaveCount(0);
    await open(page, '/?view=topo');
    await page.waitForTimeout(500);
    expect(await page.locator('#view-topo').count()).toBe(0);
  });

  test('B-22 复制加入提示词写入剪贴板', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    await openView(page, 'board');
    const btn = page.locator('#f-copy-join');
    await expect(btn).toBeVisible();
    await expect(btn).toHaveAccessibleName(/./);
    await btn.click();
    const text = await page.evaluate(() => navigator.clipboard.readText());
    expect(text.length).toBeGreaterThan(20);
    expect(text).toMatch(/maw|register|next/i);
  });

  test('B-23 空结果给出空态而不是白屏', async ({ page }) => {
    await openView(page, 'board');
    await page.fill('#task-search', 'no-such-task-zzzz-0000');
    await expect.poll(async () => page.locator('.card').count(), { timeout: 10_000 }).toBe(0);
    const body = (await page.locator('.content').innerText()).replace(/\s+/g, ' ');
    expect(body.length).toBeGreaterThan(30);
    await expect(page.locator('#next')).toBeVisible();
  });

  test('B-24 键盘可达：Tab 进入导航、卡片回车打开', async ({ page }) => {
    await openView(page, 'board');
    await page.locator('body').click({ position: { x: 5, y: 5 } });
    await page.keyboard.press('Tab');
    const firstStop = await page.evaluate(() => document.activeElement?.tagName || '');
    expect(firstStop).not.toBe('BODY');

    const card = page.locator('.card').first();
    await card.focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#drawer')).toBeVisible();
  });

  test('B-26 看板泳道覆盖全部状态，卡片总数与统计一致', async ({ page }) => {
    // 用全局看板：只有跨工程视图里才会出现 backlog 等未被泳道覆盖的状态。
    await open(page, '/?view=board');
    await expect(page.locator('#columns')).toBeVisible();
    const laneCounts = await page.locator('#columns .col > h3 .count').allInnerTexts();
    const sum = laneCounts.reduce((acc, n) => acc + Number(n.trim() || 0), 0);
    const cards = await page.locator('.card').count();
    expect(sum).toBe(cards);
    // 页面自报的任务数与泳道计数之和必须一致：差值就是「有任务没落位」。
    const total = Number((await page.locator('#columns').getAttribute('data-total')) || '0');
    const laneTotal = Number((await page.locator('#columns').getAttribute('data-lane-total')) || '0');
    expect(laneTotal, '泳道计数之和与看板任务数不一致：有任务没落位').toBe(total);
    expect(sum).toBe(laneTotal);
    expect(cards).toBe(total);
    expect(
      total,
      `看板任务数 ${total} 与接口 total ${global.total} 不一致：backlog/cancelled 没有落位（BOARD_DESIGN 的 Queued 含 backlog）`,
    ).toBe(global.total);
  });

  test('B-27 全部工程时拓扑不可达，不会跳到执行节点', async ({ page }) => {
    await open(page, '/?view=board');
    await page.selectOption('#f-project', '');
    await expect(page.locator('#page-h1')).toHaveText(/Task board|任务看板/i);

    // 拓扑入口在「全部工程」下禁用。
    await expect(page.locator('#view-topo')).toBeDisabled();

    // 侧边栏里点拓扑也不该把人带走：拓扑按单工程物化，多工程图读不懂。
    const topo = page.locator('#vnext-nav [data-view="topo"]');
    if (await topo.count()) {
      await topo.click();
      await page.waitForTimeout(500);
      await expect(page.locator('#page-h1')).toHaveText(/Task board|任务看板/i);
      expect(page.url(), '拓扑视图要求单工程，不该在「全部工程」下可达').not.toMatch(/view=topo/);
    }
  });

  test('B-25 筛选与搜索在刷新后保留', async ({ page }) => {
    await openView(page, 'board');
    await page.selectOption('#f-status', 'done');
    await page.fill('#task-search', String(firstTask.id));
    await expect(page.locator('#task-search')).toHaveValue(String(firstTask.id));
    await page.waitForTimeout(400);

    const url = page.url();
    expect(url, '看板视图把筛选丢出地址栏：刷新/分享链接都会回到未筛选状态').toMatch(/status=done|q=/);
    // 工程锚点走 hash（#<project_id>），不该在查询串里再出现一份。
    expect(url, '工程筛选在查询串与 hash 里重复出现，解析优先级成了暗规则').not.toMatch(/[?&]project=/);

    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.waitForSelector('#task-search');
    await expect(page.locator('#task-search')).toHaveValue(String(firstTask.id));
    await expect(page.locator('#f-status')).toHaveValue('done');
  });

  test('B-28 切换视图后筛选与搜索词仍在', async ({ page }) => {
    await openView(page, 'board');
    await page.selectOption('#f-status', 'done');
    await page.fill('#task-search', String(firstTask.id));
    await page.waitForTimeout(600);

    // 切走再切回来：筛选与搜索词都不该丢。
    await page.locator('#view-nodes').click();
    await page.waitForTimeout(500);
    await page.locator('#view-board').click();
    await page.waitForTimeout(700);

    await expect(page.locator('#f-status')).toHaveValue('done');
    await expect(page.locator('#task-search')).toHaveValue(String(firstTask.id));
    expect(page.url()).toMatch(/status=done|q=/);
  });
});
