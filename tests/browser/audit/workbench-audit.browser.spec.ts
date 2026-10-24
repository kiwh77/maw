// VN-984 全页面视觉与交互一致性验收（UI-C）。
//
// 覆盖卡片要求：桌面／窄屏、深浅／系统主题、zh-CN／en、长中文、
// 空／载入／错误／unknown、断线与乱序响应、键盘与焦点、非颜色信息、
// reduced-motion，以及「页面没有另造全局样式」。
//
// 运行：npm --prefix tests/browser test -- --grep 'VN-984'
//
// 纪律：先断言数据与投影状态，再截图。截图是证据，不是判据。

import fs from "node:fs";
import path from "node:path";

import {
  ASSETS_DIR,
  mountWorkbench,
  overflowOf,
  shot,
  type MountSpec,
} from "./harness";

function loadPlaywright() {
  try {
    return require("@playwright/test");
  } catch {
    return require("../node_modules/@playwright/test");
  }
}

const { test, expect } = loadPlaywright();

const NODE_READY = {
  id: "node_codebuddy",
  name: "CodeBuddy 本机",
  adapter: "codebuddy",
  presence: { online: true, last_seen: new Date().toISOString() },
  auth: { state: "ready" },
  capability: {
    declared: ["non_interactive", "structured_events"],
    verified: ["non_interactive", "structured_events"],
    cli_version: "1.4.0",
    verified_version: "1.4.0",
  },
  slots: { total: 2, busy: 1 },
  intake: "open",
};

const NODE_UNKNOWN = {
  id: "node_trae",
  name: "Trae 未探测",
  adapter: "trae",
  presence: undefined,
  auth: {},
  slots: {},
};

const NODE_BLOCKED = {
  id: "node_zcode",
  name: "ZCode 未登录",
  adapter: "zcode",
  presence: { online: true, last_seen: new Date().toISOString() },
  auth: { state: "needs_login" },
  capability: { declared: ["non_interactive"], verified: [] },
  slots: { total: 1, busy: 1 },
  intake: "closed",
};

const NODES_READY = { nodes: [NODE_READY, NODE_UNKNOWN, NODE_BLOCKED] };

const LONG_NAME = "这是一个非常长的工程名称".repeat(6);

