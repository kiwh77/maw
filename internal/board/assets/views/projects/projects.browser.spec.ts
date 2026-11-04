// 工程清单的真实浏览器验收（SETTINGS / PROJECTS）。
//
// 只验证本视图 Surface 内的产物：projects-view.mjs 的 HTML + 共享 token／组件 +
// 本视图局部样式；核对深浅主题、双语、窄屏、键盘与各状态。
//
// 两条这一页特有的回归点：
//   1. 工程名列不能再被相邻长文本挤成四五个字符（旧版六行全是 `Nove…`）。
//   2. 窄屏收列时表格不能错位——收列必须收起整列（col + 单元格），
//      只隐藏单元格会让后面的列前移去占 0 宽的列。
//
// 运行：npm --prefix tests/browser test -- --grep 'SETTINGS / PROJECTS'

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

const NOW = Date.parse("2026-11-04T06:00:00Z");

const READY = {
  id: "p1",
  name: "goal-agents",
  path: "/Users/me/Projects/goal-agents",
  main_branch: "main",
  max_parallel: 4,
  last_seen: "2026-11-04T05:30:00Z",
};

const OTHER = {
  id: "p2",
  name: "companios-classroom-web-admin",
  path: "/Users/me/Projects/dbj/apps/web/companios-classroom-web-admin",
  main_branch: "feature/2.8.0",
  max_parallel: 0,
  last_seen: "2026-10-28T09:16:24Z",
};

const STATE = {
  projects: [READY, OTHER],
  taskCounts: { p1: { total: 130, running: 3 }, p2: { total: 19, running: 0 } },
};

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

async function mount(page, html, theme = "dark") {
  await page.setContent(
    `<!DOCTYPE html><html data-theme="${theme}" lang="zh-CN"><head><meta charset="utf-8">` +
    `<style>${loadCSS()}</style></head><body><div class="page">${html}</div></body></html>`
  );
}

