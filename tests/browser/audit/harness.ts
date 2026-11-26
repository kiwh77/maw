// Workbench 视觉与交互审计基座（VN-984）。
//
// 目标：把「页面上真的长什么样」拍成可复现的断言，而不是靠人工点一遍。
//
// 为什么直接从磁盘喂源模块、而不是等构建产物：审计要能指到具体的视图文件，
// 而且 `assets/dist/board-web.js` 是打包产物（`internal/board/web`），
// 源模块改完还没重建时用它审计会审到旧代码。这里按需把 `assets/` 下的
// 模块喂给浏览器，页面本身仍是生产 `page.html`（由 tests/browser 基座提供）。
//
// 约定：先断言数据与投影状态，再截图。截图只是证据，判断依据是断言。

import fs from "node:fs";
import path from "node:path";

/** 仓库内 `internal/board/assets` 的绝对路径。 */
export const ASSETS_DIR = path.resolve(__dirname, "../../../internal/board/assets");

/** 截图与报告落盘目录（本卡写盘范围内）。 */
export const ARTIFACT_DIR = path.resolve(__dirname, "../../../docs/verification/vnext/frontend");

/** 只喂这些前缀下的文件；其余请求交给基座服务。 */
const SERVED_PREFIXES = [
  "/assets/shell/",
  "/assets/i18n/",
  "/assets/views/",
  "/assets/styles/",
  "/assets/components/",
];

function contentTypeOf(file: string) {
  if (file.endsWith(".mjs") || file.endsWith(".js")) return "text/javascript; charset=utf-8";
  if (file.endsWith(".css")) return "text/css; charset=utf-8";
  return "text/plain; charset=utf-8";
}

/** 把 `assets/` 下的源模块按需从磁盘喂给浏览器。 */
export async function serveAssetModules(page: any) {
  await page.route("**/*", async (route: any) => {
    const pathname = new URL(route.request().url()).pathname;
    if (!SERVED_PREFIXES.some(prefix => pathname.startsWith(prefix))) return route.continue();
    const file = path.join(ASSETS_DIR, pathname.replace(/^\/assets\//, ""));
    if (!fs.existsSync(file)) return route.fulfill({ status: 404, body: "missing" });
    return route.fulfill({ status: 200, contentType: contentTypeOf(file), body: fs.readFileSync(file) });
  });
}

export type MountSpec = {
  /** 打开哪个地址（决定初始路由）。 */
  path?: string;
  lang?: string;
  readOnly?: boolean;
  theme?: "dark" | "light" | "system";
  /** 每个数据种类对应的载荷；未提供的种类会用下方的延迟/失败开关。 */
  payloads?: Record<string, unknown>;
  /** 能力发现结果；null 表示查不到。 */
  capabilities?: Record<string, unknown> | null;
  /** 该种类故意失败（模拟断线）。 */
  failing?: string[];
  /** 该种类故意不返回（模拟加载中）。 */
  hanging?: string[];
  /** 该种类的延迟毫秒数，用来构造乱序响应。 */
  delays?: Record<string, number>;
};

/**
 * 挂载工作台并等到首屏稳定。
 *
 * 返回的句柄上带 `dataCalls`，供「不该发请求」这类断言使用。
 */
export async function mountWorkbench(page: any, spec: MountSpec = {}) {
  await serveAssetModules(page);
  await page.goto(spec.path || "/");
  if (spec.theme && spec.theme !== "system") {
    await page.evaluate((theme: string) => {
      document.documentElement.dataset.theme = theme;
    }, spec.theme);
  }
  const styles = loadWorkbenchCSS();
  await page.evaluate(async (input: { spec: MountSpec; styles: string }) => {
    const spec = input.spec;
    const mod = await import("/assets/shell/board-shell.mjs");
    const calls: string[] = [];
    (window as any).__dataCalls = calls;
    const sources: Record<string, any> = {};
    const kinds = new Set<string>([
      ...Object.keys(spec.payloads || {}),
      ...(spec.failing || []),
      ...(spec.hanging || []),
      ...Object.keys(spec.delays || {}),
    ]);
    kinds.forEach(kind => {
      sources[kind] = async () => {
        calls.push(kind);
        const delay = (spec.delays || {})[kind] || 0;
        if (delay) await new Promise(resolve => setTimeout(resolve, delay));
        if ((spec.failing || []).indexOf(kind) >= 0) throw new Error("disconnected");
        if ((spec.hanging || []).indexOf(kind) >= 0) return new Promise(() => {});
        return (spec.payloads || {})[kind];
      };
    });
    // 未声明的种类一律当作「中枢未提供」，避免审计误打到真实网络。
    const proxy = new Proxy(sources, {
      get(target: Record<string, any>, key: string) {
        if (key in target) return target[key];
        if (key === "capabilities") return async () => spec.capabilities ?? null;
        return async () => ({ unavailable: true });
      },
    });
    (window as any).__shell = mod.mountShell({
      sources: proxy,
      lang: spec.lang || "zh",
      readOnly: spec.readOnly,
      styles: input.styles,
    });
    await new Promise(resolve => setTimeout(resolve, 60));
  }, { spec, styles });
}

/**
 * 拼出 Workbench 的完整样式文本。
 *
 * 真机上这些样式由 bundle 注入；审计直接从磁盘拼，保证「审到的样式」就是
 * `assets/` 里的当前内容。`@import` 一律展开（浏览器里相对路径会解析到
 * 文档根，指不到 `assets/components/`）。
 */
export function loadWorkbenchCSS() {
  const files: string[] = [];
  const collect = (dir: string) => {
    fs.readdirSync(dir, { withFileTypes: true })
      .sort((a, b) => (a.name < b.name ? -1 : 1))
      .forEach(entry => {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) collect(full);
        else if (entry.name.endsWith(".css")) files.push(full);
      });
  };
  ["styles", "components", "shell", "views"].forEach(name => {
    const dir = path.join(ASSETS_DIR, name);
    if (fs.existsSync(dir)) collect(dir);
  });
  return files
    .map(file => fs.readFileSync(file, "utf8").replace(/@import\s+[^;]+;/g, ""))
    .join("\n");
}

/** 截图（先断言再截图，这里只负责落盘）。 */
export async function shot(page: any, name: string) {
  const dir = path.join(ARTIFACT_DIR, "screenshots");
  fs.mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: path.join(dir, name), fullPage: true });
}

/** 页面级横向溢出像素数（>0 即出现页面级横向滚动）。 */
export function overflowOf(page: any) {
  return page.evaluate(() =>
    document.documentElement.scrollWidth - document.documentElement.clientWidth);
}
