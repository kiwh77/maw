// VN-722 任务创建与指定派发的真实浏览器验收（UI-C）。
//
// 只验证本卡 Surface 内的产物：tasks-view.mjs 渲染出的列表／表单／派发状态，
// 配上 VN-702 的共享 token 与组件，核对主题、双语、窄屏、键盘与各状态。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-722'

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
const COMPONENTS = ["badge.css", "table.css", "form.css", "empty.css", "drawer.css"];

const loadView = () => import(pathToFileURL(path.join(VIEW_DIR, "tasks-view.mjs")).href);

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const parts = [
    fs.readFileSync(path.join(ASSETS, "styles/tokens.css"), "utf8"),
    ...COMPONENTS.map(f => fs.readFileSync(path.join(ASSETS, "components", f), "utf8")),
    fs.readFileSync(path.join(ASSETS, "components/components.css"), "utf8").replace(/@import\s+"[^"]+";/g, ""),
  ];
  return strip(parts.join("\n"));
}

const ALL_TASKS = [
  { id: "t1", instance_id: "ti_a", display_id: "VN-001", title: "前置题", status: "done", priority: 10, write_scope: ["internal/a/**"] },
  { id: "t3", instance_id: "ti_c", display_id: "VN-003", title: "已退役题", status: "cancelled", state: "retired", priority: 30, write_scope: ["internal/c/**"] },
];

const readyTask = {
  id: "t9", instance_id: "ti_new", display_id: "VN-009", title: "派发中的题",
  status: "ready", priority: 42, merge_order: 42, deps: ["ti_a"],
  write_scope: ["internal/board/assets/views/tasks/tasks-view.mjs"], revision: 7,
};

const deadlockTask = { ...readyTask, id: "t10", instance_id: "ti_x", display_id: "VN-010", deps: ["VN-003"] };

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
const DARK_OK = rgb("#9cdbb0");
const DARK_DANGER = rgb("#ed9797");
const LIGHT_OK = rgb("#267044");

test.describe("VN-722 任务创建与指定派发", () => {
  test("VN-722 列表复用共享表格与徽标，并显示任务实际优先级", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderTaskList(
      { tasks: [readyTask, deadlockTask], allTasks: ALL_TASKS },
      { lang: "zh" }
    ));

    await expect(page.locator(".table")).toBeVisible();
    // 实际优先级 42 必须出现，且不是排序位置
    await expect(page.locator("td.num").first()).toHaveText("42");
    await expect(page.locator(".badge").filter({ hasText: /^可派发$/ })).toHaveCSS("color", DARK_OK);
    await expect(page.locator(".badge").filter({ hasText: /^不可派发$/ })).toHaveCSS("color", DARK_DANGER);
    // 死锁与「等前置」必须分开说
    await expect(page.locator(".table td").filter({ hasText: "结构性死锁" })).toHaveCount(1);
  });

  test("VN-722 浅色主题换用另一套 token", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderTaskList({ tasks: [readyTask], allTasks: ALL_TASKS }, { lang: "zh" }), "light");
    await expect(page.locator(".badge").filter({ hasText: /^可派发$/ })).toHaveCSS("color", LIGHT_OK);
  });

  test("VN-722 英文界面不残留中文且无页面级横向滚动", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderTaskList(
      { tasks: [readyTask, deadlockTask], allTasks: ALL_TASKS },
      { lang: "en" }
    ));
    await expect(page.locator(".table")).toBeVisible();
    await expect(page.locator(".field-hint").first()).toContainText("tasks");
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    const text = await page.locator("body").innerText();
    expect(text.replace(/前置题|已退役题|派发中的题/g, "")).not.toMatch(/[\u4e00-\u9fa5]/);
  });

  test("VN-722 700px 窄屏：页面不横向滚动，表格在内容区滚动", async ({ page }) => {
    const view = await loadView();
    await page.setViewportSize({ width: 700, height: 900 });
    await mount(page, view.renderTaskList(
      { tasks: [readyTask, deadlockTask], allTasks: ALL_TASKS },
      { lang: "zh" }
    ));
    const state = await page.evaluate(() => ({
      doc: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      wrap: getComputedStyle(document.querySelector(".table-wrap")).overflowX,
    }));
    expect(state.doc).toBeLessThanOrEqual(0);
    expect(state.wrap).toBe("auto");
  });

  test("VN-722 编辑缺版本时提交被禁用并说明原因", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderTaskForm({ mode: "edit", draft: readyTask, revision: null }, { lang: "zh" }));
    await expect(page.locator("#task-submit")).toBeDisabled();
    await expect(page.locator(".field-error")).toContainText("缺少版本号");

    await mount(page, view.renderTaskForm({ mode: "edit", draft: readyTask, revision: 7 }, { lang: "zh" }));
    await expect(page.locator("#task-submit")).toBeEnabled();
  });

  test("VN-722 字段错误可被读屏关联，键盘焦点环可见", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderTaskForm(
      { mode: "create", draft: { ...readyTask, title: "" }, errors: [{ field: "title", code: "title_required" }] },
      { lang: "zh" }
    ));
    const input = page.locator("#f-title");
    await expect(input).toHaveAttribute("aria-invalid", "true");
    await expect(input).toHaveAttribute("aria-describedby", "err-title");
    await expect(page.locator("#err-title")).toBeVisible();

    await page.locator("body").click({ position: { x: 2, y: 2 } });
    await page.keyboard.press("Tab");
    const style = await input.evaluate(el => {
      el.focus();
      const s = getComputedStyle(el);
      return { width: s.outlineWidth, offset: s.outlineOffset };
    });
    expect(style.width).toBe("2px");
    expect(style.offset).toBe("3px");
  });

  test("VN-722 派发 202 后停在操作中，只有 succeeded 才算完成", async ({ page }) => {
    const view = await loadView();

    await mount(page, view.renderDispatchState(
      { phase: "pending", operation: { status: "queued", retry_after_ms: 800 } },
      { lang: "zh" }
    ));
    await expect(page.locator('[data-phase="pending"]')).toBeVisible();
    await expect(page.locator(".badge").filter({ hasText: /已受理/ })).toHaveCount(1);
    await expect(page.locator(".badge").filter({ hasText: /派发成功/ })).toHaveCount(0);
    await expect(page.locator("body")).toContainText("800ms");

    await mount(page, view.renderDispatchState(
      { phase: "succeeded", result: { operation_id: "op_7f3c9a" } },
      { lang: "zh" }
    ));
    await expect(page.locator('[data-phase="succeeded"]')).toBeVisible();
    await expect(page.locator("body")).toContainText("op_7f3c9a");
  });

  test("VN-722 加载／空／错误／过期／未认证状态互不重复", async ({ page }) => {
    const view = await loadView();
    const states = {
      loading: { status: "loading" },
      empty: { tasks: [] },
      error: { status: "error" },
      expired: { status: "expired" },
      unauth: { status: "unauthenticated" },
    };
    const texts = new Set();
    for (const [name, state] of Object.entries(states)) {
      await mount(page, view.renderTaskList(state, { lang: "zh" }));
      const text = (await page.locator("body").innerText()).trim();
      expect(text.length, name + " 必须有文案").toBeGreaterThan(0);
      expect(texts.has(text), name + " 与其他状态重复").toBe(false);
      texts.add(text);
      if (name !== "loading") {
        await expect(page.locator(".empty-state")).toBeVisible();
      }
    }
  });
});
