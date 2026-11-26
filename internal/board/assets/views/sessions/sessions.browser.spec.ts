// VN-743 会话轮次与来源明细的真实浏览器验收（UI-C）。
//
// 验证共享／未归属标注、不支持来源的降级态、跳转入口条件、键盘选择、
// 中英深浅主题与窄屏，以及六种状态。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-743'

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
const loadView = () => import(pathToFileURL(path.join(VIEW_DIR, "sessions-view.mjs")).href);

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const parts = [
    fs.readFileSync(path.join(ASSETS, "styles/tokens.css"), "utf8"),
    ...COMPONENTS.map(f => fs.readFileSync(path.join(ASSETS, "components", f), "utf8")),
    fs.readFileSync(path.join(ASSETS, "components/components.css"), "utf8").replace(/@import\s+"[^"]+";/g, ""),
    fs.readFileSync(path.join(VIEW_DIR, "sessions.css"), "utf8"),
  ];
  return strip(parts.join("\n"));
}

const sessions = () => ([
  {
    session_id: "sess-1",
    source_ns: "codebuddy",
    adapter: "codebuddy-cli",
    opened_at: "2026-10-23T02:00:00Z",
    tasks: ["ti_a", "ti_b"],
    usage: { present: true, total_tokens: 23968, usage_unknown: false, quality: "observed" },
    turns: [
      { turn_id: "tu-1", started_at: "2026-10-23T02:01:00Z", ended_at: "2026-10-23T02:05:00Z", shared_with: ["ti_b"], usage: { present: true, total_tokens: 23968, usage_unknown: false, quality: "observed" } },
      { turn_id: "tu-2", started_at: "2026-10-23T02:06:00Z", ended_at: null, shared_with: [], usage: { present: true, usage_unknown: true, quality: "unknown" } },
    ],
  },
  {
    session_id: "sess-2",
    source_ns: "codex",
    opened_at: "2026-10-23T03:00:00Z",
    tasks: [],
    turns_supported: false,
    degrade_reason: "C24/C25 未验证",
  },
]);

const state = (over = {}) => ({
  status: "ready",
  sessions: sessions(),
  completeness: "complete",
  watermark: { cursor: 40, complete_before: 40 },
  ...over,
});

async function mount(page, html, theme = "dark") {
  await page.setContent(
    `<!DOCTYPE html><html data-theme="${theme}" lang="zh-CN"><head><meta charset="utf-8">` +
    `<style>${loadCSS()}</style></head><body><div class="page" id="page">${html}</div></body></html>`
  );
}

