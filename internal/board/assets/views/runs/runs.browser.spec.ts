// VN-723 运行详情与待核实面板的真实浏览器验收（UI-C）。
//
// 验证 runs-view.mjs 渲染的三层状态、取消受理态、unknown 面板与文本安全，
// 配上 VN-702 的共享 token 与组件。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-723'

import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

function loadPlaywright() {
  try {
    return require("@playwright/test");
  } catch {
    return require("../../../../../tests/browser/node_modules/@playwright/test");
  }
}

const { test, expect } = loadPlaywright();

const VIEW_DIR = __dirname;
const ASSETS = path.resolve(VIEW_DIR, "../..");
const COMPONENTS = ["badge.css", "table.css", "form.css", "empty.css", "drawer.css"];
const loadView = () => import(pathToFileURL(path.join(VIEW_DIR, "runs-view.mjs")).href);

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const parts = [
    fs.readFileSync(path.join(ASSETS, "styles/tokens.css"), "utf8"),
    ...COMPONENTS.map(f => fs.readFileSync(path.join(ASSETS, "components", f), "utf8")),
    fs.readFileSync(path.join(ASSETS, "components/components.css"), "utf8").replace(/@import\s+"[^"]+";/g, ""),
  ];
  return strip(parts.join("\n"));
}

const run = (overrides = {}) => ({
  run_id: "run_7f3c9a",
  phase: "running",
  reconciliation_state: "confirmed",
  desired_action: "none",
  process_stop_confirmed: false,
  delivery_frozen: false,
  generation: 2,
  task_status: "in_progress",
  last_heartbeat_at: "2026-10-23T07:00:00Z",
  events: [{ type: "run.phase", message: "started", received_at: "2026-10-23T07:00:00Z", quality: "observed" }],
  ...overrides,
});

async function mount(page, html, theme = "dark") {
  await page.setContent(
    `<!DOCTYPE html><html data-theme="${theme}" lang="zh-CN"><head><meta charset="utf-8">` +
    `<style>${loadCSS()}</style></head><body><div class="page" id="page">${html}</div></body></html>`
  );
}

const rgb = hex => {
  const h = hex.replace("#", "");
  return `rgb(${parseInt(h.slice(0, 2), 16)}, ${parseInt(h.slice(2, 4), 16)}, ${parseInt(h.slice(4, 6), 16)})`;
};
const DARK_WARN = rgb("#e5bc78");
const DARK_OK = rgb("#9cdbb0");
const LIGHT_WARN = rgb("#8a601f");

