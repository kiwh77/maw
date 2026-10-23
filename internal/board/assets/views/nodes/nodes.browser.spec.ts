// VN-721 节点视图的真实浏览器验收（UI-C）。
//
// 只验证本卡 Surface 内的产物：把 nodes-view.mjs 渲染出的 HTML，
// 连同 VN-702 的共享 token／组件一起挂进页面，核对深浅主题、双语、窄屏、
// 键盘焦点与「加载／空／错误／过期／unknown／未认证」各状态。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-721'

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
  return import(pathToFileURL(path.join(VIEW_DIR, "nodes-view.mjs")).href);
}

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const tokens = fs.readFileSync(STYLES, "utf8");
  const parts = COMPONENTS.map(f => fs.readFileSync(path.join(ASSETS, "components", f), "utf8"));
  const entry = fs
    .readFileSync(path.join(ASSETS, "components/components.css"), "utf8")
    .replace(/@import\s+"[^"]+";/g, "");
  return strip([tokens, ...parts, entry].join("\n"));
}

const NOW = Date.parse("2026-10-23T06:00:00Z");

const READY_NODE = {
  id: "node_a",
  name: "本机 CodeBuddy",
  adapter: "codebuddy",
  presence: { online: true, last_seen: "2026-10-23T05:59:30Z" },
  auth: { state: "ready" },
  capability: {
    declared: ["non_interactive", "structured_events"],
    verified: ["non_interactive", "structured_events"],
    cli_version: "1.4.0",
    verified_version: "1.4.0",
    verified_at: "2026-10-23T05:30:00Z",
  },
  slots: { total: 2, busy: 0 },
  intake: "open",
};

const BLOCKED_NODE = {
  ...READY_NODE,
  id: "node_b",
  name: "备用节点 Trae",
  auth: { state: "needs-login" },
};

async function mount(page, html, theme = "dark") {
  await page.setContent(
    `<!DOCTYPE html><html data-theme="${theme}" lang="zh-CN"><head><meta charset="utf-8">` +
    `<style>${loadCSS()}</style></head><body><div class="page">${html}</div></body></html>`
  );
}

const rgb = hex => {
  const h = hex.replace("#", "");
  const full = h.length === 3 ? h.split("").map(c => c + c).join("") : h;
  return `rgb(${parseInt(full.slice(0, 2), 16)}, ${parseInt(full.slice(2, 4), 16)}, ${parseInt(full.slice(4, 6), 16)})`;
};

const DARK_OK = rgb("#9cdbb0");
const LIGHT_OK = rgb("#267044");

