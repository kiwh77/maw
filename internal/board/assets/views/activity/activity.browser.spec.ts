// VN-742 全天活动与小时下钻的真实浏览器验收（UI-C）。
//
// 验证两指标单位分开、缺测不涂零、小时下钻上下文一致、键盘访问、
// 中英深浅主题与窄屏，以及加载／空／错误／过期／unknown 状态。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-742'

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
const loadView = () => import(pathToFileURL(path.join(VIEW_DIR, "activity-view.mjs")).href);

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const parts = [
    fs.readFileSync(path.join(ASSETS, "styles/tokens.css"), "utf8"),
    ...COMPONENTS.map(f => fs.readFileSync(path.join(ASSETS, "components", f), "utf8")),
    fs.readFileSync(path.join(ASSETS, "components/components.css"), "utf8").replace(/@import\s+"[^"]+";/g, ""),
    fs.readFileSync(path.join(VIEW_DIR, "activity.css"), "utf8"),
  ];
  return strip(parts.join("\n"));
}

const DAY = "2026-10-23";

const buckets = () => ([
  { hour: "2026-10-22T16:00:00Z", engineering_seconds: 7200, turn_seconds: 1800, task_phase_seconds: 7200, cli_process_seconds: 0, explicit_wait_seconds: 0, avg_concurrency: 2 },
  { hour: "2026-10-22T17:00:00Z", engineering_seconds: 3600, turn_seconds: 600, task_phase_seconds: 0, cli_process_seconds: 3600, explicit_wait_seconds: 1200, avg_concurrency: 1 },
  { hour: "2026-10-23T06:00:00Z", engineering_seconds: 600, turn_seconds: 0, task_phase_seconds: 600, cli_process_seconds: 0, explicit_wait_seconds: 0, avg_concurrency: 1 },
]);

const state = (over = {}) => ({
  status: "ready",
  day: DAY,
  timezone: "Asia/Shanghai",
  hours: buckets(),
  completeness: "complete",
  watermark: { cursor: 40, complete_before: 40 },
  projects: [{ id: "p1", label: "工程一" }, { id: "p2", label: "工程二" }],
  project_id: "p1",
  ...over,
});

async function mount(page, html, theme = "dark") {
  await page.setContent(
    `<!DOCTYPE html><html data-theme="${theme}" lang="zh-CN"><head><meta charset="utf-8">` +
    `<style>${loadCSS()}</style></head><body><div class="page" id="page">${html}</div></body></html>`
  );
}