test.describe("SETTINGS / PROJECTS 工程清单", () => {
  test("列信息只列快照读得到的字段", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderProjectList(STATE, { lang: "zh", now: NOW }));

    for (const label of ["工程", "任务", "主分支", "max_parallel 参考值", "最近活动"]) {
      await expect(page.locator("th", { hasText: label })).toHaveCount(1);
    }
    // 策略读不到就不占列：旧版三列在真实中枢下永远是「未就绪 + 空值」。
    await expect(page.locator("th", { hasText: "自动模式" })).toHaveCount(0);
    await expect(page.locator("th", { hasText: "计算上限" })).toHaveCount(0);
    await expect(page.locator("th", { hasText: "节点容量" })).toHaveCount(0);

    await expect(page.locator('tr[data-project="p1"] td[data-field="tasks"]')).toContainText("130");
    await expect(page.locator('tr[data-project="p1"] td[data-field="tasks"]')).toContainText("进行中 3");
    await expect(page.locator('tr[data-project="p2"] td[data-field="branch"]')).toHaveText("feature/2.8.0");
    await expect(page.locator('tr[data-project="p2"] td[data-field="ref"]')).toHaveText("未设置");
    await expect(page.locator('tr[data-project="p2"] td[data-field="seen"]')).toHaveText("6 天前");
  });

  test("工程名列不被挤成省略号，路径作为第二行可悬停看全值", async ({ page }) => {
    const view = await loadView();
    await page.setViewportSize({ width: 1200, height: 900 });
    await mount(page, view.renderProjectList(STATE, { lang: "zh", now: NOW }));

    const nameCell = page.locator('tr[data-project="p2"] td.name');
    const nameBox = await nameCell.locator(".projects-name").boundingBox();
    // 长工程名要有容身之处：旧版这一列只有四五个字符宽。
    expect(nameBox!.width).toBeGreaterThan(150);

    // 名称没被截断（截断则 scrollWidth > clientWidth）。
    const truncated = await nameCell.locator(".projects-name").evaluate(
      el => el.scrollWidth > el.clientWidth + 1
    );
    expect(truncated).toBe(false);

    await expect(nameCell.locator(".projects-name")).toHaveAttribute("title", OTHER.name);
    await expect(nameCell.locator(".projects-path")).toHaveAttribute("title", OTHER.path);
  });

  test("任务数未知时写占位符，不渲染成 0", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderProjectList({ projects: [READY] }, { lang: "zh", now: NOW }));

    await expect(page.locator(".projects-meta")).toContainText("任务数未知");
    await expect(page.locator('tr[data-project="p1"] td[data-field="tasks"]')).toHaveText("—");
  });

  test("浅色主题换用另一套 token", async ({ page }) => {
    const view = await loadView();
    const html = view.renderProjectList(STATE, { lang: "zh", now: NOW });

    await mount(page, html, "dark");
    const dark = await page.locator(".projects-path").first().evaluate(el => getComputedStyle(el).color);

    await mount(page, html, "light");
    const light = await page.locator(".projects-path").first().evaluate(el => getComputedStyle(el).color);

    expect(dark).not.toBe(light);
    await expect(page.locator("table")).toBeVisible();
  });

  test("英文界面不撑破布局且不残留中文", async ({ page }) => {
    const view = await loadView();
    await page.setViewportSize({ width: 1200, height: 900 });
    await mount(page, view.renderProjectList(STATE, { lang: "en", now: NOW }));

    await expect(page.locator(".projects-meta")).toContainText("projects");
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    const body = await page.locator("body").innerText();
    expect(body.replace(/goal-agents|companios-classroom-web-admin/g, "")).not.toMatch(/[\u4e00-\u9fa5]/);
  });

  test("700px 窄屏收列不错位、无页面级横向滚动", async ({ page }) => {
    const view = await loadView();
    await page.setViewportSize({ width: 700, height: 900 });
    await mount(page, view.renderProjectList(STATE, { lang: "zh", now: NOW }));

    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    await expect(page.locator("table")).toBeVisible();

    // 主分支与参考值整列收起。
    await expect(page.locator("th", { hasText: "主分支" })).toBeHidden();
    await expect(page.locator('tr[data-project="p1"] td[data-field="branch"]')).toBeHidden();

    // 剩下的列各就各位：任务、最近活动、操作不叠在一起。
    const boxes = await page.evaluate(() => {
      const row = document.querySelector('tr[data-project="p1"]');
      const pick = sel => {
        const el = row!.querySelector(sel);
        const r = el!.getBoundingClientRect();
        return { left: Math.round(r.left), right: Math.round(r.right) };
      };
      return {
        tasks: pick('td[data-field="tasks"]'),
        seen: pick('td[data-field="seen"]'),
        name: pick("td.name"),
      };
    });
    expect(boxes.tasks.right).toBeLessThanOrEqual(boxes.seen.left + 1);
    expect(boxes.name.right).toBeLessThanOrEqual(boxes.tasks.left + 1);
    // 窄屏下工程名仍有可读宽度。
    expect(boxes.name.right - boxes.name.left).toBeGreaterThan(180);
  });

  test("错误与未知状态不渲染表格", async ({ page }) => {
    const view = await loadView();

    await mount(page, view.renderProjectList({ status: "error" }, { lang: "zh" }));
    await expect(page.locator("table")).toHaveCount(0);
    await expect(page.locator(".empty-state")).toContainText("工程列表加载失败");

    await mount(page, view.renderProjectList({ status: "unknown" }, { lang: "zh" }));
    await expect(page.locator(".empty-state")).toContainText("工程状态未知");
  });

  test("键盘可到达工程名与移除按钮", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderProjectList(STATE, { lang: "zh", now: NOW, canRemove: true }));

    const button = page.locator('tr[data-project="p1"] [data-project-remove="p1"]');
    await expect(button).toBeVisible();
    await button.focus();
    expect(await button.evaluate(el => document.activeElement === el)).toBe(true);
    await expect(page.locator("td.name").first()).toContainText("goal-agents");
  });

  test("移除按钮与正文不同色，且只读宿主没有它", async ({ page }) => {
    const view = await loadView();

    await mount(page, view.renderProjectList(STATE, { lang: "zh", now: NOW, canRemove: true }));
    const button = page.locator('tr[data-project="p1"] [data-project-remove="p1"]');
    // 先取静态颜色：聚焦／悬停会按设计变亮，那不是「和正文同色」的证据。
    const btnColor = await button.evaluate(el => getComputedStyle(el).color);
    const nameColor = await page.locator(".projects-name").first().evaluate(el => getComputedStyle(el).color);
    expect(btnColor).not.toBe(nameColor);

    await mount(page, view.renderProjectList(STATE, { lang: "zh", now: NOW }));
    await expect(page.locator("[data-project-remove]")).toHaveCount(0);
  });

  test("移除确认态把删除入口收起来，只剩确认与取消", async ({ page }) => {
    const view = await loadView();
    await mount(
      page,
      view.renderProjectList(STATE, { lang: "zh", now: NOW, canRemove: true, pendingRemove: "p1" }),
    );

    await expect(page.locator('tr[data-project="p1"]')).toContainText("确认移除？");
    await expect(page.locator('[data-project-remove="p1"]')).toHaveCount(0);
    await expect(page.locator('[data-project-remove-confirm="p1"]')).toBeVisible();
    await expect(page.locator('[data-project-remove-cancel="p1"]')).toBeVisible();
    // 未进入确认态的行保持原样，不跟着变。
    await expect(page.locator('[data-project-remove="p2"]')).toBeVisible();
  });

  test("移除失败提示在深浅主题下都可读", async ({ page }) => {
    const view = await loadView();
    const html = view.renderProjectList(STATE, {
      lang: "zh", now: NOW, canRemove: true,
      removeNotice: { kind: "error", text: "工程还有未完成的任务或在线 Agent，先收尾再移除" },
    });

    await mount(page, html, "dark");
    const dark = await page.locator(".projects-notice--error").evaluate(el => getComputedStyle(el).color);

    await mount(page, html, "light");
    const light = await page.locator(".projects-notice--error").evaluate(el => getComputedStyle(el).color);

    expect(dark).not.toBe(light);
    await expect(page.locator(".projects-notice--error")).toContainText("先收尾再移除");
  });
});
