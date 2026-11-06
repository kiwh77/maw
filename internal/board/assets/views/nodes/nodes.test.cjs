// VN-721 节点列表与能力详情的验证。
//
// 覆盖 Acceptance：
//   · 就绪四维、能力未知、版本失效、暂停接收分别展示，不把在线等同可执行；
//   · 表单与详情复用公共组件，深浅／双语／窄屏与未认证状态都有场景（浏览器用例负责视觉，本文件负责语义）。
//
// 四维定义与依据见 nodes-view.mjs 头部（架构 §140／§144／§486）。
// 运行：node --test internal/board/assets/views/nodes/nodes.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "nodes-view.mjs")).href;
const load = () => import(MODULE);

const NOW = Date.parse("2026-10-23T06:00:00Z");

function node(overrides = {}) {
  return {
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
    ...overrides,
  };
}

const ready = (n, extra = {}) => {
  const mod = extra.mod;
  return mod.evaluateReadiness(n, { now: NOW, ...extra });
};

// ---------- 四维判定 ----------

test("四维全部成立才判可接任务", async () => {
  const mod = await load();
  const result = ready(node(), { mod });
  assert.equal(result.verdict, "executable");
  assert.equal(result.executable, true);
  assert.deepEqual(result.reasons, []);
});

test("在线不等于可执行：认证未就绪即 blocked", async () => {
  const mod = await load();
  const result = ready(node({ auth: { state: "needs-login" } }), { mod });
  assert.equal(result.presence, "online", "在线这一维仍然成立");
  assert.equal(result.verdict, "blocked");
  assert.equal(result.executable, false);
  assert.ok(result.reasons.includes("reason.auth"));
});

test("需要选模型与需要登录分别可见，不合并成同一句", async () => {
  const mod = await load();
  const login = ready(node({ auth: { state: "needs-login" } }), { mod });
  const model = ready(node({ auth: { state: "needs-model" } }), { mod });
  assert.notEqual(
    mod.t("auth." + login.auth, "zh"),
    mod.t("auth." + model.auth, "zh")
  );
  assert.equal(mod.t("auth.needs-model", "zh"), "需要选模型");
});

test("能力声明了但没验证，不得当成具备能力", async () => {
  const mod = await load();
  const result = ready(node({
    capability: { declared: ["non_interactive"], verified: [], cli_version: "1.4.0" },
  }), { mod });
  assert.equal(result.capability, "unknown");
  assert.equal(result.executable, false);
  assert.ok(result.reasons.includes("reason.capability_unknown"));
});

test("CLI 版本变化使旧验证失效，需要重新确认", async () => {
  const mod = await load();
  const result = ready(node({
    capability: {
      declared: ["non_interactive"],
      verified: ["non_interactive"],
      cli_version: "1.5.0",
      verified_version: "1.4.0",
    },
  }), { mod });
  assert.equal(result.capability, "version_changed");
  assert.equal(result.verdict, "blocked");
  assert.ok(result.reasons.includes("reason.capability_version"));
});

test("部分能力已验证时判 partial，仍按未验证处理", async () => {
  const mod = await load();
  const result = ready(node({
    capability: {
      declared: ["non_interactive", "cancel"],
      verified: ["non_interactive"],
      cli_version: "1.4.0",
      verified_version: "1.4.0",
    },
  }), { mod });
  assert.equal(result.capability, "partial");
  assert.equal(result.executable, false);
});

test("暂停接收单独成维：节点可用但不接新任务", async () => {
  const mod = await load();
  const result = ready(node({ intake: "paused", paused_reason: "维护窗口" }), { mod });
  assert.equal(result.presence, "online");
  assert.equal(result.capacity, "available");
  assert.equal(result.intake, "paused");
  assert.equal(result.verdict, "blocked");
  assert.deepEqual(result.reasons, ["reason.intake_paused"]);
});

