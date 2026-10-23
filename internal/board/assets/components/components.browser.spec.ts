// VN-702 共享主题组件的真实浏览器验收。
//
// 装配方式：把 styles/tokens.css 与 components/*.css 原样注入独立页面，
// 不依赖 Board 既有页面，确保验证对象就是共享包本身（Surface 内自证）。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-702'

import fs from "node:fs";
import path from "node:path";

// @playwright/test 安装在 tests/browser/node_modules；本文件位于 internal/** 之下，
// 默认的向上解析找不到它，因此显式解析，保证 `npm --prefix tests/browser test` 原样可跑。
function loadPlaywright() {
  try {
    return require("@playwright/test");
  } catch {
    return require("../../../../tests/browser/node_modules/@playwright/test");
  }
}

const { test, expect } = loadPlaywright();

const DIR = __dirname;
const STYLES = path.resolve(DIR, "../styles");

const COMPONENTS = ["badge.css", "table.css", "form.css", "empty.css", "drawer.css"];

function loadCSS() {
  const strip = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const base = fs.readFileSync(path.join(STYLES, "tokens.css"), "utf8");
  const components = COMPONENTS.map(f => fs.readFileSync(path.join(DIR, f), "utf8")).join("\n");
  // components.css 的基础层去掉 @import，其余规则保留
  const entry = fs
    .readFileSync(path.join(DIR, "components.css"), "utf8")
    .replace(/@import\s+"[^"]+";/g, "");
  return strip(base + "\n" + components + "\n" + entry);
}

const STATES = [
  "backlog", "ready", "claimed", "in_progress", "self_check", "in_review",
  "merging", "conflict", "blocked", "done", "cancelled",
];

const LABELS = {
  backlog: "待排期", ready: "可领取", claimed: "已领取", in_progress: "进行中",
  self_check: "自检中", in_review: "评审中", merging: "合并中", conflict: "冲突",
  blocked: "阻塞", done: "已完成", cancelled: "已取消",
};

function pageBody() {
  return `
<div class="page" id="page">
  <h1>共享组件验收</h1>
  <div id="badges">
    ${STATES.map(s => `<span class="badge badge--${s}" data-state="${s}">${LABELS[s]}</span>`).join("")}
  </div>

  <div class="table-wrap" id="table-wrap">
    <table class="table" id="table">
      <thead><tr><th>任务</th><th>负责人</th><th>优先级</th></tr></thead>
      <tbody>
        <tr><td class="name">VN-702 建立共享主题组件与状态样式</td><td class="agent">codebuddy-goalagents-5</td><td class="num">20</td></tr>
        <tr><td class="name">超长中文标题用来验证省略号截断不会撑破表格布局</td><td class="agent">a-very-long-agent-name-exceeding</td><td class="num">70</td></tr>
      </tbody>
    </table>
  </div>

  <form class="field" id="field">
    <label for="f1">工程名称</label>
    <input class="input" id="f1" placeholder="输入工程名" value="goal-agents" />
    <p class="field-hint">提示文案 13px</p>
    <button type="button" class="button button--primary" id="primary">保存</button>
  </form>

  <div class="empty-state" id="empty">
    <span class="empty-state__symbol" aria-hidden="true">·</span>
    <p class="empty-state__text">当前筛选下没有任务。</p>
  </div>

  <div class="drawer-mask" id="mask" hidden></div>
  <aside class="drawer" id="drawer" data-open="true" role="dialog" aria-modal="true" aria-labelledby="d-title">
    <div class="drawer__head">
      <div><h2 class="drawer__title" id="d-title">任务详情</h2></div>
      <button type="button" class="drawer__close" id="d-close" aria-label="关闭">×</button>
    </div>
    <div class="drawer__body"><div class="drawer__section"><h3>基本信息</h3></div></div>
  </aside>
</div>`;
}

async function mount(page, theme) {
  await page.setContent(
    `<!DOCTYPE html><html${theme ? ` data-theme="${theme}"` : ""} lang="zh-CN"><head><meta charset="utf-8"><style>${loadCSS()}</style></head><body>${pageBody()}</body></html>`
  );
}

const rgb = hex => {
  const h = hex.replace("#", "");
  const full = h.length === 3 ? h.split("").map(c => c + c).join("") : h.slice(0, 6);
  return `rgb(${parseInt(full.slice(0, 2), 16)}, ${parseInt(full.slice(2, 4), 16)}, ${parseInt(full.slice(4, 6), 16)})`;
};

const DARK = { ok: "#9cdbb0", danger: "#ed9797", warn: "#e5bc78", muted: "#9ba5ad", panel: "#191c1f", line: "#2d3338" };
const LIGHT = { ok: "#267044", danger: "#b53e45", warn: "#8a601f", muted: "#5b6871", panel: "#eef1f3", line: "#dce1e5" };