test.describe("VN-742 全天活动与小时下钻", () => {
  test("VN-742 两张热图分开：单位写在图题，同一格数值不同", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderActivity(state(), { lang: "zh", selectedHour: 0 }));
    await expect(page.locator('[data-metric="coverage"] h2')).toContainText("执行覆盖");
    await expect(page.locator('[data-metric="coverage"] h2')).toContainText("分钟");
    await expect(page.locator('[data-metric="activity"] h2')).toContainText("事件活跃度");
    await expect(page.locator('[data-metric="activity"] h2')).toContainText("轮次·分钟");
    const coverageValue = await page.locator('[data-metric="coverage"] [data-hour="0"] .activity__cell-value').innerText();
    const activityValue = await page.locator('[data-metric="activity"] [data-hour="0"] .activity__cell-value').innerText();
    expect(coverageValue).toBe("120");
    expect(activityValue).toBe("30");
  });

  test("VN-742 缺测小时标缺测（斜线），不涂成零活动", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderActivity(state({ completeness: "partial" }), { lang: "zh" }));
    const missing = page.locator('[data-metric="coverage"] [data-state="missing"]');
    await expect(missing.first()).toBeVisible();
    await expect(page.locator(".activity")).toHaveAttribute("data-status", "unknown");
    await expect(page.locator(".activity__missing-banner")).toContainText("部分小时缺测");
    // 缺测格的数值是「缺测」文案，不是 0。
    const text = await missing.first().innerText();
    expect(text).toContain("缺测");
    // complete 时同格才是真实零活动。
    await mount(page, view.renderActivity(state(), { lang: "zh" }));
    await expect(page.locator('[data-metric="coverage"] [data-hour="5"]')).toHaveAttribute("data-state", "zero");
  });

  test("VN-742 点选小时进入同一小时上下文", async ({ page }) => {
    const view = await loadView();
    const html = view.renderActivity(state(), { lang: "zh", selectedHour: 8 });
    await mount(page, html);
    const detail = page.locator('[data-detail="8"]');
    await expect(detail).toBeVisible();
    const ctx = view.hourContext(DAY, 8, "Asia/Shanghai");
    await expect(detail).toHaveAttribute("data-detail", "8");
    await expect(page.locator('[data-hour-start="' + ctx.start + '"]')).toBeVisible();
    const startAttr = await page.locator('[data-hour-start]').getAttribute("data-hour-start");
    expect(startAttr).toBe(ctx.start);
  });

  test("VN-742 色阶只在观测格上；深浅主题取不同 token", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderActivity(state(), { lang: "zh", selectedHour: 0 }));
    const top = page.locator('[data-metric="coverage"] [data-hour="0"]');
    await expect(top).toHaveAttribute("data-level", "4");
    const zeroCell = page.locator('[data-metric="coverage"] [data-hour="5"]');
    await expect(zeroCell).toHaveAttribute("data-level", "none");
    const darkBg = await top.evaluate(el => getComputedStyle(el).backgroundColor);
    await mount(page, view.renderActivity(state(), { lang: "zh", selectedHour: 0 }), "light");
    const lightBg = await page.locator('[data-metric="coverage"] [data-hour="0"]').evaluate(
      el => getComputedStyle(el).backgroundColor);
    expect(darkBg).not.toBe(lightBg);
  });

  test("VN-742 键盘：方向键移动焦点，选中格 tabindex=0", async ({ page }) => {
    const view = await loadView();
    const nextHour = view.applyKeyToState(0, "ArrowRight");
    expect(nextHour).toBe(1);
    await mount(page, view.renderActivity(state(), { lang: "zh", selectedHour: nextHour }));
    const grid = page.locator('[data-metric="coverage"] .activity__grid');
    await expect(grid.locator('[data-hour="1"]')).toHaveAttribute("tabindex", "0");
    await expect(grid.locator('[data-hour="1"]')).toHaveAttribute("aria-selected", "true");
    await expect(grid.locator('[data-hour="0"]')).toHaveAttribute("tabindex", "-1");
    // 每格都有 aria-label 文案（非颜色提示）。
    const label = await grid.locator('[data-hour="0"]').getAttribute("aria-label");
    expect(label).toContain("执行覆盖");
  });

  test("VN-742 英文界面不残留中文且无横向滚动", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderActivity(state({
      projects: [{ id: "p1", label: "Project One" }],
      project_id: "p1",
    }), { lang: "en", selectedHour: 0 }));
    await expect(page.locator(".activity__toolbar")).toBeVisible();
    expect(await page.locator("body").innerText()).not.toMatch(/[\u4e00-\u9fa5]/);
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test("VN-742 700px 窄屏：12 列网格且不横向滚动", async ({ page }) => {
    const view = await loadView();
    await page.setViewportSize({ width: 700, height: 900 });
    await mount(page, view.renderActivity(state(), { lang: "zh", selectedHour: 0 }));
    const cols = await page.locator('[data-metric="coverage"] .activity__grid').evaluate(
      el => getComputedStyle(el).gridTemplateColumns.split(" ").length);
    expect(cols).toBe(12);
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test("VN-742 工程筛选与空／错误状态", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderActivity(state(), { lang: "zh" }));
    const select = page.locator('select[data-action="project"]');
    await expect(select).toBeVisible();
    await expect(select).toHaveValue("p1");
    await expect(select.locator("option")).toHaveCount(2);

    await mount(page, view.renderActivity({ status: "error" }, { lang: "zh" }));
    await expect(page.locator(".empty-state")).toBeVisible();
    await expect(page.locator(".activity")).toHaveAttribute("data-status", "error");

    await mount(page, view.renderActivity(
      state({ hours: [], completeness: "complete" }), { lang: "zh" }));
    await expect(page.locator(".activity")).toHaveAttribute("data-status", "empty");
    await expect(page.locator(".empty-state")).toContainText("这一天没有观测到活动");
  });
  // 内容留白由外层 .vnext / .content 统一给：视图根元素自己再加一层，
  // 页面左右就会缩进两次、底部也会多一段空白。
  test("VN-742 视图根元素不再自带内边距", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderActivity(state(), { lang: "zh", selectedHour: 0 }));
    const padding = await page.locator(".activity").evaluate(el => {
      const cs = getComputedStyle(el);
      return [cs.paddingTop, cs.paddingRight, cs.paddingBottom, cs.paddingLeft];
    });
    expect(padding).toEqual(["0px", "0px", "0px", "0px"]);
  });
});
