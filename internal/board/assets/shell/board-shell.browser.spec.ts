// VN-704 统一布局挂载层的真实浏览器验收（UI-C）。
//
// 只验证本卡 Surface 内的产物：把构建出的 board-web.js 挂进页面，
// 用 MawBoard.mountShell 在统一布局里接入 nodes / tasks / runs / deliveries，
// 核对导航、路由、语言、详情返回、窄屏，以及 pending/unknown 不被呈现为成功。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-704'

import fs from "node:fs";
import path from "node:path";

// @playwright/test 装在 tests/browser/node_modules，本文件在 internal/** 下需要显式解析。
function loadPlaywright() {
  try {
    return require("@playwright/test");
  } catch {
    return require("../../../../tests/browser/node_modules/@playwright/test");
  }
}

const { test, expect } = loadPlaywright();

const ASSETS = path.resolve(__dirname, "..");
const BUNDLE = path.join(ASSETS, "dist/board-web.js");
const TOKENS = path.join(ASSETS, "styles/tokens.css");
const SHELL_CSS = path.join(ASSETS, "shell/shell.css");

const LAYOUT_CSS = `
  * { box-sizing: border-box; }
  [hidden] { display: none !important; }
  html, body { margin: 0; }
  .app { display: flex; min-height: 100vh; }
  .sidebar { width: 220px; flex: none; padding: 12px; border-right: 1px solid var(--line); }
  .workspace { flex: 1; min-width: 0; padding: 12px 16px; }
  .content { min-width: 0; }
  .project-nav { display: flex; flex-direction: column; gap: 2px; }
  .nav-item { display: flex; gap: 8px; align-items: center; width: 100%; border: 0; background: transparent;
    color: var(--text); padding: 6px 8px; text-align: left; cursor: pointer; }
  .nav-item.active { background: var(--hover); color: var(--accent); }
  .section-label { font: 600 10px/1.6 var(--mono, monospace); letter-spacing: .12em; text-transform: uppercase;
    color: var(--muted); padding: 6px 8px 2px; }
  .toolbar { display: flex; gap: 8px; flex-wrap: wrap; }
  .board { display: grid; gap: 12px; }
  .table { width: 100%; border-collapse: collapse; }
  .table th, .table td { padding: 6px 8px; text-align: left; border-bottom: 1px solid var(--line); }
`;

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  return strip([
    fs.readFileSync(TOKENS, "utf8"),
    fs.readFileSync(SHELL_CSS, "utf8"),
    LAYOUT_CSS,
  ].join("\n"));
}

const BASE_HTML = `<!DOCTYPE html>
<html data-theme="dark" lang="zh-CN">
<head><meta charset="utf-8"><title>workbench</title><style>__CSS__</style></head>
<body>
<div class="app" id="app-shell">
  <aside class="sidebar" id="app-shell-aside">
    <div class="section-label">Workspace</div>
    <nav class="project-nav" id="vnext-nav"></nav>
  </aside>
  <main class="workspace">
    <header class="topbar">
      <div class="breadcrumb"><span class="dim">Workspace</span><span class="slash">/</span><strong id="crumb-scope"></strong><span id="crumb-scope-slash" class="slash" hidden>/</span><strong id="workspace-name">All projects</strong></div>
      <select id="lang-select"><option value="en">EN</option><option value="zh">中</option></select>
    </header>
    <div class="content">
      <div class="page-heading"><div class="eyebrow">OPERATIONS / BOARD</div>
        <h1 id="page-h1">Task board</h1><p id="page-desc"></p></div>
      <section class="stats" id="stats"></section>
      <div class="toolbar">
        <div class="filters" id="filters">
          <select id="f-project"><option value="">All projects</option></select>
          <select id="f-agent"><option value="">All Agents</option></select>
          <select id="f-status"><option value="">All statuses</option></select>
        </div>
        <div class="toolbar-right">
          <div class="board-meta-left">
            <div class="view-toggle" id="view-toggle" role="group">
              <button type="button" id="view-board" data-view="board" class="active" aria-pressed="true">Board</button>
              <button type="button" id="view-factory" data-view="factory" aria-pressed="false">Workshop</button>
              <button type="button" id="view-topo" data-view="topo" aria-pressed="false">Topology</button>
            </div>
            <span id="board-count">Loading tasks…</span>
          </div>
          <label class="search-box"><input type="search" id="task-search"/></label>
        </div>
      </div>
      <div class="board" id="columns"><div class="lane">Ready</div></div>
      <div class="topo" id="topology" hidden></div>
      <section id="factory" hidden></section>
      <section class="vnext" id="vnext-view" hidden></section>
      <section class="vnext-detail" id="vnext-detail" role="region" hidden>
        <div class="vnext-detail__head">
          <button type="button" class="vnext-detail__back" id="vnext-detail-back"></button>
          <h2 class="vnext-detail__title" id="vnext-detail-title"></h2>
          <button type="button" class="vnext-detail__close" id="vnext-detail-close">×</button>
        </div>
        <div class="vnext-detail__body" id="vnext-detail-body"></div>
      </section>
      <div id="hint-center"></div>
    </div>
  </main>
</div>
</body>
</html>`;