test.describe("VN-743 会话轮次与来源明细", () => {
  test("VN-743 列表呈现共享／未归属与已知用量", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderSessions(state(), { lang: "zh", selectedId: "sess-1" }));
    await expect(page.locator('[data-session="sess-1"]')).toHaveAttribute("aria-selected", "true");
    await expect(page.locator(".sessions")).toContainText("2 个任务");
    await expect(page.locator(".sessions")).toContainText("总量 23968 tok");
    await expect(page.locator(".sessions")).toContainText("未归属任何任务");
    // 详情：共享徽章与未知用量徽章都是文字提示，不靠颜色。
    await expect(page.locator('[data-detail="sess-1"]')).toContainText("共享轮次");
    await expect(page.locator('[data-detail="sess-1"]')).toContainText("未知用量");
    await expect(page.locator('[data-detail="sess-1"]')).toContainText("未结束／缺边界");
  });

  test("VN-743 不支持轮次的来源给出一致降级态，无跳转入口", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderSessions(state(), { lang: "zh", selectedId: "sess-2" }));
    const detail = page.locator('[data-detail="sess-2"]');
    await expect(detail.locator('[data-turns="unsupported"]')).toBeVisible();
    await expect(detail).toContainText("该来源不支持轮次明细");
    await expect(detail.locator(".sessions__jump")).toHaveCount(0);
  });

  test("VN-743 跳转入口只在有真实开始时间的轮次出现，上下文对齐整点", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderSessions(state(), { lang: "zh", selectedId: "sess-1" }));
    const jumps = page.locator(".sessions__jump");
    await expect(jumps).toHaveCount(2); // tu-1、tu-2 都有开始时间
    const start = await jumps.first().getAttribute("data-jump-start");
    expect(start).toBe("2026-10-23T02:00:00.000Z");
    const end = await jumps.first().getAttribute("data-jump-end");
    expect(end).toBe("2026-10-23T03:00:00.000Z");
    // 缺开始边界的轮次不发明入口。
    const noStart = view.renderSessions(state({
      sessions: [{ session_id: "s", source_ns: "x", tasks: [], turns: [{ turn_id: "t", started_at: "", ended_at: "2026-10-23T02:05:00Z", shared_with: [], usage: { present: false } }] }],
    }), { lang: "zh", selectedId: "s" });
    expect(noStart).not.toContain("sessions__jump");
    expect(noStart).toContain("缺开始边界");
  });

  test("VN-743 键盘选择行并打开详情", async ({ page }) => {
    const view = await loadView();
    const next = view.applyKeyToState({ sessions: sessions() }, { selectedId: "sess-1", showDetail: false }, "ArrowDown");
    expect(next.selectedId).toBe("sess-2");
    await mount(page, view.renderSessions(state(), { lang: "zh", selectedId: next.selectedId }));
    await expect(page.locator(".sessions")).toHaveAttribute("data-selected", "sess-2");
    await expect(page.locator('[data-session="sess-2"]')).toHaveAttribute("tabindex", "0");
  });

  test("VN-743 深浅主题与窄屏", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderSessions(state(), { lang: "zh", selectedId: "sess-1" }));
    const darkUnknown = await page.locator(".sessions__usage--unknown").first().evaluate(
      el => getComputedStyle(el).color);
    await mount(page, view.renderSessions(state(), { lang: "zh", selectedId: "sess-1" }), "light");
    const lightUnknown = await page.locator(".sessions__usage--unknown").first().evaluate(
      el => getComputedStyle(el).color);
    expect(darkUnknown).not.toBe(lightUnknown);

    await page.setViewportSize({ width: 700, height: 900 });
    await mount(page, view.renderSessions(state(), { lang: "zh", selectedId: "sess-1" }));
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test("VN-743 英文界面不残留中文且无横向滚动", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderSessions(state(), { lang: "en", selectedId: "sess-2" }));
    await expect(page.locator(".sessions__table")).toBeVisible();
    expect(await page.locator("body").innerText()).not.toMatch(/[\u4e00-\u9fa5]/);
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test("VN-743 空与错误状态使用 empty-state 组件", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderSessions({ status: "error" }, { lang: "zh" }));
    await expect(page.locator(".empty-state")).toBeVisible();
    await expect(page.locator(".sessions")).toHaveAttribute("data-status", "error");

    await mount(page, view.renderSessions({ sessions: [] }, { lang: "zh" }));
    await expect(page.locator(".sessions")).toHaveAttribute("data-status", "empty");

    await mount(page, view.renderSessions(state({ completeness: "partial" }), { lang: "zh" }));
    await expect(page.locator(".sessions")).toHaveAttribute("data-status", "unknown");
  });
  // 内容留白由外层 .vnext / .content 统一给：视图根元素自己再加一层，
  // 页面左右就会缩进两次、底部也会多一段空白。
  test("VN-743 视图根元素不再自带内边距", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderSessions(state(), { lang: "zh", selectedId: "sess-1" }));
    const padding = await page.locator(".sessions").evaluate(el => {
      const cs = getComputedStyle(el);
      return [cs.paddingTop, cs.paddingRight, cs.paddingBottom, cs.paddingLeft];
    });
    expect(padding).toEqual(["0px", "0px", "0px", "0px"]);
  });
});