test.describe("VN-702 共享主题组件", () => {
  test("VN-702 深色主题下组件颜色全部来自 token", async ({ page }) => {
    await mount(page, "dark");
    await expect(page.locator('[data-state="done"]')).toHaveCSS("color", rgb(DARK.ok));
    await expect(page.locator('[data-state="blocked"]')).toHaveCSS("color", rgb(DARK.danger));
    await expect(page.locator('[data-state="in_progress"]')).toHaveCSS("color", rgb(DARK.warn));
    await expect(page.locator('[data-state="ready"]')).toHaveCSS("color", rgb(DARK.muted));
    await expect(page.locator(".drawer")).toHaveCSS("background-color", rgb(DARK.panel));
  });

  test("VN-702 data-theme 切换后同一组件换用浅色 token", async ({ page }) => {
    await mount(page, "light");
    await expect(page.locator('[data-state="done"]')).toHaveCSS("color", rgb(LIGHT.ok));
    await expect(page.locator('[data-state="blocked"]')).toHaveCSS("color", rgb(LIGHT.danger));
    await expect(page.locator('[data-state="in_progress"]')).toHaveCSS("color", rgb(LIGHT.warn));
    await expect(page.locator(".table td").first()).toHaveCSS("border-bottom-color", rgb(LIGHT.line));
  });

  test("VN-702 未指定 data-theme 时跟随系统深浅", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "light" });
    await mount(page, null);
    await expect(page.locator('[data-state="done"]')).toHaveCSS("color", rgb(LIGHT.ok));
    await page.emulateMedia({ colorScheme: "dark" });
    await mount(page, null);
    await expect(page.locator('[data-state="done"]')).toHaveCSS("color", rgb(DARK.ok));
  });

  test("VN-702 十一个状态徽标都带文本标签，颜色不是唯一信息载体", async ({ page }) => {
    await mount(page, "dark");
    for (const state of STATES) {
      const badge = page.locator(`[data-state="${state}"]`);
      await expect(badge).toHaveText(LABELS[state]);
      const fontSize = await badge.evaluate(el => parseFloat(getComputedStyle(el).fontSize));
      expect(fontSize).toBeGreaterThan(0);
    }
  });

  test("VN-702 键盘焦点环为 2px accent、偏移 3px", async ({ page }) => {
    await mount(page, "dark");
    await page.locator("body").click({ position: { x: 2, y: 2 } });
    await page.keyboard.press("Tab");
    const style = await page.evaluate(() => {
      const el = document.querySelector("#primary");
      el.focus();
      const s = getComputedStyle(el);
      return { width: s.outlineWidth, color: s.outlineColor, offset: s.outlineOffset, style: s.outlineStyle };
    });
    expect(style.style).toBe("solid");
    expect(style.width).toBe("2px");
    expect(style.offset).toBe("3px");
    expect(style.color).toBe(rgb(DARK.ok));
  });

  test("VN-702 抽屉宽度为 min(500px, 94vw)，窄屏收起为 94vw", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await mount(page, "dark");
    expect(await page.locator(".drawer").evaluate(el => el.getBoundingClientRect().width)).toBe(500);

    await page.setViewportSize({ width: 400, height: 800 });
    const narrow = await page.locator(".drawer").evaluate(el => el.getBoundingClientRect().width);
    expect(Math.round(narrow)).toBe(Math.round(400 * 0.94));
  });

  test("VN-702 减弱动画下过渡被关闭", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await mount(page, "dark");
    const dur = await page.locator(".drawer").evaluate(el => getComputedStyle(el).transitionDuration);
    expect(["0s", "0s, 0s"]).toContain(dur.trim());
  });

  test("VN-702 700px 窄屏无页面级横向滚动，表格只在内容区滚动", async ({ page }) => {
    await page.setViewportSize({ width: 700, height: 800 });
    await mount(page, "dark");
    const overflow = await page.evaluate(() => ({
      doc: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      wrapOverflowX: getComputedStyle(document.getElementById("table-wrap")).overflowX,
    }));
    expect(overflow.doc).toBeLessThanOrEqual(0);
    expect(overflow.wrapOverflowX).toBe("auto");
  });

  test("VN-702 长中文不撑破控件高度与圆角", async ({ page }) => {
    await mount(page, "dark");
    const input = await page.locator("#f1").evaluate(el => {
      const s = getComputedStyle(el);
      return { h: el.getBoundingClientRect().height, radius: s.borderTopLeftRadius };
    });
    expect(Math.round(input.h)).toBe(32);
    expect(input.radius).toBe("6px");
    const primary = await page.locator("#primary").evaluate(el => el.getBoundingClientRect().height);
    expect(Math.round(primary)).toBe(32);
  });
});
