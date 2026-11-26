// VN-723 运行详情与待核实面板的验证。
//
// 覆盖 Acceptance：
//   · 运行／交付／任务状态分开，取消受理不立即显示已停止；
//   · unknown 面板给出最后事实、未知点、占用资源与处理入口，文本安全渲染；
//   · 加载／空／错误／过期／unknown 状态可核对，未另造公共层。
//
// 契约依据：runtime-state-contract §1–§5（三套语义、十阶段、unknown、释放前置、取消竞争）。
// 运行：node --test internal/board/assets/views/runs/runs.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "runs-view.mjs")).href;
const load = () => import(MODULE);

const run = (overrides = {}) => ({
  run_id: "run_1",
  phase: "running",
  reconciliation_state: "confirmed",
  desired_action: "none",
  process_stop_confirmed: false,
  delivery_frozen: false,
  generation: 1,
  task_status: "in_progress",
  last_heartbeat_at: "2026-10-23T07:00:00Z",
  events: [{ type: "run.phase", message: "started", received_at: "2026-10-23T07:00:00Z", quality: "observed" }],
  ...overrides,
});

// ---------- 三层状态不互相推导 ----------

test("运行完成不等于任务完成，也不等于交付通过", async () => {
  const mod = await load();
  const layers = mod.describeLayers(run({ phase: "completed", task_status: "in_progress" }));
  assert.equal(layers.run.phase, "completed");
  assert.equal(layers.run.terminal, true);
  assert.equal(layers.task.status, "in_progress", "任务状态不得被运行阶段带跑");
  assert.equal(layers.task.completedImpliesDone, false);
  assert.equal(layers.delivery.state, "not_frozen");
});

test("十阶段封闭，未知阶段不猜成执行中", async () => {
  const mod = await load();
  assert.equal(mod.RUN_PHASES.length, 10);
  const layers = mod.describeLayers(run({ phase: "bogus" }));
  assert.equal(layers.run.phase, "unknown");
  assert.equal(layers.run.terminal, false);
});

test("终态集合与契约一致", async () => {
  const mod = await load();
  for (const phase of ["completed", "failed", "cancelled", "timed_out"]) {
    assert.equal(mod.isTerminal(phase), true, phase);
  }
  for (const phase of ["reserved", "preparing", "launching", "running", "waiting", "stopping"]) {
    assert.equal(mod.isTerminal(phase), false, phase);
  }
});

// ---------- 取消：意图 vs 事实 ----------

test("取消受理后仍是执行中，不显示已停止", async () => {
  const mod = await load();
  const state = mod.describeCancel(run({ desired_action: "stop" }));
  assert.equal(state.state, "accepted");
  assert.equal(state.stopConfirmed, false);
  const html = mod.renderRunDetail(run({ desired_action: "stop" }), { lang: "zh" });
  assert.ok(html.includes(mod.t("cancel.accepted", "zh")));
  assert.ok(!html.includes(mod.t("cancel.stopped", "zh")), "受理不等于停止");
});

test("阶段已 cancelled 但停止未确认时，仍不能说已停止", async () => {
  const mod = await load();
  const state = mod.describeCancel(run({ phase: "cancelled", process_stop_confirmed: false }));
  assert.equal(state.state, "accepted");
  assert.equal(state.stopConfirmed, false);
});

test("停止确认后才显示已停止", async () => {
  const mod = await load();
  const state = mod.describeCancel(run({ phase: "cancelled", process_stop_confirmed: true }));
  assert.equal(state.state, "stopped");
  const html = mod.renderRunDetail(run({ phase: "cancelled", process_stop_confirmed: true }), { lang: "zh" });
  assert.ok(html.includes(mod.t("cancel.stopped", "zh")));
});

test("结束事实先到时 already_finished，且不改写原阶段", async () => {
  const mod = await load();
  const finished = run({ phase: "completed", task_status: "done" });
  const state = mod.describeCancel(finished, {
    status: "failed",
    error: { code: "already_finished" },
    result: { code: "already_finished", result: { exit_code: 0 } },
  });
  assert.equal(state.state, "already_finished");
  assert.equal(state.phase, "completed", "不得把 completed 追改成 cancelled");
  assert.equal(state.mutatedPhase, false);
  assert.deepEqual(state.original, { exit_code: 0 });
});

test("运行已是 completed 时，即使没有操作也判 already_finished", async () => {
  const mod = await load();
  const state = mod.describeCancel(run({ phase: "completed", desired_action: "stop" }));
  assert.equal(state.state, "already_finished");
});

// ---------- 占用与释放 ----------

test("仍占用的三条判据各自成立", async () => {
  const mod = await load();
  assert.deepEqual(mod.stillOccupying(run()).reasons, ["occ.reason.non_terminal"]);
  assert.deepEqual(
    mod.stillOccupying(run({ phase: "completed", reconciliation_state: "unknown" })).reasons,
    ["occ.reason.unknown"]
  );
  assert.deepEqual(
    mod.stillOccupying(run({ phase: "failed", process_stop_confirmed: false })).reasons,
    ["occ.reason.not_confirmed"]
  );
});

test("三条判据都不成立才算释放", async () => {
  const mod = await load();
  const released = mod.stillOccupying(run({
    phase: "completed", reconciliation_state: "confirmed", process_stop_confirmed: true,
  }));
  assert.equal(released.occupying, false);
  assert.deepEqual(released.reasons, []);
});