const NODE_READY = {
  id: "node_a",
  name: "CodeBuddy local",
  adapter: "codebuddy",
  presence: { online: true, last_seen: new Date().toISOString() },
  auth: { state: "ready" },
  capability: {
    declared: ["non_interactive", "structured_events"],
    verified: ["non_interactive", "structured_events"],
    cli_version: "1.4.0",
    verified_version: "1.4.0",
    verified_at: new Date().toISOString(),
  },
  slots: { total: 1, busy: 0 },
  intake: "open",
};

const NODE_UNKNOWN = {
  id: "node_b",
  name: "Unprobed node",
  adapter: "trae",
  presence: undefined,
  auth: {},
  slots: {},
};

const RUN_UNKNOWN = {
  run_id: "run_1",
  generation: 1,
  phase: "running",
  reconciliation_state: "unknown",
  task: { status: "in_progress" },
  delivery: { state: "none" },
};

const TASK_UNKNOWN = {
  id: "VN-990",
  instance_id: "ti_unknown1",
  display_id: "VN-990",
  project_id: "p1",
  title: "Unknown-state task",
  status: "unknown",
  priority: 10,
  deps: [],
  write_scope: ["docs/**"],
};

const DELIVERY_PENDING = {
  delivery_id: "dlv_1",
  task_instance: "ti_unknown1",
  task_version: 1,
  mode: "code",
  artifacts: [],
  verifications: [],
  verification: { state: "pending" },
  merge: { merged: false },
};

async function mountWorkbench(page, { fixtures = {}, lang = "zh" } = {}) {
  await page.setContent(BASE_HTML.replace("__CSS__", loadCSS()));
  await page.addScriptTag({ path: BUNDLE });
  await page.evaluate(
    ({ fixtures, lang }) => {
      window.__shell = window.MawBoard.mountShell({ fixtures, lang });
    },
    { fixtures, lang }
  );
}