test("槽满与暂停接收是两件事，原因不混", async () => {
  const mod = await load();
  const full = ready(node({ slots: { total: 2, busy: 2 } }), { mod });
  assert.equal(full.capacity, "full");
  assert.deepEqual(full.reasons, ["reason.capacity"]);

  const paused = ready(node({ intake: "paused" }), { mod });
  assert.deepEqual(paused.reasons, ["reason.intake_paused"]);
});

test("心跳过期降级为 stale，不直接判离线", async () => {
  const mod = await load();
  const result = ready(node({
    presence: { online: true, last_seen: "2026-10-23T05:50:00Z" },
  }), { mod });
  assert.equal(result.presence, "stale");
  assert.equal(result.executable, false);
  assert.ok(result.reasons.includes("reason.not_online"));
});

test("明确离线与心跳过期区分开", async () => {
  const mod = await load();
  const offline = ready(node({ presence: { online: false, last_seen: "2026-10-23T05:59:30Z" } }), { mod });
  assert.equal(offline.presence, "offline");
});

test("缺证据判 unknown，且绝不渲染成可执行", async () => {
  const mod = await load();
  const noPresence = ready(node({ presence: undefined }), { mod });
  assert.equal(noPresence.verdict, "unknown");
  assert.equal(noPresence.executable, false);

  const noAuth = ready(node({ auth: {} }), { mod });
  assert.equal(noAuth.verdict, "unknown");
  assert.equal(noAuth.executable, false);
});

// ---------- 列表渲染 ----------

test("加载／空／错误／过期／未认证各走一种状态，不互相冒充", async () => {
  const mod = await load();
  const outputs = {
    loading: mod.renderNodeList({ status: "loading" }, { lang: "zh" }),
    empty: mod.renderNodeList({ nodes: [] }, { lang: "zh" }),
    error: mod.renderNodeList({ status: "error" }, { lang: "zh" }),
    expired: mod.renderNodeList({ status: "expired" }, { lang: "zh" }),
    unauth: mod.renderNodeList({ status: "unauthenticated" }, { lang: "zh" }),
  };
  const texts = Object.entries(outputs).map(([k, v]) => [k, v.replace(/<[^>]+>/g, "")]);
  const seen = new Set();
  for (const [key, text] of texts) {
    assert.ok(text.length > 0, key + " 必须有文案");
    assert.ok(!seen.has(text), key + " 与其他状态文案重复，等于互相冒充");
    seen.add(text);
  }
  assert.match(outputs.unauth, /empty-state/);
  assert.match(outputs.error, /empty-state/);
});