test("槽与目录分别判断释放前置，不一起放", async () => {
  const mod = await load();
  const both = mod.releaseEligibility(run({ process_stop_confirmed: false, delivery_frozen: false }));
  assert.equal(both.node_slot.releasable, false);
  assert.equal(both.workspace.releasable, false);

  const slotOnly = mod.releaseEligibility(run({ process_stop_confirmed: true, delivery_frozen: false }));
  assert.equal(slotOnly.node_slot.releasable, true);
  assert.equal(slotOnly.workspace.releasable, false, "进程停了不代表产物能重分配");
  assert.ok(slotOnly.workspace.blockedBy);
});

// ---------- unknown 面板 ----------

test("unknown 面板四块齐全：最后事实／未知点／占用／处理入口", async () => {
  const mod = await load();
  const info = mod.resolveUnknown(run({ reconciliation_state: "unknown", last_heartbeat_at: null }));
  assert.ok(info.facts.length >= 1, "要有最后事实");
  assert.ok(info.points.includes("unknown.point.no_heartbeat"));
  assert.ok(info.points.includes("unknown.point.no_evidence"));
  assert.equal(info.occupancy.occupying, true, "unknown 期间占用保留");
  assert.deepEqual(info.actions.map(a => a.id), ["recheck", "report"]);
});

test("unknown 不提供强制释放入口", async () => {
  const mod = await load();
  const info = mod.resolveUnknown(run({ reconciliation_state: "unknown" }));
  assert.equal(info.forcedRelease, false);
  const html = mod.renderUnknownPanel(run({ reconciliation_state: "unknown" }), { lang: "zh" });
  // 只禁止「可点的强制释放」，说明里那句「不提供强制释放」是应该保留的。
  assert.ok(!/data-action="(force|release)/.test(html), "不得出现强制释放动作");
  assert.ok(!/<button[^>]*>\s*强制释放/.test(html));
  assert.ok(html.includes(mod.t("unknown.action.note", "zh")));
});

test("unknown 期间释放按钮保持阻塞态而非可点", async () => {
  const mod = await load();
  const html = mod.renderUnknownPanel(
    run({ reconciliation_state: "unknown", process_stop_confirmed: false, delivery_frozen: false }),
    { lang: "zh" }
  );
  assert.ok(html.includes(mod.t("release.node_slot.blocked", "zh")));
  assert.ok(html.includes(mod.t("release.workspace.blocked", "zh")));
  assert.ok(!html.includes(mod.t("release.ready", "zh")));
});

test("没有可信事实时明确说没有，不编造", async () => {
  const mod = await load();
  const info = mod.resolveUnknown({ run_id: "run_x", reconciliation_state: "unknown" });
  assert.equal(info.facts.length, 0);
  const html = mod.renderUnknownPanel({ run_id: "run_x", reconciliation_state: "unknown" }, { lang: "zh" });
  assert.ok(html.includes(mod.t("unknown.no_facts", "zh")));
});

// ---------- 文本安全 ----------

test("日志与事件文本一律转义，不解析 HTML", async () => {
  const mod = await load();
  const html = mod.renderRunDetail(run({
    events: [{ type: "run.log", message: '<script>alert("x")</script>', received_at: "2026-10-23T07:00:00Z" }],
  }), { lang: "zh" });
  assert.ok(!html.includes("<script>"));
  assert.ok(html.includes("&lt;script&gt;"));
  assert.match(html, /<pre class="run-log">/);
});

test("运行 ID 与任务状态也走转义", async () => {
  const mod = await load();
  const html = mod.renderRunDetail(run({
    run_id: '<img src=x onerror="alert(1)">',
    task_status: "<b>x</b>",
  }), { lang: "zh" });
  assert.ok(!html.includes("<img"));
  assert.ok(!html.includes("<b>x</b>"));
});

// ---------- 列表状态与双语 ----------

test("加载／空／错误／过期／未认证各走独立文案", async () => {
  const mod = await load();
  const states = {
    loading: { status: "loading" },
    empty: { runs: [] },
    error: { status: "error" },
    expired: { status: "expired" },
    unauth: { status: "unauthenticated" },
  };
  const seen = new Set();
  for (const [key, state] of Object.entries(states)) {
    const html = mod.renderRunList(state, { lang: "zh" });
    assert.ok(html.length > 0, key);
    assert.ok(!seen.has(html), key + " 与其他状态重复");
    seen.add(html);
  }
});

test("列表把三层状态并列显示，任一列都不替其他列下结论", async () => {
  const mod = await load();
  const html = mod.renderRunList(
    { runs: [run({ phase: "completed", task_status: "in_progress", delivery_frozen: false })] },
    { lang: "zh" }
  );
  assert.ok(html.includes(mod.t("layer.run", "zh")));
  assert.ok(html.includes(mod.t("layer.delivery", "zh")));
  assert.ok(html.includes(mod.t("layer.task", "zh")));
  assert.ok(html.includes(mod.t("phase.completed", "zh")));
  assert.ok(html.includes("in_progress"), "任务状态列保留自身取值");
  assert.ok(html.includes(mod.t("delivery.pending", "zh")) === false);
  assert.ok(html.includes(mod.t("delivery.not_frozen", "zh")));
});

test("英文界面不残留中文", async () => {
  const mod = await load();
  const en = mod.renderRunList({ runs: [run()] }, { lang: "en" });
  assert.ok(en.includes("Runs"));
  assert.doesNotMatch(en, /[\u4e00-\u9fa5]/);
});

test("重试核实与上报人工处置是两个入口，不是一个万能按钮", async () => {
  const mod = await load();
  const html = mod.renderUnknownPanel(run({ reconciliation_state: "unknown" }), { lang: "en" });
  assert.match(html, /data-action="recheck"/);
  assert.match(html, /data-action="report"/);
});