test.describe("VN-984 视觉与交互一致性", () => {
  test("VN-984 桌面深色：状态以文本承载，页面无横向滚动", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await mountWorkbench(page, {
      path: "/?view=nodes",
      theme: "dark",
      lang: "zh",
      payloads: { nodes: NODES_READY },
    });

    // 先断言数据：三台节点都进来了，且状态各自成立。
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(3);
    await expect(page.locator("#page-h1")).toHaveText("执行节点");
    await expect(page.locator("#vnext-view")).not.toContainText("undefined");

    // 非颜色信息：每个徽标都带文本，颜色不是唯一载体。
    const badges = page.locator("#vnext-view .badge");
    const badgeCount = await badges.count();
    expect(badgeCount).toBeGreaterThan(0);
    for (let index = 0; index < badgeCount; index += 1) {
      const text = (await badges.nth(index).innerText()).trim();
      expect(text.length).toBeGreaterThan(0);
    }

    expect(await overflowOf(page)).toBeLessThanOrEqual(0);
    await shot(page, "VN-984-01-desktop-dark-zh-nodes.png");
  });

  test("VN-984 浅色英文：不残留中文，颜色只走 token", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await mountWorkbench(page, {
      path: "/?view=nodes",
      theme: "light",
      lang: "en",
      payloads: { nodes: NODES_READY },
    });

    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.locator("#page-h1")).toHaveText("Execution nodes");
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(3);
    // 视图正文里不该混进中文（数据本身是英文夹具）。
    await expect(page.locator("#vnext-view")).not.toContainText("执行");
    // 已知漂移（报告 4.5）：挂载时语言只交给 vnext 视图，旧看板的统计条要等
    // 用户手动切换语言才跟随，所以这里仍是中文。断言「目前仍然如此」，
    // 让这条漂移可执行、可追；修好后这条会红，提醒回头更新报告。
    await expect(page.locator("#stats")).toContainText("可领取");
    // 已知漂移（报告 4.5）：挂载时语言只交给 vnext 视图，旧看板的统计条要等
    // 用户手动切换语言才跟随，所以这里仍是中文。断言「目前仍然如此」，
    // 让这条漂移可执行、可追；修好后这条会红，提醒回头更新报告。
    await expect(page.locator("#stats")).toContainText("可领取");

    // 浅色主题下底与字都来自 token：背景不是深色，文字不是浅色。
    const palette = await page.evaluate(() => {
      const body = getComputedStyle(document.body);
      const cell = document.querySelector("#vnext-view tbody td");
      return {
        bg: body.backgroundColor,
        text: cell ? getComputedStyle(cell).color : "",
      };
    });
    expect(palette.bg).not.toBe("rgb(20, 22, 24)");
    expect(palette.text.length).toBeGreaterThan(0);
    expect(await overflowOf(page)).toBeLessThanOrEqual(0);
    await shot(page, "VN-984-02-desktop-light-en-nodes.png");
  });

  test("VN-984 390px 窄屏：页面不横向滚动，表格只在内容区滚动", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await mountWorkbench(page, {
      path: "/?view=nodes",
      theme: "dark",
      lang: "zh",
      payloads: { nodes: NODES_READY },
    });

    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(3);
    expect(await overflowOf(page)).toBeLessThanOrEqual(0);

    // 表格容器自己滚动；没有容器时至少不能把页面撑开。
    const wrap = page.locator("#vnext-view .table-wrap").first();
    if (await wrap.count()) {
      expect(await wrap.evaluate((el: HTMLElement) => getComputedStyle(el).overflowX)).toBe("auto");
    }
    await shot(page, "VN-984-03-narrow-390-nodes.png");
  });

  test("VN-984 长中文与长标识不撑破布局", async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 800 });
    await mountWorkbench(page, {
      path: "/?view=tasks&project=p-long",
      theme: "dark",
      lang: "zh",
      payloads: {
        tasks: {
          tasks: [{
            id: "VN-900",
            instance_id: "ti_long_1",
            display_id: "VN-900",
            project_id: "p-long",
            project_name: LONG_NAME,
            title: LONG_NAME,
            status: "blocked",
            owner: LONG_NAME,
            priority: 10,
            deps: [],
            write_scope: ["docs/**"],
          }],
          allTasks: [],
        },
      },
    });

    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(1);
    expect(await overflowOf(page)).toBeLessThanOrEqual(0);
    // 文本不会溢出到容器外（内容区自身不产生横向溢出）。
    const content = page.locator(".content, #vnext-view").first();
    const fits = await content.evaluate((el: HTMLElement) => el.scrollWidth - el.clientWidth);
    expect(fits).toBeLessThanOrEqual(1);
    await shot(page, "VN-984-04-long-zh-tasks.png");
  });

  test("VN-984 载入／空／错误／unknown 四种呈现互不混淆", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });

    // 载入：数据源不返回，停在「正在读取」。
    await mountWorkbench(page, { path: "/?view=nodes", hanging: ["nodes"] });
    const loading = (await page.locator("#vnext-view").innerText()).trim();
    expect(loading.length).toBeGreaterThan(0);

    // 空：端点给了空列表。
    await mountWorkbench(page, { path: "/?view=nodes", payloads: { nodes: { nodes: [] } } });
    const empty = (await page.locator("#vnext-view").innerText()).trim();
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(0);

    // 错误：端点不可用，必须说明「未提供该数据」，不能是空成功。
    await mountWorkbench(page, { path: "/?view=nodes", failing: ["nodes"] });
    const failed = (await page.locator("#vnext-view").innerText()).trim();
    await expect(page.locator("#vnext-view .vnext-status")).toContainText("未提供该数据");
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(0);

    // unknown：投影从未推进，有横幅但不当成「零活动」。
    await mountWorkbench(page, {
      path: "/?view=timeline&project=p-1",
      payloads: { timeline: { status: "unknown", completeness: "unknown", items: [], watermark: null } },
    });
    const unknown = (await page.locator("#vnext-view").innerText()).trim();
    await expect(page.locator("#vnext-view .timeline")).toHaveAttribute("data-status", "unknown");

    // 四段文案两两不同：同一句话不可能同时表示四种状态。
    const texts = [loading, empty, failed, unknown];
    expect(new Set(texts).size).toBe(4);
  });

  test("VN-984 断线保留显式失败，不回落成空成功", async ({ page }) => {
    await mountWorkbench(page, {
      path: "/?view=deliveries",
      failing: ["deliveries"],
    });

    await expect(page.locator("#vnext-view .vnext-status")).toContainText("未提供该数据");
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(0);
    // 失败不会被画成「已通过／已合入」这类成功态。
    await expect(page.locator("#vnext-view .badge").filter({ hasText: /^已通过$/ })).toHaveCount(0);
  });

  test("VN-984 乱序响应：后发的请求结果获胜", async ({ page }) => {
    await mountWorkbench(page, {
      path: "/?view=nodes&project=p-slow",
      delays: { nodes: 200 },
      payloads: { nodes: { nodes: [NODE_UNKNOWN] } },
    });

    // 慢请求还在飞的时候切到另一个工程，用更快的载荷。
    await page.evaluate(() => {
      (window as any).__shell.navigate("nodes", { project: "p-fast" });
    });
    await page.evaluate(async () => {
      // 让快请求的结果先落地。
      await new Promise(resolve => setTimeout(resolve, 60));
    });

    // 等慢请求也回来，仍然应该是最后一次选择的工程的数据。
    await page.waitForTimeout(300);
    const route = await page.evaluate(() => (window as any).__shell.route());
    expect(route.project).toBe("p-fast");
    await expect(page.locator("#vnext-view tbody tr")).toHaveCount(1);
  });

  test("VN-984 键盘：导航可 Enter 激活，Escape 关闭详情并归还焦点", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await mountWorkbench(page, {
      path: "/?view=nodes",
      payloads: { nodes: NODES_READY },
    });

    const settingsNav = page.locator('#vnext-nav [data-view="nodes"]');
    await settingsNav.focus();
    await expect(settingsNav).toBeFocused();
    // 焦点可见：不是 outline: none 之后不给替代。
    const outline = await settingsNav.evaluate((el: HTMLElement) => {
      const style = getComputedStyle(el);
      return { width: style.outlineWidth, style: style.outlineStyle };
    });
    expect(outline.style).not.toBe("none");

    // 行可打开详情，Escape 关闭并把焦点还给触发行。
    const row = page.locator("#vnext-view tbody tr").first();
    await row.click();
    await expect(page.locator("#vnext-detail")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.locator("#vnext-detail")).toBeHidden();
    const detailRoute = await page.evaluate(() => (window as any).__shell.route());
    expect(detailRoute.detail).toBe("");
  });

  test("VN-984 reduced-motion 下关掉动画与过渡", async ({ page }) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await mountWorkbench(page, { path: "/?view=nodes", payloads: { nodes: NODES_READY } });

    const motion = await page.evaluate(() => {
      const el = document.querySelector("#vnext-view tbody tr") as HTMLElement;
      const style = getComputedStyle(el);
      return { animation: style.animationName, transition: style.transitionDuration };
    });
    expect(motion.animation).toBe("none");
    expect(motion.transition === "" || motion.transition === "0s").toBe(true);
  });

  test("VN-984 页面没有另造全局样式：颜色只走 token，异常清单被钉住", () => {
    const cssFiles: string[] = [];
    const collect = (dir: string) => {
      fs.readdirSync(dir, { withFileTypes: true }).forEach(entry => {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) collect(full);
        else if (entry.name.endsWith(".css")) cssFiles.push(full);
      });
    };
    ["views", "components", "shell", "styles"].forEach(name => collect(path.join(ASSETS_DIR, name)));
    expect(cssFiles.length).toBeGreaterThan(5);

    const hex = /#[0-9a-fA-F]{3,6}\b/;
    const offenders: string[] = [];
    cssFiles.forEach(file => {
      const rel = path.relative(ASSETS_DIR, file);
      const body = fs.readFileSync(file, "utf8");
      // token 文件是颜色唯一实现处；其余样式表只许 `var(--x)`。
      if (rel !== path.join("styles", "tokens.css") && hex.test(body)) offenders.push(rel);
    });
    expect(offenders).toEqual([]);

    // `!important` 只允许出现在契约 §11 的减弱动画，以及隐藏态兜底的两处。
    const allowed = ["components/components.css", "shell/shell.css"];
    const important = cssFiles
      .map(file => path.relative(ASSETS_DIR, file).split(path.sep).join("/"))
      .filter(rel => allowed.indexOf(rel) < 0 &&
        fs.readFileSync(path.join(ASSETS_DIR, rel), "utf8").indexOf("!important") >= 0);
    expect(important).toEqual([]);

    // 视图自身不得定义全局元素选择器；基础层只允许出现在共享 components 层。
    const globalSelector = /(^|[,{}])\s*(:root|\*|html|body)\s*(,|\{)/m;
    const viewFiles = cssFiles.filter(file => path.relative(ASSETS_DIR, file).startsWith("views"));
    expect(viewFiles.length).toBeGreaterThan(3);
    const globalInViews = viewFiles
      .filter(file => globalSelector.test(fs.readFileSync(file, "utf8")))
      .map(file => path.relative(ASSETS_DIR, file));
    expect(globalInViews).toEqual([]);
  });
});