test("列表卡片同时给出四维与结论，且复用共享徽标组件", async () => {
  const mod = await load();
  const html = mod.renderNodeList({ nodes: [node()] }, { lang: "zh", now: NOW });
  // VN-721 起列表由表格改为卡片网格：维度不再是表头，而是每卡四枚徽标。
  assert.match(html, /class="nodes-grid"/, "节点列表用卡片网格");
  assert.match(html, /class="nodes-card"/, "每个节点一张卡片");
  assert.match(html, /class="badge badge--done"/, "复用共享徽标组件");
  const dims = html.match(/<div class="nodes-card__dims">([\s\S]*?)<\/div>/);
  assert.ok(dims, "四维徽标容器应在");
  assert.equal((dims[1].match(/class="badge/g) || []).length, 4, "在线／认证／接收／容量四维各一枚徽标");
  for (const key of ["presence.online", "auth.ready", "intake.open"]) {
    assert.ok(html.includes(mod.t(key, "zh")), "缺少维度徽标：" + key);
  }
  assert.ok(html.includes(mod.t("verdict.executable", "zh")));
});

test("不可执行的行必须写明卡在哪一维", async () => {
  const mod = await load();
  const html = mod.renderNodeList(
    { nodes: [node({ auth: { state: "needs-login" } })] },
    { lang: "zh", now: NOW }
  );
  assert.ok(html.includes(mod.t("verdict.blocked", "zh")));
  assert.ok(html.includes(mod.t("reason.auth", "zh", { state: mod.t("auth.needs-login", "zh") })));
  // 用徽标类名判定，避免与统计口径里的「个可接任务」子串混淆。
  assert.ok(!html.includes('badge badge--done">' + mod.t("verdict.executable", "zh")),
    "不得同时出现可执行结论徽标");
});

test("统计口径只数真正可执行的节点", async () => {
  const mod = await load();
  const html = mod.renderNodeList(
    {
      nodes: [
        node({ id: "n1" }),
        node({ id: "n2", intake: "paused" }),
        node({ id: "n3", presence: undefined }),
      ],
    },
    { lang: "zh", now: NOW }
  );
  assert.ok(html.includes("3 " + mod.t("nodes.count", "zh")));
  assert.ok(html.includes("1 " + mod.t("nodes.executable", "zh")));
});

test("双语各自完整：英文界面不残留中文标签", async () => {
  const mod = await load();
  const zh = mod.renderNodeList({ nodes: [node()] }, { lang: "zh", now: NOW });
  const en = mod.renderNodeList({ nodes: [node()] }, { lang: "en", now: NOW });
  assert.ok(zh.includes("个节点"));
  assert.ok(en.includes("nodes"));
  assert.doesNotMatch(en.replace(/node_a|codebuddy|本机 CodeBuddy/g, ""), /[\u4e00-\u9fa5]/,
    "英文界面不得混入中文");
});

test("节点名里的标记被转义，不当作 HTML 执行", async () => {
  const mod = await load();
  const html = mod.renderNodeList(
    { nodes: [node({ name: '<script>alert("x")</script>' })] },
    { lang: "zh", now: NOW }
  );
  assert.ok(!html.includes("<script>"));
  assert.ok(html.includes("&lt;script&gt;"));
});

// ---------- 详情渲染 ----------

test("详情按声明／已验证两列展示能力，未验证项单独标出", async () => {
  const mod = await load();
  const html = mod.renderNodeDetail(node({
    capability: {
      declared: ["non_interactive", "cancel"],
      verified: ["non_interactive"],
      cli_version: "1.4.0",
      verified_version: "1.4.0",
    },
  }), { lang: "zh", now: NOW });
  assert.ok(html.includes(mod.t("cap.declared", "zh")));
  assert.ok(html.includes(mod.t("cap.verified_list", "zh")));
  assert.ok(html.includes(mod.t("cap.unverified", "zh", { n: 1 })));
});

test("版本失效在详情里给出明确提示，不只体现在列表", async () => {
  const mod = await load();
  const html = mod.renderNodeDetail(node({
    capability: {
      declared: ["non_interactive"],
      verified: ["non_interactive"],
      cli_version: "2.0.0",
      verified_version: "1.4.0",
    },
  }), { lang: "zh", now: NOW });
  assert.ok(html.includes(mod.t("cap.version_changed", "zh")));
  assert.ok(html.includes(mod.t("reason.capability_version", "zh")));
});

test("未认证只显示登录提示，不泄漏节点能力", async () => {
  const mod = await load();
  const html = mod.renderNodeDetail(node(), { lang: "zh", now: NOW, unauthenticated: true });
  assert.ok(html.includes(mod.t("detail.unauth_title", "zh")));
  assert.ok(!html.includes("non_interactive"), "未认证不得输出能力清单");
});

test("暂停原因只在详情出现，且与槽位信息分开", async () => {
  const mod = await load();
  const html = mod.renderNodeDetail(node({ intake: "paused", paused_reason: "维护窗口" }), {
    lang: "zh", now: NOW,
  });
  assert.ok(html.includes(mod.t("detail.paused_reason", "zh")));
  assert.ok(html.includes("维护窗口"));
  assert.ok(html.includes(mod.t("detail.slots", "zh")));
});
