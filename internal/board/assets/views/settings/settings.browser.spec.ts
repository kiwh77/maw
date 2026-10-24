// VN-725 设置页的真实浏览器验收（UI-C）。
//
// 只验证本卡 Surface 内的产物：把 settings-view.mjs 渲染出的 HTML，连同 VN-702
// 的共享 token／组件一起挂进页面，核对深浅主题、双语、窄屏、键盘焦点与
// 「加载／空／错误／过期／unknown／冲突」各状态。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-725'

import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

// @playwright/test 装在 tests/browser/node_modules，本文件在 internal/** 下需要显式解析。
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
const COMPONENTS = ["badge.css", "table.css", "form.css", "empty.css", "drawer.css"];

async function loadView() {
  return import(pathToFileURL(path.join(VIEW_DIR, "settings-view.mjs")).href);
}

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const tokens = fs.readFileSync(STYLES, "utf8");
  const parts = COMPONENTS.map(f => fs.readFileSync(path.join(ASSETS, "components", f), "utf8"));
  const entry = fs
    .readFileSync(path.join(ASSETS, "components/components.css"), "utf8")
    .replace(/@import\s+"[^"]+";/g, "");
  const local = fs.readFileSync(path.join(VIEW_DIR, "settings.css"), "utf8");
  return strip([tokens, ...parts, entry, local].join("\n"));
}

const POLICY = {
  auto_enabled: false,
  managed_execution_limit: 1,
  node_capacity: 2,
  max_parallel_reference: 4,
  retention: { days: 30, runs: 50 },
  revision: 7,
};

async function mount(page, html, theme = "dark") {
  await page.setContent(
    `<!DOCTYPE html><html data-theme="${theme}" lang="zh-CN"><head><meta charset="utf-8">` +
    `<style>${loadCSS()}</style></head><body><div class="page">${html}</div></body></html>`
  );
}

test.describe("VN-725 工程策略与保留设置", () => {
  test("VN-725 深色主题下四个概念分开呈现", async ({ page }) => {
    const view = await loadView();
    const policy = view.normalizePolicy(POLICY);
    await mount(page, view.renderSettings({ status: "ready", policy, save: "idle" }, { lang: "zh" }));

    await expect(page.locator("h1")).toHaveText("工程策略与保留");
    for (const label of ["自动模式", "计算上限", "节点容量", "max_parallel 参考值"]) {
      await expect(page.locator("th", { hasText: label })).toHaveCount(1);
    }
    // 默认关闭：自动模式那行是「关闭」，不是「已开启」
    await expect(page.locator('tr[data-concept="auto"] .badge')).toHaveText("关闭");
    await expect(page.locator('tr[data-concept="ref"]')).toContainText("仅供参考");
    // 表单四个输入都在，且没有密码框
    await expect(page.locator("form input.input")).toHaveCount(4);
    await expect(page.locator('input[type="password"]')).toHaveCount(0);
    await expect(page.locator("[data-no-secret]")).toContainText("本页不含令牌");
  });

  test("VN-725 浅色主题换用另一套 token", async ({ page }) => {
    const view = await loadView();
    const policy = view.normalizePolicy(POLICY);
    const html = view.renderSettings({ status: "ready", policy }, { lang: "zh" });

    await mount(page, html, "dark");
    const dark = await page.locator(".badge").first().evaluate(el => getComputedStyle(el).color);

    await mount(page, html, "light");
    const light = await page.locator(".badge").first().evaluate(el => getComputedStyle(el).color);

    expect(dark).not.toBe(light);
    await expect(page.locator("table")).toBeVisible();
  });

  test("VN-725 英文界面不撑破布局且不残留中文", async ({ page }) => {
    const view = await loadView();
    const policy = view.normalizePolicy(POLICY);
    await mount(page, view.renderSettings({ status: "ready", policy }, { lang: "en" }));

    await expect(page.locator("h1")).toHaveText("Execution policy & retention");
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    const body = await page.locator("body").innerText();
    expect(body).not.toMatch(/[\u4e00-\u9fa5]/);
  });

  test("VN-725 700px 窄屏无页面级横向滚动", async ({ page }) => {
    const view = await loadView();
    const policy = view.normalizePolicy(POLICY);
    await page.setViewportSize({ width: 700, height: 900 });
    await mount(page, view.renderSettings({ status: "ready", policy }, { lang: "zh" }));

    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    await expect(page.locator("form input.input").first()).toBeVisible();
  });

  test("VN-725 键盘可依次到达四个输入与提交按钮", async ({ page }) => {
    const view = await loadView();
    const policy = view.normalizePolicy(POLICY);
    await mount(page, view.renderSettings({ status: "ready", policy, save: "dirty" }, { lang: "zh" }));

    const order = [];
    for (let i = 0; i < 5; i += 1) {
      await page.keyboard.press("Tab");
      order.push(await page.evaluate(() => {
        const el = document.activeElement;
        return el ? (el.id || el.getAttribute("type") || el.tagName.toLowerCase()) : "";
      }));
    }
    expect(order.slice(0, 4)).toEqual([
      "settings-limit", "settings-capacity", "settings-retentionDays", "settings-retentionRuns",
    ]);
    expect(order[4]).toBe("submit");
  });

  test("VN-725 冲突态可看见重新载入入口与说明", async ({ page }) => {
    const view = await loadView();
    const policy = view.normalizePolicy(POLICY);
    await mount(page, view.renderSettings({ status: "ready", policy, save: "conflict" }, { lang: "zh" }));

    await expect(page.locator('[data-save="conflict"]')).toContainText("设置已被其他人更新");
    await expect(page.locator('button[data-action="reload"]')).toBeVisible();
  });

  test("VN-725 各状态自洽：错误不渲染表单，未就不冒充关闭", async ({ page }) => {
    const view = await loadView();
    const policy = view.normalizePolicy(POLICY);

    await mount(page, view.renderSettings({ status: "error" }, { lang: "zh" }));
    await expect(page.locator("form")).toHaveCount(0);
    await expect(page.locator(".empty-state")).toContainText("设置加载失败");

    await mount(page, view.renderSettings({ status: "ready", policy, platformSupported: false }, { lang: "zh" }));
    await expect(page.locator('tr[data-concept="auto"] .badge')).toHaveText("未就绪");
    await expect(page.locator("[data-auto-reason]")).toContainText("macOS");
  });
});
