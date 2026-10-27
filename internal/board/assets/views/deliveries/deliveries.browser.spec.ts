// VN-724 交付与验收详情页的真实浏览器验收（UI-C）。
//
// 验证 deliveries-view.mjs 渲染的产物／提交、验证对象、完成判定与追溯，
// 配 VN-702 的共享 token 与组件（不另造公共层）。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-724'

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
const loadView = () => import(pathToFileURL(path.join(VIEW_DIR, "deliveries-view.mjs")).href);

function loadCSS() {
  const strip = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, "");
  const parts = [
    fs.readFileSync(path.join(ASSETS, "styles/tokens.css"), "utf8"),
    ...COMPONENTS.map(f => fs.readFileSync(path.join(ASSETS, "components", f), "utf8")),
    fs.readFileSync(path.join(ASSETS, "components/components.css"), "utf8").replace(/@import\s+"[^"]+";/g, ""),
  ];
  return strip(parts.join("\n"));
}

const SHA = "a".repeat(64);

const delivery = (overrides = {}) => ({
  delivery_id: "dlv_001",
  task_instance: "ti_vn003c",
  task_version: 1,
  run_id: "run_a001",
  mode: "code",
  baseline_commit: "dedc919",
  candidate_commit: "abc1234",
  delivered_at: "2026-10-23T07:09:00Z",
  fingerprint: "fp_1",
  artifacts: [
    { artifact_id: "art_001", kind: "commit", ref: "abc1234", declared_by: "manual", exists: true, in_write_scope: true },
    {
      artifact_id: "art_002", kind: "file", ref: "docs/x.md", declared_by: "cli",
      exists: true, in_write_scope: true, sha256: SHA,
    },
  ],
  verification: [{
    verification_id: "vfy_001", command: "go test ./...", target_task_version: 1,
    target_commit: "abc1234", result: "pass",
    evidence: ["exit_code:0", "go test: 12 packages ok"], ran_at: "2026-10-23T07:10:00Z",
  }],
  review: { required: true, verdict: "approved", reviewer: "r1", reviewed_at: "2026-10-23T07:11:00Z" },
  merge: { required: true, merged: true, merged_commit: "def5678", merged_at: "2026-10-23T07:12:00Z" },
  completion: { decision: "done", decided_at: "2026-10-23T07:13:00Z", reason: "" },
  ...overrides,
});

const onlyExitCode = () => delivery({
  verification: [{
    verification_id: "vfy_002", command: "go test ./...", target_task_version: 1,
    target_commit: "abc1234", result: "pass", evidence: ["exit_code:0"],
    ran_at: "2026-10-23T07:10:00Z",
  }],
});

async function mount(page, html, theme = "dark") {
  await page.setContent(
    `<!DOCTYPE html><html data-theme="${theme}" lang="zh-CN"><head><meta charset="utf-8">` +
    `<style>${loadCSS()}</style></head><body><div class="page" id="page">${html}</div></body></html>`
  );
}

const rgb = (hex: string) => {
  const h = hex.replace("#", "");
  return `rgb(${parseInt(h.slice(0, 2), 16)}, ${parseInt(h.slice(2, 4), 16)}, ${parseInt(h.slice(4, 6), 16)})`;
};
const DARK_WARN = rgb("#e5bc78");
const DARK_OK = rgb("#9cdbb0");
const LIGHT_WARN = rgb("#8a601f");

