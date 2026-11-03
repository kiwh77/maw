// VN-741 分层执行时间轴的真实浏览器验收（UI-C）。
//
// 验证图例分色、未知不补满、小时窗口控件、键盘聚焦、中英深浅主题与窄屏。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-741'

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
const loadView = () => import(pathToFileURL(path.join(VIEW_DIR, "timeline-view.mjs")).href);

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const parts = [
    fs.readFileSync(path.join(ASSETS, "styles/tokens.css"), "utf8"),
    ...COMPONENTS.map(f => fs.readFileSync(path.join(ASSETS, "components", f), "utf8")),
    fs.readFileSync(path.join(ASSETS, "components/components.css"), "utf8").replace(/@import\s+"[^"]+";/g, ""),
    fs.readFileSync(path.join(VIEW_DIR, "timeline.css"), "utf8"),
  ];
  return strip(parts.join("\n"));
}

const intervals = () => ([
  {
    interval_id: "iv_phase",
    kind: "task_phase",
    phase: "in_progress",
    instance_id: "ti_a",
    run_id: "run_1",
    start_at: "2026-10-23T01:00:00Z",
    end_at: "2026-10-23T03:00:00Z",
    quality: "observed",
  },
  {
    interval_id: "iv_cli",
    kind: "cli_process",
    instance_id: "ti_a",
    run_id: "run_1",
    start_at: "2026-10-23T01:30:00Z",
    end_at: "2026-10-23T02:00:00Z",
    quality: "observed",
  },
  {
    interval_id: "iv_wait",
    kind: "explicit_wait",
    reason: "blocked",
    instance_id: "ti_a",
    run_id: "run_1",
    start_at: "2026-10-23T02:10:00Z",
    end_at: "2026-10-23T02:40:00Z",
    quality: "observed",
  },
  {
    interval_id: "iv_open",
    kind: "turn",
    instance_id: "ti_a",
    run_id: "run_2",
    start_at: "2026-10-23T02:50:00Z",
    open: true,
    quality: "unknown",
  },
]);

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
const LIGHT_WARN = rgb("#8a601f");
const DARK_ACCENT = rgb("#9cdbb0");

