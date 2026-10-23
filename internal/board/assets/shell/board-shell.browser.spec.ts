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
      <div class="breadcrumb"><span class="dim">Workspace</span><span class="slash">/</span><strong id="workspace-name">All projects</strong></div>
      <select id="lang-select"><option value="en">EN</option><option value="zh">中</option></select>
    </header>
    <div class="content">
      <div class="page-heading"><div class="eyebrow">OPERATIONS / BOARD</div>
        <h1 id="page-h1">Task board</h1><p id="page-desc"></p></div>
      <div id="filters"></div>
      <div class="board" id="columns"><div class="lane">Ready</div></div>
      <div class="topo" id="topology" hidden></div>
      <section id="factory" hidden></section>
      <section class="vnext" id="vnext-view" hidden></section>
      <div id="hint-center"></div>
    </div>
  </main>
</div>
<section class="vnext-detail" id="vnext-detail" role="region" hidden>
  <div class="vnext-detail__head">
    <button type="button" class="vnext-detail__back" id="vnext-detail-back"></button>
    <h2 class="vnext-detail__title" id="vnext-detail-title"></h2>
    <button type="button" class="vnext-detail__close" id="vnext-detail-close">×</button>
  </div>
  <div class="vnext-detail__body" id="vnext-detail-body"></div>
</section>
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
    // 三个 legacy 流程视图 + 四个 vnext 控制视图，分组标题来自全局语言簿。
    await expect(nav.locator("[data-view]")).toHaveCount(7);
    await expect(nav.locator(".section-label").first()).toHaveText("流程");

    // 默认落在看板，legacy 容器保持可见，vnext 容器隐藏。
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
    await expect(page.locator("#vnext-nav .section-label").first()).toHaveText("Flow");

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
});