test.describe("VN-704 统一布局内接入受管闭环视图", () => {
  test("VN-704 导航登记四个工作台视图并能在统一布局内切换", async ({ page }) => {
    await mountWorkbench(page, {
      fixtures: { nodes: { nodes: [NODE_READY, NODE_UNKNOWN] } },
    });

    const nav = page.locator("#vnext-nav");
    await expect(nav).toBeVisible();
    // 侧栏不展示流程组（看板／工坊／拓朴改由 toolbar）。
    await expect(nav.locator("[data-view]")).toHaveCount(10);
    await expect(nav.locator(".section-label").first()).toHaveText("控制");
    await expect(nav.locator('[data-view="board"]')).toHaveCount(0);

    // 无工程默认落到节点；legacy 容器隐藏，vnext 可见。
    await expect(page.locator("#vnext-view")).toBeVisible();
    await expect(page.locator("#columns")).toBeHidden();
    await expect(page.locator("#page-h1")).toHaveText("执行节点");
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(2);

    await page.evaluate(() => (window as any).__shell.navigate("board"));
    await expect(page.locator("#columns")).toBeVisible();
    await expect(page.locator("#vnext-view")).toBeHidden();

    await nav.locator('[data-view="nodes"]').click();
    await expect(page.locator("#vnext-view")).toBeVisible();
    await expect(page.locator("#columns")).toBeHidden();
    await expect(page.locator("#topology")).toBeHidden();
    await expect(page.locator("#factory")).toBeHidden();
    await expect(page.locator("#page-h1")).toHaveText("执行节点");
    await expect(page.locator("#workspace-name")).toHaveText("节点");
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(2);

    // unknown 节点只出现一次「状态未知」，绝不被计成可接任务。
    await expect(page.locator("#vnext-view .badge").filter({ hasText: /^状态未知$/ })).toHaveCount(1);
    await expect(page.locator("#vnext-view .badge").filter({ hasText: /^可接任务$/ })).toHaveCount(1);
  });

  test("VN-704 全局语言切换同时改文档语言、导航与页面标题", async ({ page }) => {
    await mountWorkbench(page, {
      lang: "zh",
      fixtures: { nodes: { nodes: [NODE_READY] } },
    });
    await expect(page.locator("html")).toHaveAttribute("lang", "zh-CN");

    await page.selectOption("#lang-select", "en");
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator("#vnext-nav .section-label").first()).toHaveText("Control");

    await page.locator('#vnext-nav [data-view="nodes"]').click();
    await expect(page.locator("#page-h1")).toHaveText("Execution nodes");
    await expect(page.locator("#vnext-view")).not.toContainText("执行节点");

    // 浅色主题沿用同一套共享 token，不出现布局级横向溢出。
    await page.locator("html").evaluate(el => { el.dataset.theme = "light"; });
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test("VN-704 390px 窄屏导航可用且页面不横向滚动", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await mountWorkbench(page, {
      fixtures: { deliveries: { deliveries: [DELIVERY_PENDING] } },
    });
    await page.locator('#vnext-nav [data-view="deliveries"]').click();
    await expect(page.locator("#vnext-view")).toBeVisible();
    const state = await page.evaluate(() => ({
      doc: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      nav: getComputedStyle(document.querySelector("#vnext-nav")).display,
      wrap: document.querySelector("#vnext-view .table-wrap")
        ? getComputedStyle(document.querySelector("#vnext-view .table-wrap")).overflowX
        : "auto",
    }));
    expect(state.doc).toBeLessThanOrEqual(0);
    expect(state.nav).not.toBe("none");
    expect(state.wrap).toBe("auto");
  });

  test("VN-704 详情从行打开、关闭后回到列表且不清掉筛选", async ({ page }) => {
    await mountWorkbench(page, {
      fixtures: { runs: { runs: [RUN_UNKNOWN] } },
    });
    await page.locator('#vnext-nav [data-view="runs"]').click();
    await page.locator("#vnext-view tbody tr").first().click();

    const detail = page.locator("#vnext-detail");
    await expect(detail).toBeVisible();
    await expect(page.locator("#vnext-view")).toBeHidden();
    await expect(page.locator("#vnext-detail-title")).toHaveText("run_1");
    // 运行的对账未知必须显式写「待核实」，不能当作完成。
    await expect(page.locator("#vnext-detail-body")).toContainText("待核实");
    const detailRoute = await page.evaluate(() => window.__shell.route());
    expect(detailRoute.view).toBe("runs");
    expect(detailRoute.detail).toBe("run_1");

    await page.locator("#vnext-detail-back").click();
    await expect(detail).toBeHidden();
    await expect(page.locator("#vnext-view")).toBeVisible();
    const listRoute = await page.evaluate(() => window.__shell.route());
    expect(listRoute.detail).toBe("");
    expect(listRoute.view).toBe("runs");
  });

  test("VN-704 pending 与 unknown 不被呈现为成功", async ({ page }) => {
    await mountWorkbench(page, {
      fixtures: {
        tasks: { tasks: [TASK_UNKNOWN], allTasks: [TASK_UNKNOWN] },
        deliveries: { deliveries: [DELIVERY_PENDING] },
      },
    });

    await page.locator('#vnext-nav [data-view="tasks"]').click();
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(1);
    // 任务状态原样透传，不被翻译成 done/已合入。
    await expect(page.locator("#vnext-view .badge").filter({ hasText: /^done$/ })).toHaveCount(0);

    await page.locator('#vnext-nav [data-view="deliveries"]').click();
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(1);
    await expect(page.locator("#vnext-view .badge").filter({ hasText: /^已通过$/ })).toHaveCount(0);
    await expect(page.locator("#vnext-view .badge").filter({ hasText: /^已合入$/ })).toHaveCount(0);
  });

  test("VN-704 端点缺失时显式标为不可用而不是空成功", async ({ page }) => {
    await mountWorkbench(page, {
      fixtures: { nodes: { unavailable: true } },
    });
    await page.locator('#vnext-nav [data-view="nodes"]').click();
    await expect(page.locator("#vnext-view .vnext-status")).toContainText("未提供该数据");
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(0);
    await expect(page.locator("#vnext-view .badge").filter({ hasText: /^可接任务$/ })).toHaveCount(0);
  });

  test("VN-704 看板 chrome 按视图门控：settings/tasks 全藏、board 全显", async ({ page }) => {
    await mountWorkbench(page, {
      fixtures: {
        tasks: { tasks: [TASK_UNKNOWN], allTasks: [TASK_UNKNOWN] },
        settings: {
          ok: true,
          auto_execution: { enabled: false, managed_execution_limit: 2, config_version: 3, platform_supported: true },
        },
      },
    });

    const chromeHidden = async () => page.evaluate(() => ({
      stats: Boolean(document.getElementById("stats")?.hidden),
      filters: Boolean(document.getElementById("filters")?.hidden),
      toggle: Boolean(document.getElementById("view-toggle")?.hidden),
      count: Boolean(document.getElementById("board-count")?.hidden),
      search: Boolean(document.querySelector(".search-box")?.hidden),
      toolbar: Boolean(document.querySelector(".toolbar")?.hidden),
      columns: Boolean(document.getElementById("columns")?.hidden),
      vnext: Boolean(document.getElementById("vnext-view")?.hidden),
      boardActive: document.getElementById("view-board")?.classList.contains("active") === true,
    }));

    // 无工程默认落到 nodes；先切 board 再断言流程 chrome。
    await page.evaluate(() => (window as any).__shell.navigate("board"));
    expect(await chromeHidden()).toEqual({
      stats: false, filters: false, toggle: false, count: false,
      search: false, toolbar: false, columns: false, vnext: true, boardActive: true,
    });

    await page.evaluate(() => (window as any).__shell.navigate("tasks"));
    expect(await chromeHidden()).toEqual({
      stats: true, filters: true, toggle: true, count: true,
      search: true, toolbar: true, columns: true, vnext: false, boardActive: false,
    });

    await page.evaluate(() => (window as any).__shell.navigate("settings"));
    expect(await chromeHidden()).toEqual({
      stats: true, filters: true, toggle: true, count: true,
      search: true, toolbar: true, columns: true, vnext: false, boardActive: false,
    });

    await page.evaluate(() => (window as any).__shell.navigate("board"));
    expect(await chromeHidden()).toEqual({
      stats: false, filters: false, toggle: false, count: false,
      search: false, toolbar: false, columns: false, vnext: true, boardActive: true,
    });
  });
});