test.describe("VN-741 分层执行时间轴", () => {
  test("VN-741 四类图例分开，未知条不铺满", async ({ page }) => {
    const view = await loadView();
    const ctx = view.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" });
    await mount(page, view.renderTimeline(
      { intervals: intervals(), cursors: { state_cursor: 12, projection_cursor: 10, completeness: "partial" } },
      { lang: "zh", dateContext: ctx, selectedId: "iv_phase", showDetail: true }
    ));

    await expect(page.locator(".timeline__legend-item")).toHaveCount(5);
    await expect(page.locator("body")).toContainText("阶段驻留");
    await expect(page.locator("body")).toContainText("CLI 运行");
    await expect(page.locator("body")).toContainText("明确等待");
    await expect(page.locator('[data-interval-id="iv_phase"]')).toHaveCSS("background-color", DARK_WARN);
    await expect(page.locator('[data-interval-id="iv_cli"]')).toHaveCSS("background-color", DARK_ACCENT);

    const openWidth = await page.locator('[data-interval-id="iv_open"]').evaluate(
      el => parseFloat(el.style.width)
    );
    expect(openWidth).toBeLessThan(40);

    await expect(page.locator(".timeline__detail")).toContainText("区间详情");
    await expect(page.locator("body")).toContainText("状态水位");
    await expect(page.locator("body")).toContainText("投影水位");
  });

  test("VN-741 浅色主题换用另一套 token", async ({ page }) => {
    const view = await loadView();
    const ctx = view.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" });
    await mount(page, view.renderTimeline(
      { intervals: intervals() },
      { lang: "zh", dateContext: ctx, selectedId: "iv_phase" }
    ), "light");
    await expect(page.locator('[data-interval-id="iv_phase"]')).toHaveCSS("background-color", LIGHT_WARN);
  });

  test("VN-741 英文界面不残留中文且无页面级横向滚动", async ({ page }) => {
    const view = await loadView();
    const ctx = view.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" });
    await mount(page, view.renderTimeline(
      { intervals: intervals() },
      { lang: "en", dateContext: ctx }
    ));
    await expect(page.locator(".timeline__toolbar")).toBeVisible();
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    expect(await page.locator("body").innerText()).not.toMatch(/[\u4e00-\u9fa5]/);
  });

  test("VN-741 700px 窄屏：页面不横向滚动，工具栏可换行", async ({ page }) => {
    const view = await loadView();
    await page.setViewportSize({ width: 700, height: 900 });
    const ctx = view.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 6, timezone: "UTC" });
    await mount(page, view.renderTimeline({ intervals: intervals() }, { lang: "zh", dateContext: ctx }));
    const state = await page.evaluate(() => ({
      doc: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      toolbar: getComputedStyle(document.querySelector(".timeline__toolbar")).flexWrap,
    }));
    expect(state.doc).toBeLessThanOrEqual(0);
    expect(state.toolbar).toBe("wrap");
  });

  test("VN-741 上下键切换焦点并可打开详情，左右键平移窗口", async ({ page }) => {
    const view = await loadView();
    const ctx = view.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" });
    let selectedId = "iv_phase";
    // 上下键：移动选中（不改变窗口）
    const next = view.applyKeyToState(
      { intervals: intervals() },
      { dateContext: ctx, selectedId, showDetail: false },
      "ArrowDown"
    );
    selectedId = next.selectedId;
    expect(next.window, "上下键不应平移窗口").toBeNull();
    await mount(page, view.renderTimeline(
      { intervals: intervals() },
      { lang: "zh", dateContext: ctx, selectedId, showDetail: true }
    ));
    await expect(page.locator(".timeline")).toHaveAttribute("data-selected", selectedId);
    await expect(page.locator(".timeline__detail")).toBeVisible();
    await expect(page.locator(`[data-interval-id="${selectedId}"]`)).toHaveAttribute("tabindex", "0");

    // 左右键：平移窗口（等价工具栏「上一窗」「下一窗」）
    const moved = view.applyKeyToState(
      { intervals: intervals() },
      { dateContext: ctx, selectedId, showDetail: false },
      "ArrowRight"
    );
    expect(moved.window).toBe("next");
    expect(moved.dateContext.startMs).toBe(ctx.startMs + ctx.hours * 3600_000);
  });

  test("VN-741 空与错误状态使用 empty-state 组件", async ({ page }) => {
    const view = await loadView();
    const ctx = view.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 1, timezone: "UTC" });
    await mount(page, view.renderTimeline({ status: "error" }, { lang: "zh", dateContext: ctx }));
    await expect(page.locator(".empty-state")).toBeVisible();
    await expect(page.locator(".timeline")).toHaveAttribute("data-status", "error");

    await mount(page, view.renderTimeline({ intervals: [] }, { lang: "zh", dateContext: ctx }));
    await expect(page.locator(".timeline")).toHaveAttribute("data-status", "empty");
  });
  // 内容留白由外层 .vnext / .content 统一给：视图根元素自己再加一层，
  // 页面左右就会缩进两次、底部也会多一段空白。
  test("VN-741 视图根元素不再自带内边距", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderTimeline({ intervals: intervals() }, { lang: "zh", dateContext: view.createDateContext({ start: "2026-10-23T01:00:00Z", hours: 3, timezone: "UTC" }) }));
    const padding = await page.locator(".timeline").evaluate(el => {
      const cs = getComputedStyle(el);
      return [cs.paddingTop, cs.paddingRight, cs.paddingBottom, cs.paddingLeft];
    });
    expect(padding).toEqual(["0px", "0px", "0px", "0px"]);
  });
});
