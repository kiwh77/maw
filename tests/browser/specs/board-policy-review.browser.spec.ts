import { test, expect, type Page } from '@playwright/test';

/**
 * 执行策略（自动化策略）与回顾数据（时间轴／热图／会话／统计）的真实浏览器 E2E。
 *
 * 依赖已启动的隔离实例；回顾数据要有交付记录的工程，所以工程 ID 用 E2E_REVIEW_PID 指定。
 * 空库用例（F 组）需要 E2E_EMPTY_BASE 指向一个空 MAW_GLOBAL 起的实例。
 */
const BASE = process.env.E2E_BASE || 'http://127.0.0.1:7421';
const EMPTY = process.env.E2E_EMPTY_BASE || '';
const REVIEW_PID = process.env.E2E_REVIEW_PID || '';

type Snapshot = { projects: Array<{ id: string; name?: string }>; total: number };

async function open(page: Page, path: string, base = BASE) {
  await page.goto(base + path, { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('#page-h1', { timeout: 20_000 });
}

async function openView(page: Page, view: string, project: string, base = BASE) {
  const q = new URLSearchParams({ view });
  if (project) q.set('project', project);
  await open(page, '/?' + q.toString(), base);
  await page.waitForTimeout(600);
}

/** 在页面上下文里调接口（自动带会话 Cookie）。 */
async function apiJson(page: Page, path: string) {
  return page.evaluate(async (p) => {
    await fetch('/api/session', { method: 'POST' });
    const r = await fetch(p, { headers: { Accept: 'application/json' } });
    return { status: r.status, body: await r.json().catch(() => null) };
  }, path);
}

test.describe('D 组：执行策略（自动化策略）', () => {
  let pid = '';

  test.beforeEach(async ({ page }) => {
    const res = await page.request.get(BASE + '/api/board');
    if (!res.ok()) test.skip(true, `实例 ${BASE} 不可用`);
    const snap = (await res.json()) as Snapshot;
    pid = snap.projects[0]?.id || '';
    test.skip(!pid, '没有工程，策略页无内容可测');
    await openView(page, 'settings', pid);
  });

  test('D-01 计算上限显示服务端实际值（不是「—」）', async ({ page }) => {
    const api = await apiJson(page, '/api/runtime');
    const limit = api.body?.auto_execution?.managed_execution_limit;
    expect(typeof limit, '/api/runtime 应给出整数上限').toBe('number');
    const cell = page.locator('tr[data-concept="limit"] td.num');
    await expect(cell).toHaveText(String(limit));
  });

  test('D-02 平台支持未声明时自动化应显示「未就绪」', async ({ page }) => {
    const api = await apiJson(page, '/api/runtime');
    const platform = api.body?.auto_execution?.platform_supported;
    const cell = page.locator('tr[data-concept="auto"] td.num');
    const text = (await cell.innerText()).trim();
    if (platform === undefined) {
      // 设计注释：平台支持「没说不猜成支持」，缺失时不能显示成「关闭」。
      expect(text, '平台支持未声明却显示成「关闭」，会让人以为手动开一下就行').toMatch(/Not ready|未就绪/i);
    } else {
      expect(text.length).toBeGreaterThan(0);
    }
  });

  test('D-03 表单回填当前策略值', async ({ page }) => {
    const api = await apiJson(page, '/api/runtime');
    const limit = String(api.body?.auto_execution?.managed_execution_limit ?? '');
    const value = await page.locator('#settings-limit').inputValue();
    expect(value, '计算上限输入框没有回填当前值').toBe(limit);
  });

  test('D-04 点「保存设置」不跳走、不丢当前视图', async ({ page }) => {
    const before = page.url();
    await page.locator('#settings-limit').fill('2');
    await page.locator('.settings-form button[type="submit"]').click();
    await page.waitForTimeout(1200);
    expect(page.url(), '保存按钮触发了原生表单提交，把用户带离策略页').toBe(before);
    await expect(page.locator('#page-h1')).toHaveText(/Execution policy|执行策略/i);
  });

  test('D-05 页面不出现秘密字段且明确声明', async ({ page }) => {
    expect(await page.locator('input[type="password"]').count()).toBe(0);
    const body = (await page.locator('#vnext-view').innerText()).replace(/\s+/g, ' ');
    expect(body).toMatch(/no token|不含令牌|token key|凭据/i);
  });

  test('D-06 中文界面下策略文案本地化', async ({ page }) => {
    await page.selectOption('#lang-select', 'zh');
    await page.waitForTimeout(400);
    const body = (await page.locator('#vnext-view').innerText()).replace(/\s+/g, ' ');
    expect(body).toMatch(/自动模式|计算上限/);
  });
});

test.describe('E 组：回顾数据', () => {
  let pid = REVIEW_PID;

  test.beforeEach(async ({ page }) => {
    if (!pid) {
      const res = await page.request.get(BASE + '/api/board');
      const snap = (await res.json()) as Snapshot;
      pid = snap.projects[0]?.id || '';
    }
    test.skip(!pid, '没有工程');
  });

  test('E-01 时间轴未知态给出解释而不是「没有区间」', async ({ page }) => {
    await openView(page, 'timeline', pid);
    const api = await apiJson(page, `/api/v1/projects/${pid}/timeline?hours=24`);
    const root = page.locator('#vnext-view > div').first();
    await expect(root).toHaveAttribute('data-status', /unknown|ready/);
    const text = (await page.locator('#vnext-view').innerText()).replace(/\s+/g, ' ');
    if (api.body?.status === 'unknown') {
      expect(text, 'API 说未知，页面却说这段时间没有数据').toMatch(/incomplete|未知|不完整/i);
      expect(text).not.toMatch(/No intervals in this hour window/i);
    }
  });

  test('E-02 时间轴窗口标签能区分 24 小时跨度', async ({ page }) => {
    await openView(page, 'timeline', pid);
    const label = (await page.locator('.timeline__toolbar .field-hint').innerText()).replace(/\s+/g, ' ');
    // "16:14 – 16:14 (24h)" 这种只印时分的写法会让人以为窗口是 0 长度。
    const parts = label.match(/(\d{1,2}:\d{2})\s*[–-]\s*(\d{1,2}:\d{2})/);
    expect(parts, `窗口标签无法解析: ${label}`).toBeTruthy();
    if (parts && parts[1] === parts[2]) {
      expect(label, '24h 窗口只显示时分且首尾相同，缺少日期').toMatch(/\d{4}-\d{2}-\d{2}|[A-Z][a-z]{2}\s+\d{1,2}/);
    }
  });

  test('E-03 时间轴工具栏（上一段/下一段）可用', async ({ page }) => {
    await openView(page, 'timeline', pid);
    const before = (await page.locator('.timeline__toolbar .field-hint').innerText()).trim();
    const reqs: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/timeline')) reqs.push(r.url());
    });
    await page.locator('.timeline__toolbar [data-action="prev"]').click();
    await page.waitForTimeout(1200);
    const after = (await page.locator('.timeline__toolbar .field-hint').innerText()).trim();
    expect(reqs.length + (before === after ? 0 : 1), '点「Previous」既没发请求也没改变窗口').toBeGreaterThan(0);
  });

  test('E-04 热图把缺测小时标成 missing 而不是 0', async ({ page }) => {
    await openView(page, 'activity', pid);
    const missing = await page.locator('.activity__cell--missing').count();
    const cells = await page.locator('.activity__cell').count();
    expect(cells).toBeGreaterThanOrEqual(24);
    expect(missing, '没有 missing 标记：缺测被当成 0').toBeGreaterThan(0);
    const text = (await page.locator('#vnext-view').innerText()).replace(/\s+/g, ' ');
    expect(text).toMatch(/missing|缺测|未测/i);
  });

  test('E-05 统计页的交付数与接口一致且标注不完整', async ({ page }) => {
    await openView(page, 'analytics', pid);
    const api = await apiJson(page, `/api/v1/projects/${pid}/analytics?hours=48`);
    const count = api.body?.report?.deliveries?.count;
    const text = (await page.locator('#vnext-view').innerText()).replace(/\s+/g, ' ');
    if (typeof count === 'number') {
      expect(text, `接口交付数 ${count} 未出现在页面上`).toContain(String(count));
    }
    if (api.body?.status === 'unknown') {
      expect(text).toMatch(/incomplete|不完整|未知/i);
    }
  });

  test('E-06 会话空态说明「查过没有」而不是沉默', async ({ page }) => {
    await openView(page, 'sessions', pid);
    const api = await apiJson(page, `/api/v1/projects/${pid}/sessions?hours=48`);
    const text = (await page.locator('#vnext-view').innerText()).replace(/\s+/g, ' ');
    if (!(api.body?.items || []).length) {
      expect(text, '空会话列表没有任何解释').toMatch(/no observable|暂无|还没有/i);
      expect(text).toMatch(/complete|完整|completeness/i);
    }
  });
});