// ---------- VN-705：回顾统计与设置入口 ----------
//
// 本组用例不依赖构建产物：把 assets 下的源模块按需从磁盘喂给浏览器，
// 页面仍是生产 page.html（由 tests/browser 基座提供），因此验证的是
// 「shell 源模块挂进真实页面」这条路径，而不是打包后的 bundle。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-705'

const ASSETS_DIR = path.resolve(__dirname, "..");
const SERVED_PREFIXES = [
  "/assets/shell/",
  "/assets/i18n/",
  "/assets/views/",
  "/assets/styles/",
  "/assets/components/",
];

function assetContentType(file: string) {
  if (file.endsWith(".mjs") || file.endsWith(".js")) return "text/javascript; charset=utf-8";
  if (file.endsWith(".css")) return "text/css; charset=utf-8";
  return "text/plain; charset=utf-8";
}

async function serveSourceModules(page) {
  await page.route("**/*", async route => {
    const pathname = new URL(route.request().url()).pathname;
    if (!SERVED_PREFIXES.some(prefix => pathname.startsWith(prefix))) return route.continue();
    const file = path.join(ASSETS_DIR, pathname.replace(/^\/assets\//, ""));
    if (!fs.existsSync(file)) return route.fulfill({ status: 404, body: "missing" });
    return route.fulfill({ status: 200, contentType: assetContentType(file), body: fs.readFileSync(file) });
  });
}

type MountSpec = {
  path?: string;
  lang?: string;
  readOnly?: boolean;
  payloads?: Record<string, unknown>;
  capabilities?: Record<string, unknown> | null;
};

async function mountFromSource(page, spec: MountSpec = {}) {
  // 生产入口已是 Vue 整页；本套用例验证 board-shell 源模块，需独占 DOM。
  // 先落到 harness 同源，再 setContent 换成静态骨架，避免 Vue 抢写 #vnext-nav。
  await serveSourceModules(page);
  await page.goto("/");
  await page.setContent(BASE_HTML.replace("__CSS__", loadCSS()), { waitUntil: "domcontentloaded" });
  await page.evaluate(path => {
    history.replaceState({}, "", path || "/");
  }, spec.path || "/");
  await page.evaluate(async input => {
    const mod = await import("/assets/shell/board-shell.mjs");
    const calls: string[] = [];
    (window as any).__dataCalls = calls;
    const sources: Record<string, any> = {};
    Object.entries(input.payloads || {}).forEach(([kind, payload]) => {
      sources[kind] = async () => { calls.push(kind); return payload; };
    });
    sources.capabilities = async () => input.capabilities ?? null;
    (window as any).__shell = mod.mountShell({
      sources,
      lang: input.lang || "zh",
      readOnly: input.readOnly,
    });
    // 视图加载是异步的：等两轮宏任务再交给断言。
    await new Promise(resolve => setTimeout(resolve, 30));
  }, spec as any);
}

const TIMELINE_PARTIAL = {
  ok: true,
  status: "partial",
  completeness: "partial",
  capability: { name: "timeline", supported: true },
  items: [
    { interval_id: "iv_run_1", kind: "cli_process", run_id: "run_1", start_at: "2026-10-24T01:00:00Z", end_at: "2026-10-24T01:40:00Z", quality: "observed" },
    { interval_id: "iv_wait_1", kind: "explicit_wait", run_id: "run_1", start_at: "2026-10-24T02:00:00Z", end_at: null, quality: "observed" },
  ],
  window: { start: "2026-10-24T00:00:00Z", end: "2026-10-24T06:00:00Z", timezone: "Asia/Shanghai", hours: 6 },
  watermark: { cursor: 12, complete_before: 11, received_at: "2026-10-24T06:05:00Z", gaps: [{ from: 11, to: 12, reason: "ingest_lag" }] },
};

const ANALYTICS_READY = {
  ok: true,
  status: "ready",
  completeness: "complete",
  capability: { name: "stats", supported: true },
  report: {
    scope: { project_id: "p-harness" },
    completeness: "complete",
    deliveries: { count: 3, superseded: 0, by_mode: {} },
    usage: { turn_count: 5, tokens: { total_tokens: 1200, input_tokens: 900, output_tokens: 300 }, coverage: { observed_turns: 5, observed_denom: 5, ratio: 1 } },
    timeline: { engineering_seconds: 5400, cli_process_seconds: 4800, explicit_wait_seconds: 600 },
    by_source: [{ source_ns: "codebuddy", turn_count: 5 }],
    by_day: [{ day: "2026-10-24", delivery_count: 3, turn_count: 5 }],
  },
};

const SETTINGS_POLICY = {
  ok: true,
  auto_execution: { enabled: false, managed_execution_limit: 2, config_version: 3, platform_supported: true },
};

test.describe("VN-705 挂载回顾统计与设置入口", () => {
  test("VN-705 导航登记回顾／统计／设置，切页保留工程筛选与面包屑", async ({ page }) => {
    await mountFromSource(page, {
      path: "/?view=timeline&project=p-harness",
      payloads: { timeline: TIMELINE_PARTIAL, analytics: ANALYTICS_READY },
    });

    const nav = page.locator("#vnext-nav");
    await expect(nav.locator("[data-view]")).toHaveCount(10);
    await expect(nav.locator(".section-label").nth(0)).toHaveText("控制");
    await expect(nav.locator(".section-label").nth(1)).toHaveText("回顾");
    await expect(nav.locator(".section-label").nth(2)).toHaveText("设置");
    await expect(nav.locator('[data-view="board"]')).toHaveCount(0);

    // 直达链接恢复上下文：分组进面包屑，工程进标题位。
    await expect(page.locator("#crumb-scope")).toHaveText("回顾");
    await expect(page.locator("#workspace-name")).toHaveText("p-harness");
    await expect(page.locator("#page-h1")).toHaveText("执行时间轴");
    await expect(page.locator("#vnext-view .timeline__bar")).toHaveCount(2);

    // 切到统计：筛选不走丢，地址仍带着工程。
    await nav.locator('[data-view="analytics"]').click();
    await expect(page.locator("#page-h1")).toHaveText("交付与用量统计");
    await expect(page.locator("#crumb-scope")).toHaveText("回顾");
    await expect(page.locator("#workspace-name")).toHaveText("p-harness");
    const url = new URL(page.url());
    expect(url.searchParams.get("view")).toBe("analytics");
    expect(url.searchParams.get("project")).toBe("p-harness");
    expect(url.hash).toBe("#p-harness");
    const route = await page.evaluate(() => (window as any).__shell.route());
    expect(route.view).toBe("analytics");
    expect(route.project).toBe("p-harness");
  });

  test("VN-705 未选工程时给出「先选工程」而不是空表", async ({ page }) => {
    await mountFromSource(page, { path: "/?view=activity", payloads: {} });

    const notice = page.locator('#vnext-view .vnext-notice[data-state="no_project"]');
    await expect(notice).toBeVisible();
    await expect(notice).toContainText("先选择一个工程");
    await expect(page.locator("#vnext-view table")).toHaveCount(0);
    // 一个数据请求都不该发出去。
    expect(await page.evaluate(() => (window as any).__dataCalls)).toEqual([]);
  });

  test("VN-705 来源能力缺失时降级文案与空态一致", async ({ page }) => {
    await mountFromSource(page, {
      path: "/?view=analytics&project=p-harness",
      payloads: { analytics: ANALYTICS_READY },
      capabilities: { stats: { supported: false, reason: "source_not_configured" } },
    });

    await expect(page.locator("#vnext-view .vnext-status")).toContainText("当前中枢版本未提供该数据");
    await expect(page.locator("#vnext-view .vnext-status")).toContainText("缺失的数据不会被标成完成");
    await expect(page.locator("#vnext-view table")).toHaveCount(0);
    // 能力已知不支持，就不做无望请求。
    expect(await page.evaluate(() => (window as any).__dataCalls)).not.toContain("analytics");
  });

  test("VN-705 回顾视图标出只读与覆盖不全", async ({ page }) => {
    await mountFromSource(page, {
      path: "/?view=timeline&project=p-harness",
      payloads: { timeline: TIMELINE_PARTIAL },
    });

    await expect(page.locator('#vnext-view [data-readonly-notice="timeline"]')).toContainText("只读");
    // 覆盖不全只是横幅：视图自己把状态标成 unknown（partial 也照此），
    // 但已观测的区间仍然照常画出来——不是失败，也不补满。
    await expect(page.locator("#vnext-view .timeline")).toHaveAttribute("data-status", "unknown");
    await expect(page.locator("#vnext-view .timeline .empty-state")).toContainText("不完整");
    await expect(page.locator("#vnext-view .timeline__bar")).toHaveCount(2);
    // 未闭合区间按「未知／未闭合」呈现，不补成完整时长。
    expect(await page.locator("#vnext-view .timeline__bar--unknown").count()).toBeGreaterThan(0);
    await expect(page.locator("#vnext-view .timeline__cursors")).toContainText("12");
  });

  test("VN-705 静态导出下设置页只读：保留数值、禁用保存并说明原因", async ({ page }) => {
    await mountFromSource(page, {
      path: "/?view=settings",
      readOnly: true,
      payloads: { settings: SETTINGS_POLICY },
    });

    await expect(page.locator("#vnext-view form[data-readonly=\"1\"]")).toHaveCount(1);
    const submit = page.locator('#vnext-view form button[type="submit"]');
    await expect(submit).toBeDisabled();
    await expect(page.locator('#vnext-view [data-readonly-notice="settings"]')).toContainText("没有开放策略写入入口");
    // 策略值照常显示：只读不改内容（数值在输入框里，用 value 断言）。
    await expect(page.locator("#vnext-view #settings-limit")).toHaveValue("2");
    await expect(page.locator("#vnext-view #settings-limit")).toBeEditable();
    expect(await page.evaluate(() => (window as any).__shell.readOnly())).toBe(true);
  });

  test("VN-705 有写入口时设置页不被降级成只读", async ({ page }) => {
    await mountFromSource(page, {
      path: "/?view=settings",
      readOnly: false,
      payloads: { settings: SETTINGS_POLICY },
    });

    await expect(page.locator("#vnext-view form").first()).toBeVisible();
    await expect(page.locator("#vnext-view form button[type=\"submit\"]")).toBeEnabled();
    await expect(page.locator('#vnext-view [data-readonly-notice="settings"]')).toHaveCount(0);
    // 设置组不是筛选目标：看板 chrome 必须整体隐藏。
    const chrome = await page.evaluate(() => ({
      stats: Boolean(document.getElementById("stats")?.hidden),
      filters: Boolean(document.getElementById("filters")?.hidden),
      toggle: Boolean(document.getElementById("view-toggle")?.hidden),
      toolbar: Boolean(document.querySelector(".toolbar")?.hidden),
    }));
    expect(chrome).toEqual({ stats: true, filters: true, toggle: true, toolbar: true });
  });

  test("VN-705 control 视图隐藏工程筛选头；回 board 全显", async ({ page }) => {
    await mountFromSource(page, {
      path: "/?view=tasks&project=p-harness",
      payloads: { tasks: { tasks: [], allTasks: [] } },
    });

    const chromeHidden = async () => page.evaluate(() => ({
      stats: Boolean(document.getElementById("stats")?.hidden),
      filters: Boolean(document.getElementById("filters")?.hidden),
      toggle: Boolean(document.getElementById("view-toggle")?.hidden),
      search: Boolean(document.querySelector(".search-box")?.hidden),
      toolbar: Boolean(document.querySelector(".toolbar")?.hidden),
      vnext: Boolean(document.getElementById("vnext-view")?.hidden),
    }));

    expect(await chromeHidden()).toEqual({
      stats: true, filters: true, toggle: true, search: true, toolbar: true, vnext: false,
    });

    await page.evaluate(() => (window as any).__shell.navigate("board"));
    expect(await chromeHidden()).toEqual({
      stats: false, filters: false, toggle: false, search: false, toolbar: false, vnext: true,
    });
  });

  test("VN-705 打开节点详情时列表隐藏、详情占满内容区", async ({ page }) => {
    await mountFromSource(page, {
      path: "/?view=nodes&project=p-harness",
      payloads: {
        nodes: {
          nodes: [{
            id: "node_a",
            name: "CodeBuddy local",
            adapter: "codebuddy",
            presence: { online: true, last_seen: new Date().toISOString() },
            auth: { state: "ready" },
            capability: {
              declared: ["non_interactive"],
              verified: ["non_interactive"],
              cli_version: "1.4.0",
              verified_version: "1.4.0",
              verified_at: new Date().toISOString(),
            },
            slots: { total: 1, busy: 0 },
            intake: "open",
          }],
        },
      },
    });

    await page.locator("#vnext-view tbody tr").first().click();
    await expect(page.locator("#vnext-detail")).toBeVisible();
    await expect(page.locator("#vnext-view")).toBeHidden();
    await expect(page.locator("#filters")).toBeHidden();

    await page.locator("#vnext-detail-back").click();
    await expect(page.locator("#vnext-detail")).toBeHidden();
    await expect(page.locator("#vnext-view")).toBeVisible();
  });

  test("VN-705 工程清单说明快照缺策略，不把每行读成关闭", async ({ page }) => {
    await mountFromSource(page, {
      path: "/?view=projects",
      payloads: { projects: { projects: [{ id: "p-harness", name: "Harness", max_parallel: 2 }] } },
    });

    await expect(page.locator("#vnext-view [data-policy-missing]")).toContainText("没有带各工程的执行策略");
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(1);
  });

  test("VN-705 390px 窄屏下回顾页不横向溢出，地址上下文仍可恢复", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await mountFromSource(page, {
      path: "/?view=timeline&project=p-harness",
      payloads: { timeline: TIMELINE_PARTIAL },
    });

    // 注：≤700px 时 legacy 样式表 `[.sidebar]{display:none}` 会隐藏整个侧栏，
    // 导航因此在该断点不可见。这属于旧布局（internal/board/assets/legacy/board.css），
    // 不在本卡写盘范围内，已作为视觉一致性问题交给 VN-984；这里只验证本卡产出的
    // 内容本身不溢出、且上下文仍能从地址恢复。
    await expect(page.locator("#vnext-view .timeline")).toBeVisible();
    await expect(page.locator("#workspace-name")).toHaveText("p-harness");
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });
});