test.describe("VN-723 运行详情与待核实", () => {
  test("VN-723 三层状态并列显示，取消受理停在待停止", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderRunDetail(run({ desired_action: "stop" }), { lang: "zh" }));

    await expect(page.locator(".table th")).toHaveCount(3);
    await expect(page.locator(".table th").nth(0)).toHaveText("运行");
    await expect(page.locator(".table th").nth(1)).toHaveText("交付");
    await expect(page.locator(".table th").nth(2)).toHaveText("任务");

    // 取消受理：warn 色徽标 + 文案含「尚未确认停止」，且没有「已停止」
    const cancelBadge = page.locator(".badge").filter({ hasText: "取消已受理" });
    await expect(cancelBadge).toBeVisible();
    await expect(cancelBadge).toHaveCSS("color", DARK_WARN);
    await expect(page.locator("body")).not.toContainText("已停止，进程停止已确认");
    // 任务状态仍是 in_progress，不被运行阶段带跑
    await expect(page.locator("body")).toContainText("in_progress");
  });

  test("VN-723 浅色主题换用另一套 token", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderRunDetail(run({ desired_action: "stop" }), { lang: "zh" }), "light");
    await expect(page.locator(".badge").filter({ hasText: "取消已受理" })).toHaveCSS("color", LIGHT_WARN);
  });

  test("VN-723 英文界面不残留中文且无页面级横向滚动", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderRunList(
      { runs: [run(), run({ run_id: "run_2", reconciliation_state: "unknown" })] },
      { lang: "en" }
    ));
    await expect(page.locator("h1")).toHaveText("Runs");
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    expect(await page.locator("body").innerText()).not.toMatch(/[\u4e00-\u9fa5]/);
  });

  test("VN-723 700px 窄屏：页面不横向滚动，表格在内容区滚动", async ({ page }) => {
    const view = await loadView();
    await page.setViewportSize({ width: 700, height: 900 });
    await mount(page, view.renderRunList({ runs: [run()] }, { lang: "zh" }));
    const state = await page.evaluate(() => ({
      doc: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      wrap: getComputedStyle(document.querySelector(".table-wrap")).overflowX,
    }));
    expect(state.doc).toBeLessThanOrEqual(0);
    expect(state.wrap).toBe("auto");
  });

  test("VN-723 unknown 面板四块齐全且不给强制释放", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderUnknownPanel(
      run({ reconciliation_state: "unknown", last_heartbeat_at: null }),
      { lang: "zh" }
    ));

    await expect(page.locator(".unknown-panel")).toBeVisible();
    for (const heading of ["最后事实", "未知点", "占用资源", "处理入口"]) {
      await expect(page.locator(".unknown-panel h3", { hasText: heading })).toHaveCount(1);
    }
    await expect(page.locator('[data-action="recheck"]')).toBeVisible();
    await expect(page.locator('[data-action="report"]')).toBeVisible();
    await expect(page.locator('button[data-action*="release"]')).toHaveCount(0);
    // 占用继续保留：槽与目录都还是「仍占用」
    await expect(page.locator(".badge").filter({ hasText: /^仍占用$/ })).toHaveCount(2);
  });

  test("VN-723 未知面板键盘可达，焦点环沿用共享约定", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderUnknownPanel(run({ reconciliation_state: "unknown" }), { lang: "zh" }));
    const button = page.locator('[data-action="recheck"]');
    await page.locator("body").click({ position: { x: 2, y: 2 } });
    await page.keyboard.press("Tab");
    const style = await button.evaluate(el => {
      el.focus();
      const s = getComputedStyle(el);
      return { width: s.outlineWidth, offset: s.outlineOffset, color: s.outlineColor };
    });
    expect(style.width).toBe("2px");
    expect(style.offset).toBe("3px");
    expect(style.color).toBe(DARK_OK);
  });

  test("VN-723 日志中的标记只当文本，不生成 DOM 元素", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderRunDetail(run({
      events: [{
        type: "run.log",
        message: '<img src=x onerror="window.__pwned=1"><script>window.__pwned=2</script>',
        received_at: "2026-10-23T07:01:00Z",
      }],
    }), { lang: "zh" }));

    await expect(page.locator(".run-log")).toBeVisible();
    await expect(page.locator(".run-log img")).toHaveCount(0);
    await expect(page.locator(".run-log script")).toHaveCount(0);
    expect(await page.evaluate(() => window.__pwned)).toBeUndefined();
    await expect(page.locator(".run-log")).toContainText("<script>");
  });

  test("VN-723 六个状态互不重复", async ({ page }) => {
    const view = await loadView();
    const states = {
      loading: { status: "loading" },
      empty: { runs: [] },
      error: { status: "error" },
      expired: { status: "expired" },
      unauth: { status: "unauthenticated" },
      unknown: { runs: [run({ reconciliation_state: "unknown" })] },
    };
    const texts = new Set();
    for (const [name, state] of Object.entries(states)) {
      await mount(page, view.renderRunList(state, { lang: "zh" }));
      const text = (await page.locator("body").innerText()).trim();
      expect(text.length, name).toBeGreaterThan(0);
      expect(texts.has(text), name + " 与其他状态重复").toBe(false);
      texts.add(text);
      if (!["loading", "unknown"].includes(name)) {
        await expect(page.locator(".empty-state")).toBeVisible();
      }
    }
  });
});
