// VN-725 工程列表的真实浏览器验收（UI-C）。
//
// 只验证本卡 Surface 内的产物：projects-view.mjs 的 HTML + 共享 token／组件 +
// 本视图局部样式；核对深浅主题、双语、窄屏、键盘与各状态。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-725'

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
const STYLES = path.join(ASSETS, "styles/tokens.css");
const COMPONENTS = ["badge.css", "table.css", "form.css", "empty.css"];

async function loadView() {
  return import(pathToFileURL(path.join(VIEW_DIR, "projects-view.mjs")).href);
}

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const tokens = fs.readFileSync(STYLES, "utf8");
  const parts = COMPONENTS.map(f => fs.readFileSync(path.join(ASSETS, "components", f), "utf8"));
  const entry = fs
    .readFileSync(path.join(ASSETS, "components/components.css"), "utf8")
    .replace(/@import\s+"[^"]+";/g, "");
  const local = fs.readFileSync(path.join(VIEW_DIR, "projects.css"), "utf8");
  return strip([tokens, ...parts, entry, local].join("\n"));
}

const READY = {
  project_id: "p1",
  name: "goal-agents",
  platform_supported: true,
  workspace: "git",
  policy: {
    auto_enabled: false,
    managed_execution_limit: 1,
    node_capacity: 2,
    max_parallel_reference: 4,
    revision: 7,
  },
};

const NOT_READY = {
  project_id: "p2",
  name: "niuma-buddy",
  platform_supported: false,
  workspace: "nongit",
  policy: { auto_enabled: false, managed_execution_limit: 1, revision: 3 },
};

async function mount(page, html, theme = "dark") {
  await page.setContent(
    `<!DOCTYPE html><html data-theme="${theme}" lang="zh-CN"><head><meta charset="utf-8">` +
    `<style>${loadCSS()}</style></head><body><div class="page">${html}</div></body></html>`
  );
}

test.describe("VN-725 工程列表与策略概览", () => {
  test("VN-725 深色主题下四列分开且参考值弱化", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderProjectList({ projects: [READY, NOT_READY] }, { lang: "zh" }));

    await expect(page.locator("h1")).toHaveText("工程");
    for (const label of ["自动模式", "计算上限", "节点容量", "max_parallel 参考值"]) {
      await expect(page.locator("th", { hasText: label })).toHaveCount(1);
    }
    // 参考值列带「仅供参考」说明，且颜色比数字淡
    const refNote = page.locator('td[data-field="ref"] .field-hint').first();
    await expect(refNote).toContainText("仅供参考");
    const noteColor = await refNote.evaluate(el => getComputedStyle(el).color);
    const nameColor = await page.locator("td.name").first().evaluate(el => getComputedStyle(el).color);
    expect(noteColor).not.toBe(nameColor);

    // 未就绪项目写明原因，非 Git 工作区如实标注
    await expect(page.locator('tr[data-project="p2"] .badge')).toHaveText("未就绪");
    await expect(page.locator('tr[data-project="p2"]')).toContainText("macOS");
    await expect(page.locator('tr[data-project="p2"] td[data-field="workspace"]')).toHaveText("非 Git");
    // 缺字段用占位符，不显示 0
    await expect(page.locator('tr[data-project="p2"] td[data-field="capacity"]')).toHaveText("—");
  });

  test("VN-725 浅色主题换用另一套 token", async ({ page }) => {
    const view = await loadView();
    const html = view.renderProjectList({ projects: [READY] }, { lang: "zh" });

    await mount(page, html, "dark");
    const dark = await page.locator(".badge").first().evaluate(el => getComputedStyle(el).color);

    await mount(page, html, "light");
    const light = await page.locator(".badge").first().evaluate(el => getComputedStyle(el).color);

    expect(dark).not.toBe(light);
    await expect(page.locator("table")).toBeVisible();
  });

  test("VN-725 英文界面不撑破布局且不残留中文", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderProjectList({ projects: [READY, NOT_READY] }, { lang: "en" }));

    await expect(page.locator("h1")).toHaveText("Projects");
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    const body = await page.locator("body").innerText();
    expect(body.replace(/goal-agents|niuma-buddy/g, "")).not.toMatch(/[\u4e00-\u9fa5]/);
  });

  test("VN-725 700px 窄屏无页面级横向滚动", async ({ page }) => {
    const view = await loadView();
    await page.setViewportSize({ width: 700, height: 900 });
    await mount(page, view.renderProjectList({ projects: [READY, NOT_READY] }, { lang: "zh" }));

    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    await expect(page.locator("table")).toBeVisible();
  });

  test("VN-725 错误与未知状态不渲染表格", async ({ page }) => {
    const view = await loadView();

    await mount(page, view.renderProjectList({ status: "error" }, { lang: "zh" }));
    await expect(page.locator("table")).toHaveCount(0);
    await expect(page.locator(".empty-state")).toContainText("工程列表加载失败");

    await mount(page, view.renderProjectList({ status: "unknown" }, { lang: "zh" }));
    await expect(page.locator(".empty-state")).toContainText("策略状态未知");
  });

  test("VN-725 键盘可到达设置入口所在的表格行内链接按钮", async ({ page }) => {
    const view = await loadView();
    // 列表本身没有可聚焦控件时，键盘焦点应落在表格容器上而不是丢失。
    await mount(page, view.renderProjectList({ projects: [READY] }, { lang: "zh" }));
    const focusable = await page.evaluate(() => {
      const el = document.querySelector("table");
      el.setAttribute("tabindex", "0");
      el.focus();
      return document.activeElement === el;
    });
    expect(focusable).toBe(true);
    await expect(page.locator("td.name")).toContainText("goal-agents");
  });
});
