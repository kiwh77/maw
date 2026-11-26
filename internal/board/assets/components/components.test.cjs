// VN-702 共享主题组件的静态契约测试。
//
// 验证目标（对应任务卡 Acceptance）：
//   1. token 成为唯一视觉源：契约颜色表 ↔ tokens.css 双向对齐，组件层零色值字面量。
//   2. 状态徽标覆盖 11 个固定状态键，且颜色职责与契约 §4 一致，颜色不单独承载信息。
//   3. 焦点、减弱动画、系统主题、抽屉尺寸与断点沿用旧约定。
//
// 运行：node --test internal/board/assets/components/components.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const DIR = __dirname;
const ROOT = path.resolve(DIR, "../../../..");
const CONTRACT = path.join(ROOT, "docs/contracts/vnext/ui/WORKBENCH_UI_CONTRACT.md");

const read = p => fs.readFileSync(p, "utf8");
// CSS 注释不参与契约判定：先剥离，再解析声明与 @import。
const stripCSS = css => css.replace(/\/\*[\s\S]*?\*\//g, "");
const readCSS = p => stripCSS(read(p));
const tokensCSS = readCSS(path.join(DIR, "../styles/tokens.css"));
const componentsCSS = readCSS(path.join(DIR, "components.css"));
const legacyStyle = read(path.join(DIR, "../legacy/style.css"));
const contractMD = read(CONTRACT);

// 契约 §2 颜色 token 表：| `--name` | `#dark` | `#light` | 职责 |
function parseColorTable(md) {
  const rows = [];
  for (const line of md.split("\n")) {
    const m = line.match(/^\|\s*`(--[a-z-]+)`\s*\|\s*`([^`]+)`\s*\|\s*`([^`]+)`\s*\|/);
    if (m) rows.push({ name: m[1], dark: m[2], light: m[3] });
  }
  return rows;
}

// 契约 §4 状态表：| `state` | 标签 | 泳道 | `--token` | 说明 |
function parseStateTable(md) {
  const rows = [];
  for (const line of md.split("\n")) {
    const m = line.match(/^\|\s*`([a-z_]+)`\s*\|\s*([^|]+?)\s*\|\s*([^|]+?)\s*\|\s*`(--[a-z-]+)`\s*\|/);
    if (m) rows.push({ state: m[1], label: m[2], lane: m[3], token: m[4] });
  }
  return rows;
}

// 从 CSS 中取某个选择器块内的声明文本
function block(css, selector) {
  const idx = css.indexOf(selector);
  if (idx < 0) return null;
  const open = css.indexOf("{", idx);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

function declarations(blockText) {
  const out = new Map();
  for (const part of (blockText || "").split(";")) {
    const i = part.indexOf(":");
    if (i < 0) continue;
    out.set(part.slice(0, i).trim(), part.slice(i + 1).trim());
  }
  return out;
}

const colorRows = parseColorTable(contractMD);
const stateRows = parseStateTable(contractMD);
const darkRoot = declarations(block(tokensCSS, ":root {"));
const lightRoot = declarations(block(tokensCSS, ':root[data-theme="light"]'));

test("契约颜色表可解析且数量与契约版本一致", () => {
  assert.equal(colorRows.length, 15, "契约 §2 应声明 15 个颜色 token");
  assert.equal(stateRows.length, 11, "契约 §4 应声明 11 个状态键");
});

test("tokens.css 深色模式逐条等于契约值", () => {
  for (const row of colorRows) {
    assert.equal(darkRoot.get(row.name), row.dark, `${row.name} 深色值必须与契约一致`);
  }
});

test("tokens.css 浅色模式逐条等于契约值", () => {
  for (const row of colorRows) {
    assert.equal(lightRoot.get(row.name), row.light, `${row.name} 浅色值必须与契约一致`);
  }
});

test("系统主题默认跟随，且可被 data-theme 覆盖", () => {
  assert.match(tokensCSS, /@media \(prefers-color-scheme: light\)/);
  assert.match(tokensCSS, /:root:not\(\[data-theme="dark"\]\)/);
  assert.match(tokensCSS, /:root\[data-theme="light"\]/);
});

test("tokens.css 的额外颜色只能是既有实现的派生色，不新增契约语义", () => {
  const known = new Set(colorRows.map(r => r.name));
  const derived = new Set([
    "--accent-ink", "--accent-soft", "--accent-hover",
    "--selection", "--brand-border", "--card-hover", "--card-hover-border",
  ]);
  for (const name of darkRoot.keys()) {
    if (!name.startsWith("--")) continue;
    if (known.has(name) || derived.has(name)) continue;
    const value = darkRoot.get(name);
    const isColor = /^#[0-9a-fA-F]{3,8}$/.test(value) || /^(rgba?|hsla?)\(/.test(value);
    assert.ok(!isColor, `${name} 是新增颜色 token，但不在契约 §2 或派生色白名单内：${value}`);
  }
});

test("组件层零色值字面量，颜色只能来自 token", () => {
  const files = fs.readdirSync(DIR).filter(f => f.endsWith(".css"));
  assert.ok(files.length >= 6, "组件层应包含 badge/table/form/empty/drawer/components");
  for (const file of files) {
    const css = readCSS(path.join(DIR, file));
    const hex = css.match(/#[0-9a-fA-F]{3,8}\b/g) || [];
    assert.deepEqual(hex, [], `${file} 不得硬编码色值：${hex.join(", ")}`);
    const fn = css.match(/\b(rgba?|hsla?)\(/g) || [];
    assert.deepEqual(fn, [], `${file} 不得硬编码色值函数：${fn.join(", ")}`);
  }
});

test("状态徽标覆盖 11 个固定状态键", () => {
  const badge = readCSS(path.join(DIR, "badge.css"));
  for (const row of stateRows) {
    assert.match(
      badge,
      new RegExp(`\\.badge--${row.state}\\s*\\{`),
      `缺少状态徽标 .badge--${row.state}`
    );
  }
  // 不得出现契约外的状态修饰类，避免状态词汇漂移
  const declared = [...badge.matchAll(/\.badge--([a-z_]+)/g)].map(m => m[1]);
  const allowed = new Set(stateRows.map(r => r.state));
  for (const name of new Set(declared)) {
    assert.ok(allowed.has(name), `徽标类 .badge--${name} 不在契约状态表内`);
  }
});

test("状态徽标颜色职责与契约 §4 一致", () => {
  const badge = readCSS(path.join(DIR, "badge.css"));
  for (const row of stateRows) {
    const decl = declarations(block(badge, `.badge--${row.state} {`));
    assert.equal(
      decl.get("color"),
      `var(${row.token})`,
      `.badge--${row.state} 应使用 ${row.token}`
    );
  }
});

test("徽标必须与文本标签同现，不以颜色单独承载信息", () => {
  const badge = readCSS(path.join(DIR, "badge.css"));
  // 圆点只是辅助，文本由调用方提供；组件不得把文本隐藏
  assert.doesNotMatch(badge, /\.badge\s*\{[^}]*font-size:\s*0/, "徽标不得把文字压成 0");
  assert.doesNotMatch(badge, /--text-hide|text-indent:\s*-999/, "徽标不得用视差隐藏文本");
});

test("焦点环沿用 2px accent / 偏移 3px，且不允许无替代的 outline:none", () => {
  const all = componentsCSS + fs.readdirSync(DIR)
    .filter(f => f.endsWith(".css"))
    .map(f => readCSS(path.join(DIR, f)))
    .join("\n");
  const focus = declarations(block(all, ":focus-visible {"));
  assert.equal(focus.get("outline"), "2px solid var(--accent)");
  assert.equal(focus.get("outline-offset"), "3px");
  assert.doesNotMatch(all, /outline:\s*none(?![\s\S]{0,120}:focus-visible)/);
});

test("prefers-reduced-motion 下关闭全部动画与过渡", () => {
  const m = componentsCSS.match(/@media \(prefers-reduced-motion: reduce\) \{([\s\S]*?)\n\}/);
  assert.ok(m, "缺少 prefers-reduced-motion 段落");
  assert.match(m[1], /animation:\s*none\s*!important/);
  assert.match(m[1], /transition:\s*none\s*!important/);
});

test("断点固定为 1500 / 1100 / 700", () => {
  const breakpoints = new Set(
    [...componentsCSS.matchAll(/@media \(max-width:\s*(\d+)px\)/g)].map(m => `${m[1]}px`)
  );
  const allowed = new Set(["1500px", "1100px", "700px"]);
  for (const bp of breakpoints) {
    assert.ok(allowed.has(bp), `新增断点 ${bp} 不在契约 §13 范围内`);
  }
  for (const bp of allowed) {
    assert.ok(breakpoints.has(bp), `缺少契约断点 ${bp}`);
  }
});

test("形状与密度 token 等于契约 §3", () => {
  assert.equal(darkRoot.get("--radius"), "6px");
  assert.equal(darkRoot.get("--control-height"), "32px");
  assert.equal(darkRoot.get("--control-height-compact"), "28px");
  assert.equal(darkRoot.get("--row-height"), "36px");
  assert.equal(darkRoot.get("--font-body"), "13px");
  assert.equal(darkRoot.get("--font-meta"), "12px");
  assert.equal(darkRoot.get("--font-panel"), "18px");
  assert.equal(darkRoot.get("--font-title"), "22px");
});

test("抽屉宽度、边界与遮罩沿用契约 §9", () => {
  const drawer = readCSS(path.join(DIR, "drawer.css"));
  const panel = declarations(block(drawer, ".drawer {"));
  assert.equal(panel.get("width"), "min(500px, 94vw)");
  assert.equal(panel.get("border-left"), "1px solid var(--line-strong)");
  assert.equal(panel.get("background"), "var(--panel)");
  const mask = declarations(block(drawer, ".drawer-mask {"));
  assert.equal(mask.get("background"), "var(--scrim)");
});

test("空态用虚线 line 边与 24px 16px 内边距，不用插画与渐变", () => {
  const empty = readCSS(path.join(DIR, "empty.css"));
  const decl = declarations(block(empty, ".empty-state {"));
  assert.equal(decl.get("border"), "1px dashed var(--line)");
  assert.equal(decl.get("padding"), "24px 16px");
  assert.doesNotMatch(empty, /gradient\(/);
});

test("components.css 的 @import 与实际组件文件一一对应", () => {
  const imported = [...componentsCSS.matchAll(/@import\s+"\.\/([^"]+)"/g)].map(m => m[1]);
  const present = fs.readdirSync(DIR).filter(f => f.endsWith(".css") && f !== "components.css");
  assert.deepEqual([...imported].sort(), [...present].sort(), "组件入口与实际文件不一致");
});

test("legacy/style.css 只做装配，不写第二套规则", () => {
  const body = legacyStyle.replace(/\/\*[\s\S]*?\*\//g, "").trim();
  const lines = body.split("\n").map(l => l.trim()).filter(Boolean);
  for (const line of lines) {
    assert.match(line, /^@import\s+"\.\.\/(styles\/tokens|components\/components)\.css";$/,
      `legacy/style.css 只允许装配共享包：${line}`);
  }
  assert.equal(lines.length, 2, "legacy/style.css 应只装配 tokens 与 components");
});