test.describe("VN-724 交付与验收详情", () => {
  test("VN-724 产物、验证对象与结论逐条对上", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderDeliveryDetail(delivery(), {
      lang: "zh",
      run: { phase: "completed", exit_code: 0 },
      task_status: "done",
    }));

    // 验证表：每条结论都带着它测的对象
    const verification = page.locator("section").filter({ has: page.locator("h2", { hasText: "验证" }) });
    const headers = verification.locator(".table th");
    await expect(headers).toHaveCount(8);
    await expect(headers.nth(0)).toHaveText("命令");
    await expect(headers.nth(5)).toHaveText("证据");
    await expect(headers.nth(7)).toHaveText("可支撑完成");
    await expect(verification.locator(".table td").nth(0)).toHaveText("go test ./...");
    await expect(verification.locator(".table td").nth(2)).toHaveText("abc1234");
    await expect(verification.locator(".table td").nth(5)).toContainText("12 packages ok");
    await expect(verification.locator(".table")).toContainText("可支撑");

    // 产物表：提交与文件都带核实结果
    const artifacts = page.locator("section").filter({ has: page.locator("h2", { hasText: "产物" }) });
    await expect(artifacts.locator(".table")).toContainText("docs/x.md");
    await expect(artifacts.locator(".table")).toContainText("工具声明（候选）");

    // 完成判定与四阶段都在
    await expect(page.locator(".badge").filter({ hasText: /^已完成$/ })).toBeVisible();
    await expect(page.locator("body")).toContainText("运行结束");
    await expect(page.locator("body")).toContainText("任务完成");
  });

  test("VN-724 只有退出码时判为待验收，不显示已完成", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderDeliveryDetail(onlyExitCode(), {
      lang: "zh",
      run: { phase: "completed", exit_code: 0 },
      task_status: "in_progress",
    }));

    const pending = page.locator(".badge").filter({ hasText: /^待验收$/ }).first();
    await expect(pending).toBeVisible();
    await expect(pending).toHaveCSS("color", DARK_WARN);
    await expect(page.locator(".badge").filter({ hasText: /^已完成$/ })).toHaveCount(0);
    // 结论写明原因，而不是只说一句「未通过」
    await expect(page.locator("body")).toContainText("只有退出码不算通过");
    await expect(page.locator("body")).toContainText("运行结束不是验收通过");
    // 运行结束与任务完成必须分开
    await expect(page.locator("section").filter({ hasText: "阶段" }).locator(".table")).toBeVisible();
  });

  test("VN-724 旧版本与旧提交的验证不支撑完成，浅色主题换 token", async ({ page }) => {
    const view = await loadView();
    const stale = delivery({
      verification: [
        { ...delivery().verification[0], verification_id: "vfy_old_version", target_task_version: 2 },
        { ...delivery().verification[0], verification_id: "vfy_old_commit", target_commit: "0000000" },
      ],
    });

    await mount(page, view.renderDeliveryDetail(stale, { lang: "zh" }), "light");
    const verification = page.locator("section").filter({ has: page.locator("h2", { hasText: "验证" }) });
    await expect(verification.locator(".table")).toContainText("目标版本不是本交付的版本");
    await expect(verification.locator(".table")).toContainText("不能给新提交背书");
    await expect(verification.locator(".table tbody tr")).toHaveCount(2);
    for (const row of await verification.locator(".table tbody tr").all()) {
      await expect(row.locator("td").last()).toContainText("不支撑");
    }

    const pending = page.locator(".badge").filter({ hasText: /^待验收$/ }).first();
    await expect(pending).toHaveCSS("color", LIGHT_WARN);
  });

  test("VN-724 英文界面不残留中文且无页面级横向滚动", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderDeliveryDetail(delivery(), { lang: "en", run: { phase: "completed", exit_code: 0 } }));
    await expect(page.locator(".deliveries-meta")).toBeVisible();
    await expect(page.locator("body")).toContainText("Review and merge");

    const overflow = await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
    expect(await page.locator("body").innerText()).not.toMatch(/[\u4e00-\u9fa5]/);
  });

  test("VN-724 700px 窄屏：页面不横向滚动，表格在内容区滚动", async ({ page }) => {
    const view = await loadView();
    await page.setViewportSize({ width: 700, height: 900 });
    await mount(page, view.renderDeliveryDetail(delivery(), { lang: "zh", run: { phase: "completed", exit_code: 0 } }));
    const state = await page.evaluate(() => ({
      doc: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      wraps: [...document.querySelectorAll(".table-wrap")].map(el => getComputedStyle(el).overflowX),
    }));
    expect(state.doc).toBeLessThanOrEqual(0);
    expect(state.wraps.length).toBeGreaterThan(0);
    expect(state.wraps.every(value => value === "auto")).toBe(true);
  });

  test("VN-724 键盘可达：重新读取按钮沿用共享焦点环", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderDeliveryDetail(delivery(), { lang: "zh" }));
    const button = page.locator('[data-action="recheck"]').first();
    await expect(button).toBeVisible();
    const style = await button.evaluate((el: HTMLElement) => {
      el.focus();
      const s = getComputedStyle(el);
      return { width: s.outlineWidth, offset: s.outlineOffset, color: s.outlineColor };
    });
    expect(style.width).toBe("2px");
    expect(style.offset).toBe("3px");
    expect(style.color).toBe(DARK_OK);
  });

  test("VN-724 加载／空／错误／过期／无法判定五态可核对", async ({ page }) => {
    const view = await loadView();
    const states = {
      loading: { status: "loading" },
      empty: { deliveries: [] },
      error: { status: "error" },
      expired: { status: "expired" },
      unauth: { status: "unauthenticated" },
    };
    const texts = new Set<string>();
    for (const [name, state] of Object.entries(states)) {
      await mount(page, view.renderDeliveryList(state, { lang: "zh" }));
      const text = (await page.locator("body").innerText()).trim();
      expect(text.length, name).toBeGreaterThan(0);
      expect(texts.has(text), name + " 与其他状态重复").toBe(false);
      texts.add(text);
    }

    // 绑定缺失走 unknown：不给结论，只给缺失字段与处理入口
    await mount(page, view.renderDeliveryDetail(delivery({ task_version: null, run_id: "" }), { lang: "zh" }));
    await expect(page.locator('[data-decision="unknown"]')).toBeVisible();
    await expect(page.locator("body")).toContainText("无法判定");
    await expect(page.locator("body")).toContainText("缺失字段");
    await expect(page.locator('[data-action="recheck"]')).toBeVisible();
    await expect(page.locator(".badge").filter({ hasText: /^已完成$/ })).toHaveCount(0);
  });

  test("VN-724 产物引用与证据只当文本，不生成 DOM 元素", async ({ page }) => {
    const view = await loadView();
    await mount(page, view.renderDeliveryDetail(delivery({
      artifacts: [{
        artifact_id: "art_x", kind: "file", ref: '<img src=x onerror="window.__pwned=1">',
        declared_by: "cli", exists: true, in_write_scope: true, sha256: SHA,
      }],
      verification: [{
        ...delivery().verification[0],
        evidence: ['<script>window.__pwned=2</script>', "go test: 3 ok"],
      }],
    }), { lang: "zh" }));

    await expect(page.locator("img")).toHaveCount(0);
    await expect(page.locator("script")).toHaveCount(0);
    expect(await page.evaluate(() => (window as any).__pwned)).toBeUndefined();
    await expect(page.locator("body")).toContainText('<img src=x onerror="window.__pwned=1">');
  });
});
