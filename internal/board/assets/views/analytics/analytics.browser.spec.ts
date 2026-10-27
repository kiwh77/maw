// VN-744 交付与用量统计的真实浏览器验收（UI-C）。
//
// 验证成果／投入分卡、口径文案、未知标记、共享色阶图例、证据下钻、
// 键盘可达、中英深浅主题与窄屏。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-744'

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
const loadView = () => import(pathToFileURL(path.join(VIEW_DIR, "analytics-view.mjs")).href);

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const parts = [
    fs.readFileSync(path.join(ASSETS, "styles/tokens.css"), "utf8"),
    ...COMPONENTS.map(f => fs.readFileSync(path.join(ASSETS, "components", f), "utf8")),
    fs.readFileSync(path.join(ASSETS, "components/components.css"), "utf8").replace(/@import\s+"[^"]+";/g, ""),
    fs.readFileSync(path.join(VIEW_DIR, "analytics.css"), "utf8"),
  ];
  return strip(parts.join("\n"));
}

const report = () => ({
  scope: { project_id: "p1", timezone: "Asia/Shanghai" },
  generated_at: "2026-10-23T12:00:00Z",
  completeness: "partial",
  watermark: { cursor: 128, complete_before: 120 },
  timeline: {
    task_phase_seconds: 7200, cli_process_seconds: 7200, turn_seconds: 300,
    explicit_wait_seconds: 3600, engineering_seconds: 10800, avg_concurrency: 1.5,
  },
  usage: {
    turn_count: 5, shared_turns: 1, unknown_turns: 1, estimated_turns: 0,
    tokens: { total_tokens: 1730, input_tokens: 1350, output_tokens: 380 },
    usage_unknown: true,
    coverage: { observed_turns: 3, observed_denom: 5, ratio: 0.6 },
  },
  deliveries: { count: 2, superseded: 1, by_mode: { code: 1, non_code: 1 }, ids: ["dl-1", "dl-2"] },
  by_source: [
    { source_ns: "claude", turn_count: 4, usage_unknown: false, turns_supported: true },
    { source_ns: "codex", turn_count: 1, usage_unknown: true, turns_supported: false },
  ],
  by_day: [
    { day: "2026-10-23", delivery_count: 2, turn_count: 4 },
    { day: "2026-10-24", delivery_count: 0, turn_count: 1 },
  ],
  evidence: { intervals: 2, turns: 5, usage_records: 5, deliveries: 2, identities: 1 },
});

async function mount(page, html, theme = "dark") {
  await page.setContent(
    `<!DOCTYPE html><html data-theme="${theme}" lang="zh-CN"><head><meta charset="utf-8">` +
    `<style>${loadCSS()}</style></head><body><div class="page" id="page">${html}</div></body></html>`
  );
}

test.describe("VN-744 交付与用量统计", () => {
  test("VN-744 三张卡分开：成果／投入／用量，口径写在卡里", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderAnalytics({ report: report() }, { lang: "zh" }));
    await expect(page.locator('[data-card="deliveries"] h2')).toHaveText("交付成果");
    await expect(page.locator('[data-card="invest"] h2')).toHaveText("执行投入");
    await expect(page.locator('[data-card="usage"] h2')).toHaveText("轮次用量");
    await expect(page.locator('[data-card="deliveries"]')).toContainText("去重交付数");
    await expect(page.locator('[data-card="invest"]')).toContainText("按身份并集");
    await expect(page.locator('[data-card="usage"]')).toContainText("去重累计");
    await expect(page.locator('[data-card="invest"]')).toContainText("平均并发身份");
  });

  test("VN-744 未知不显示为零：部分未知横幅与未知标记", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderAnalytics({ report: report() }, { lang: "zh" }));
    await expect(page.locator(".analytics__unknown-banner")).toContainText("部分未知");
    await expect(page.locator('[data-unknown="true"]').first()).toBeVisible();
  });

  test("VN-744 来源与按日分布：共享色阶与图例，可下钻证据", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderAnalytics({ report: report() }, { lang: "zh" }));
    await expect(page.locator('[data-chart="sources"] [data-source="claude"] .analytics__bar')).toHaveAttribute("data-level", "4");
    await expect(page.locator('[data-chart="sources"] [data-source="codex"] .analytics__bar')).toHaveAttribute("data-level", "1");
    await expect(page.locator('[data-chart="sources"]')).toContainText("来源不支持轮次");
    await expect(page.locator('[data-legend="levels"]')).toHaveCount(2); // 两张图共用同一图例文案
    const legend = await page.locator('[data-legend="levels"]').first().innerText();
    expect(legend).toContain("色阶");
    // 证据与水位下钻。
    await expect(page.locator("[data-evidence]")).toContainText("轮次 5");
    await expect(page.locator("[data-evidence]")).toContainText("状态水位=128");
    await expect(page.locator(".analytics")).toHaveAttribute("data-status", "unknown");
  });

  test("VN-744 图卡可键盘聚焦", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderAnalytics({ report: report() }, { lang: "zh" }));
    const cards = page.locator(".analytics__card");
    expect(await cards.count()).toBe(3);
    for (let i = 0; i < 3; i++) {
      await expect(cards.nth(i)).toHaveAttribute("tabindex", "0");
    }
    await page.keyboard.press("Tab");
    await expect(page.locator('[data-card="deliveries"]')).toBeFocused();
  });

  test("VN-744 深浅主题色阶取不同 token；窄屏不横向滚动", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderAnalytics({ report: report() }, { lang: "zh" }));
    const darkBar = await page.locator('[data-chart="sources"] [data-level="4"]').evaluate(
      el => getComputedStyle(el).backgroundColor);
    await mount(page, view.renderAnalytics({ report: report() }, { lang: "zh" }), "light");
    const lightBar = await page.locator('[data-chart="sources"] [data-level="4"]').evaluate(
      el => getComputedStyle(el).backgroundColor);
    expect(darkBar).not.toBe(lightBar);

    await page.setViewportSize({ width: 700, height: 900 });
    await mount(page, view.renderAnalytics({ report: report() }, { lang: "zh" }));
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test("VN-744 英文界面不残留中文且无横向滚动", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderAnalytics({ report: report() }, { lang: "en" }));
    await expect(page.locator(".analytics__cards")).toBeVisible();
    expect(await page.locator("body").innerText()).not.toMatch(/[\u4e00-\u9fa5]/);
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test("VN-744 空与错误状态使用 empty-state 组件", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderAnalytics({ status: "error" }, { lang: "zh" }));
    await expect(page.locator(".empty-state")).toBeVisible();
    await expect(page.locator(".analytics")).toHaveAttribute("data-status", "error");

    await mount(page, view.renderAnalytics({ report: {} }, { lang: "zh" }));
    await expect(page.locator(".analytics")).toHaveAttribute("data-status", "empty");
  });
});