test.describe("VN-721 节点列表与能力详情", () => {
  test("VN-721 列表在深色主题下复用共享组件与 token", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderNodeList({ nodes: [READY_NODE, BLOCKED_NODE] }, { lang: "zh", now: NOW }));

    await expect(page.locator(".table")).toBeVisible();
    // 用精确文案：'不可接任务' 包含 '可接任务'，模糊匹配会撞。
    await expect(page.locator(".badge").filter({ hasText: /^可接任务$/ })).toHaveCSS("color", DARK_OK);
    // 四维表头都在，不把在线当成唯一判据
    for (const label of ["在线", "认证", "能力", "容量", "接收"]) {
      await expect(page.locator(".table th", { hasText: label })).toHaveCount(1);
    }
    // 不可执行的节点必须写明卡点
    await expect(page.locator(".table td", { hasText: "认证未就绪" })).toHaveCount(1);
  });

  test("VN-721 浅色主题换用另一套 token，结构不变", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderNodeList({ nodes: [READY_NODE] }, { lang: "zh", now: NOW }), "light");
    await expect(page.locator(".badge").filter({ hasText: /^可接任务$/ })).toHaveCSS("color", LIGHT_OK);
    await expect(page.locator(".table")).toBeVisible();
  });

  test("VN-721 英文界面不撑破布局且不残留中文", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderNodeList({ nodes: [READY_NODE, BLOCKED_NODE] }, { lang: "en", now: NOW }));
    await expect(page.locator("h1")).toHaveText("Execution nodes");
    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    const bodyText = await page.locator("#page, body").first().innerText();
    expect(bodyText.replace(/本机 CodeBuddy|备用节点 Trae/g, "")).not.toMatch(/[\u4e00-\u9fa5]/);
  });

  test("VN-721 700px 窄屏无页面级横向滚动，表格在内容区滚动", async ({ page }) => {
    const view = await loadView();
    await page.setViewportSize({ width: 700, height: 900 });
    await mount(page, view.renderNodeList({ nodes: [READY_NODE, BLOCKED_NODE] }, { lang: "zh", now: NOW }));
    const state = await page.evaluate(() => ({
      doc: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      wrap: getComputedStyle(document.querySelector(".table-wrap")).overflowX,
    }));
    expect(state.doc).toBeLessThanOrEqual(0);
    expect(state.wrap).toBe("auto");
  });

  test("VN-721 详情页键盘可达：返回控件有焦点环并回到列表", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderNodeDetail(READY_NODE, { lang: "zh", now: NOW }));
    const back = page.locator("#nodes-back");
    await expect(back).toBeVisible();

    await page.locator("body").click({ position: { x: 2, y: 2 } });
    await page.keyboard.press("Tab");
    const style = await back.evaluate(el => {
      el.focus();
      const s = getComputedStyle(el);
      return { width: s.outlineWidth, offset: s.outlineOffset, color: s.outlineColor };
    });
    expect(style.width).toBe("2px");
    expect(style.offset).toBe("3px");
    expect(style.color).toBe(DARK_OK);
    await expect(back).toHaveAttribute("href", "#/nodes");
  });

  test("VN-721 六个状态各有独立文案，空态用虚线边框", async ({ page }) => {
    const view = await loadView();
    const states = {
      loading: { status: "loading" },
      empty: { nodes: [] },
      error: { status: "error" },
      expired: { status: "expired" },
      unauth: { status: "unauthenticated" },
      unknown: { nodes: [{ id: "n_unknown", name: "未探测节点", presence: undefined, auth: {}, slots: {} }] },
    };
    const texts = new Set();
    for (const [name, state] of Object.entries(states)) {
      await mount(page, view.renderNodeList(state, { lang: "zh", now: NOW }));
      const text = await page.locator("body").innerText();
      expect(text.trim().length, name + " 必须有文案").toBeGreaterThan(0);
      expect(texts.has(text), name + " 文案与其它状态重复").toBe(false);
      texts.add(text);

      if (name === "unknown") {
        await expect(page.locator(".badge").filter({ hasText: /^状态未知$/ })).toHaveCount(1);
        await expect(page.locator(".badge").filter({ hasText: /^可接任务$/ })).toHaveCount(0);
      } else if (name !== "loading") {
        await expect(page.locator(".empty-state")).toBeVisible();
        await expect(page.locator(".empty-state")).toHaveCSS("border-top-style", "dashed");
      }
    }
  });

  test("VN-721 版本失效与暂停接收在详情里分别可见", async ({ page }) => {
    const view = await loadView();
    const stale = {
      ...READY_NODE,
      capability: {
        declared: ["non_interactive"],
        verified: ["non_interactive"],
        cli_version: "2.0.0",
        verified_version: "1.4.0",
      },
    };
    await mount(page, view.renderNodeDetail(stale, { lang: "zh", now: NOW }));
    await expect(page.locator(".field-hint").filter({ hasText: "版本变化，需重新确认" })).toHaveCount(1);

    await mount(page, view.renderNodeDetail(
      { ...READY_NODE, intake: "paused", paused_reason: "维护窗口" },
      { lang: "zh", now: NOW }
    ));
    await expect(page.locator(".badge").filter({ hasText: /^暂停接收$/ })).toHaveCount(1);
    await expect(page.locator("dd").filter({ hasText: "维护窗口" })).toHaveCount(1);
  });
});