test.describe('F 组：空库冷启动（浏览器）', () => {
  test.beforeEach(() => {
    test.skip(!EMPTY, '未设置 E2E_EMPTY_BASE，跳过空库用例');
  });

  test('F-01 空库首页不白屏且能进各视图', async ({ page }) => {
    await open(page, '/', EMPTY);
    await expect(page.locator('#app-shell-aside')).toBeVisible();
    await expect(page.locator('#page-h1')).not.toBeEmpty();
    for (const view of ['nodes', 'tasks', 'runs', 'deliveries', 'projects', 'settings', 'timeline', 'activity', 'sessions', 'analytics']) {
      await open(page, `/?view=${view}`, EMPTY);
      await page.waitForTimeout(250);
      const body = (await page.locator('.content').innerText()).replace(/\s+/g, ' ');
      expect(body.length, `空库 ${view} 视图空白`).toBeGreaterThan(30);
    }
  });

  test('F-02 空库不提供工程下拉选项、看板给出空态', async ({ page }) => {
    await open(page, '/?view=board', EMPTY);
    await expect(page.locator('.card')).toHaveCount(0);
    const body = (await page.locator('.content').innerText()).replace(/\s+/g, ' ');
    expect(body.length).toBeGreaterThan(30);
    const stats = page.locator('#stats');
    if (await stats.count()) {
      await expect(stats).toContainText('0');
    }
  });
});
