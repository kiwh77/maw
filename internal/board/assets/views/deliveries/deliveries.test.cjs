// VN-724 交付与验收详情页的验证。
//
// 覆盖 Acceptance：
//   · 展示产物／提交、验证对象和结论；运行退出成功不会自动变成验收通过；
//   · 旧版本结论不可用于新交付，人工结论与合入操作状态可追溯；
//   · 复用共享主题／布局组件（不另造公共层），中英双语，加载／空／错误／过期／unknown 可核对。
//
// 契约依据：delivery-manifest-contract-v1.md §1–§6（三条硬规则、产物核实、证据等级、
// 代码与非代码两套完成条件、重复交付）；ui-data 契约 §3（局部字典命名空间化）。
//
// 运行：node --test internal/board/assets/views/deliveries/deliveries.test.cjs

const test = require("node:test");
const assert = require("node:assert/strict");
const path = require("node:path");
const { pathToFileURL } = require("node:url");

const MODULE = pathToFileURL(path.join(__dirname, "deliveries-view.mjs")).href;
const load = () => import(MODULE);

const SHA = "a".repeat(64);

/** 一份「什么都对」的代码交付，测试按需覆盖单项。 */
const delivery = (overrides = {}) => ({
  delivery_id: "dlv_001",
  task_instance: "ti_vn003c",
  task_version: 1,
  run_id: "run_a001",
  mode: "code",
  baseline_commit: "dedc919",
  candidate_commit: "abc1234",
  delivered_at: "2026-10-23T07:09:00Z",
  artifacts: [
    { artifact_id: "art_001", kind: "commit", ref: "abc1234", declared_by: "manual", exists: true, in_write_scope: true },
    {
      artifact_id: "art_002", kind: "file", ref: "docs/x.md", declared_by: "cli",
      exists: true, in_write_scope: true, sha256: SHA,
    },
  ],
  verification: [
    {
      verification_id: "vfy_001", command: "go test ./...", target_task_version: 1,
      target_commit: "abc1234", target_artifact_id: null, target_artifact_sha256: null,
      result: "pass", evidence: ["exit_code:0", "go test: 12 packages ok"],
      ran_at: "2026-10-23T07:10:00Z",
    },
  ],
  review: { required: true, verdict: "approved", reviewer: "r1", reviewed_at: "2026-10-23T07:11:00Z" },
  merge: { required: true, merged: true, merged_commit: "def5678", merged_at: "2026-10-23T07:12:00Z" },
  completion: { decision: "done", decided_at: "2026-10-23T07:13:00Z", reason: "" },
  ...overrides,
});

const nonCode = (overrides = {}) => delivery({
  mode: "non_code",
  candidate_commit: null,
  artifacts: [
    {
      artifact_id: "art_010", kind: "file", ref: "docs/report.md", declared_by: "manual",
      exists: true, in_write_scope: true, sha256: SHA,
    },
  ],
  verification: [
    {
      verification_id: "vfy_010", command: "manual review", target_task_version: 1,
      target_commit: null, result: "manual", evidence: ["人工验收：章节齐全"],
      ran_at: "2026-10-23T07:10:00Z",
    },
  ],
  merge: { required: false, merged: false },
  ...overrides,
});

const codes = result => result.blockers.map(item => item.code);

// ---------- 规则一：运行退出成功不会自动变成验收通过 ----------

test("只有 exit_code:0 的验证只能得到待验收，且说明为什么不算通过", async () => {
  const mod = await load();
  const onlyExit = delivery({
    verification: [{
      verification_id: "vfy_002", command: "go test ./...", target_task_version: 1,
      target_commit: "abc1234", result: "pass", evidence: ["exit_code:0"],
      ran_at: "2026-10-23T07:10:00Z",
    }],
  });

  const result = mod.evaluateCompletion(onlyExit);
  assert.equal(result.decision, "pending");
  assert.equal(result.canBeDone, false);
  assert.equal(result.shortfall, true, "声明 done 但证据不足要单独标出来");
  assert.ok(codes(result).includes("E340"), "缺少有效通过");
  assert.ok(codes(result).includes("E333"), "只有退出码不算通过");

  const html = mod.renderDeliveryDetail(onlyExit, { lang: "zh" });
  assert.ok(html.includes('<span class="badge badge--claimed">' + mod.t("view.deliveries.decision.pending", "zh") + "</span>"));
  assert.ok(!html.includes('<span class="badge badge--done">' + mod.t("view.deliveries.decision.done", "zh") + "</span>"),
    "不得把结论渲染成已完成");
  assert.ok(html.includes(mod.t("view.deliveries.code.E333", "zh")));
});

test("运行结果永远不是验收通过：退出码只作候选证据", async () => {
  const mod = await load();
  const ok = mod.describeRunOutcome({ phase: "completed", exit_code: 0 });
  assert.equal(ok.success, true);
  assert.equal(ok.supportsDone, false, "退出码不支撑完成判定");

  const bad = mod.describeRunOutcome({ phase: "failed", exit_code: 2 });
  assert.equal(bad.success, false);
  assert.equal(bad.supportsDone, false);

  const missing = mod.describeRunOutcome(null);
  assert.equal(missing.exitCode, null);
  assert.equal(missing.supportsDone, false);
});

test("有结果类证据、已合入、评审通过时才算完成", async () => {
  const mod = await load();
  const result = mod.evaluateCompletion(delivery());
  assert.equal(result.decision, "done");
  assert.equal(result.shortfall, false);
  assert.deepEqual(result.blockers, []);
});

test("pass 但没有任何证据时是 E332，不是「通过」", async () => {
  const mod = await load();
  const noEvidence = delivery({
    verification: [{
      verification_id: "vfy_003", command: "go test ./...", target_task_version: 1,
      target_commit: "abc1234", result: "pass", evidence: [],
    }],
  });
  const result = mod.evaluateCompletion(noEvidence);
  assert.equal(result.decision, "pending");
  assert.ok(codes(result).includes("E332"));
});

test("done 时仍存在失败验证则不得完成（E361）", async () => {
  const mod = await load();
  const withFail = delivery({
    verification: [
      ...delivery().verification,
      { verification_id: "vfy_004", command: "npm test", target_task_version: 1, target_commit: "abc1234", result: "fail", evidence: ["2 failing"] },
    ],
  });
  const result = mod.evaluateCompletion(withFail);
  assert.equal(result.decision, "pending");
  assert.ok(codes(result).includes("E361"));
});

// ---------- 规则二：产物查不到实体不算数 ----------

test("产物不存在或越界都不计入交付", async () => {
  const mod = await load();
  const bad = delivery({
    artifacts: [
      { artifact_id: "art_020", kind: "file", ref: "docs/x.md", declared_by: "cli", exists: false, in_write_scope: true },
      { artifact_id: "art_021", kind: "file", ref: "internal/y.go", declared_by: "cli", exists: true, in_write_scope: false },
    ],
  });
  const resolved = mod.resolveArtifacts(bad);
  assert.equal(resolved.accepted.length, 0);
  assert.deepEqual(resolved.rejected.map(item => item.code), ["E320", "E321"]);

  const result = mod.evaluateCompletion(bad);
  assert.equal(result.decision, "pending");
  assert.ok(codes(result).includes("E341"), "没有计入交付的产物");
  assert.ok(codes(result).includes("E320"));
  assert.ok(codes(result).includes("E321"));
});

test("没有产物时不能完成", async () => {
  const mod = await load();
  const result = mod.evaluateCompletion(delivery({ artifacts: [] }));
  assert.equal(result.decision, "pending");
  assert.ok(codes(result).includes("E341"));
});

// ---------- 规则三：旧证据不背书新交付 ----------

test("验证目标版本与交付版本不一致时失效（E330）", async () => {
  const mod = await load();
  const stale = delivery({
    verification: [{ ...delivery().verification[0], target_task_version: 2 }],
  });
  const result = mod.evaluateCompletion(stale);
  assert.equal(result.decision, "pending");
  assert.ok(codes(result).includes("E330"));

  const verdict = mod.verificationVerdict({ ...delivery().verification[0], target_task_version: 2 }, stale);
  assert.equal(verdict.supportsDone, false, "旧版本结论不可用于新交付");
  assert.equal(verdict.link, "stale");
});

test("针对旧提交的通过结论不能给新提交背书（E331）", async () => {
  const mod = await load();
  const oldCommit = delivery({
    verification: [{ ...delivery().verification[0], target_commit: "0000000" }],
  });
  const verdict = mod.verificationVerdict(oldCommit.verification[0], oldCommit);
  assert.equal(verdict.supportsDone, false);
  assert.equal(verdict.code, "E331");
  assert.ok(codes(mod.evaluateCompletion(oldCommit)).includes("E331"));
});

test("产物哈希变了，旧验证对当前产物失效（E334），重验后才成立", async () => {
  const mod = await load();
  const changed = delivery({
    verification: [{
      ...delivery().verification[0],
      target_artifact_id: "art_002",
      target_artifact_sha256: "b".repeat(64),
    }],
  });
  const stale = mod.verificationVerdict(changed.verification[0], changed);
  assert.equal(stale.supportsDone, false);
  assert.equal(stale.code, "E334");
  assert.equal(stale.link, "stale");

  const reverified = delivery({
    verification: [{
      ...delivery().verification[0],
      target_artifact_id: "art_002",
      target_artifact_sha256: SHA,
    }],
  });
  assert.equal(mod.evaluateCompletion(reverified).decision, "done", "针对新哈希重验后可以完成");
});

// ---------- 代码与非代码两套完成条件 ----------

test("代码交付未合入不得完成（E342）", async () => {
  const mod = await load();
  const result = mod.evaluateCompletion(delivery({ merge: { required: true, merged: false } }));
  assert.equal(result.decision, "pending");
  assert.ok(codes(result).includes("E342"));
});

test("需要评审但未通过时不得完成（E343）", async () => {
  const mod = await load();
  const result = mod.evaluateCompletion(delivery({
    review: { required: true, verdict: "rejected", reviewer: "r2", reviewed_at: "2026-10-23T07:11:00Z" },
    completion: { decision: "pending", decided_at: "2026-10-23T07:13:00Z" },
  }));
  assert.equal(result.decision, "pending");
  assert.ok(codes(result).includes("E343"));
});

test("非代码交付：人工验收 + 评审通过且不记合入才能完成", async () => {
  const mod = await load();
  assert.equal(mod.evaluateCompletion(nonCode()).decision, "done");

  const recordedMerge = mod.evaluateCompletion(nonCode({ merge: { required: false, merged: true } }));
  assert.equal(recordedMerge.decision, "pending");
  assert.ok(codes(recordedMerge).includes("E351"));

  const noManual = mod.evaluateCompletion(nonCode({
    verification: [{ ...nonCode().verification[0], result: "pass", evidence: ["exit_code:0", "lint ok"] }],
  }));
  assert.equal(noManual.decision, "pending");
  assert.ok(codes(noManual).includes("E350"));
});

test("人工验收结论在代码交付里不算通过（E350）", async () => {
  const mod = await load();
  const manualInCode = delivery({
    verification: [{ ...delivery().verification[0], result: "manual", evidence: ["看着没问题"] }],
  });
  const verdict = mod.verificationVerdict(manualInCode.verification[0], manualInCode);
  assert.equal(verdict.supportsDone, false);
  assert.equal(verdict.code, "E350");
});

// ---------- 拒绝与重复交付 ----------

test("拒绝必须写明原因（E360），写明后按已拒绝处理", async () => {
  const mod = await load();
  const noReason = mod.evaluateCompletion(delivery({
    completion: { decision: "rejected", decided_at: "2026-10-23T07:13:00Z", reason: "" },
  }));
  assert.ok(codes(noReason).includes("E360"));

  const withReason = mod.evaluateCompletion(delivery({
    merge: { required: true, merged: false, merged_at: null },
    verification: [{ ...delivery().verification[0], result: "fail", evidence: ["合入冲突"] }],
    completion: { decision: "rejected", decided_at: "2026-10-23T07:13:00Z", reason: "合入冲突，需返工" },
  }));
  assert.equal(withReason.decision, "rejected");
  assert.equal(withReason.state, "rejected");
  assert.ok(!codes(withReason).includes("E360"));
});

test("被取代的交付不得保留完成结论（E370）", async () => {
  const mod = await load();
  const old = delivery({ superseded_by: "dlv_002" });
  const result = mod.evaluateCompletion(old);
  assert.equal(result.decision, "pending");
  assert.ok(codes(result).includes("E370"));
});

test("supersedes 不得自指，也不得指向不存在的交付（E371）", async () => {
  const mod = await load();
  const self = delivery({ supersedes: "dlv_001" });
  assert.ok(codes(mod.evaluateCompletion(self)).includes("E371"));

  const missing = delivery({ supersedes: "dlv_404" });
  const siblings = [{ delivery_id: "dlv_002" }, { delivery_id: "dlv_003" }];
  assert.ok(codes(mod.evaluateCompletion(missing, { siblings })).includes("E371"));

  const ok = delivery({ supersedes: "dlv_002" });
  assert.deepEqual(codes(mod.evaluateCompletion(ok, { siblings })), []);
});

// ---------- unknown：不知道就说不知道 ----------

test("绑定缺失时不给结论，只列缺失字段", async () => {
  const mod = await load();
  const broken = delivery({ run_id: "", task_version: null });
  const result = mod.evaluateCompletion(broken);
  assert.equal(result.state, "unknown");
  assert.equal(result.decision, "unknown");
  assert.equal(result.canBeDone, false);
  assert.ok(codes(result).includes("E312"));
  assert.ok(codes(result).includes("E311"));

  const html = mod.renderDeliveryDetail(broken, { lang: "zh" });
  assert.ok(html.includes(mod.t("view.deliveries.unknown.title", "zh")));
  assert.ok(html.includes(mod.t("view.deliveries.code.E311", "zh")));
  assert.ok(!html.includes(mod.t("view.deliveries.decision.done", "zh")));
  assert.match(html, /data-action="recheck"/);
  assert.ok(!/data-action="(force|complete)/.test(html), "不提供强制完成入口");
});

test("提交哈希格式不对也是绑定缺口（E313）", async () => {
  const mod = await load();
  const alias = delivery({ candidate_commit: "HEAD" });
  assert.ok(codes(mod.evaluateCompletion(alias)).includes("E313"));
});

// ---------- 四阶段分开 ----------

test("运行结束、待验收、验收通过、任务完成互不推导", async () => {
  const mod = await load();
  const onlyExit = delivery({
    verification: [{ ...delivery().verification[0], evidence: ["exit_code:0"] }],
  });
  const stages = mod.describeStages(onlyExit, {
    run: { phase: "completed", exit_code: 0 },
    task_status: "in_progress",
  });
  const byId = Object.fromEntries(stages.map(stage => [stage.id, stage]));
  assert.equal(byId.run_finished.on, true, "运行确实结束了");
  assert.equal(byId.delivery_pending.on, true, "但交付仍待验收");
  assert.equal(byId.accepted.on, false);
  assert.equal(byId.task_done.on, false);

  const accepted = mod.describeStages(delivery(), {
    run: { phase: "completed", exit_code: 0 },
    task_status: "done",
  });
  const acceptedById = Object.fromEntries(accepted.map(stage => [stage.id, stage]));
  assert.equal(acceptedById.accepted.on, true);
  assert.equal(acceptedById.task_done.on, true);

  const noTaskState = mod.describeStages(delivery({}), { run: null });
  assert.equal(noTaskState[0].known, false, "没有运行结果时不猜运行结束");
  assert.equal(noTaskState[3].known, false, "没有任务状态时不推断任务完成");
});

// ---------- 追溯 ----------

test("追溯覆盖交付、验证、评审、合入与完成判定，并按时间排序", async () => {
  const mod = await load();
  const rows = mod.traceOf(delivery());
  const kinds = rows.map(row => row.kindKey);
  assert.ok(kinds.includes("view.deliveries.trace.delivery"));
  assert.ok(kinds.includes("view.deliveries.trace.verification"));
  assert.ok(kinds.includes("view.deliveries.trace.review"));
  assert.ok(kinds.includes("view.deliveries.trace.merge"));
  assert.ok(kinds.includes("view.deliveries.trace.completion"));

  const times = rows.map(row => row.at).filter(Boolean);
  assert.deepEqual(times, [...times].sort(), "有时间的记录必须按时间升序");

  const html = mod.renderDeliveryDetail(delivery(), { lang: "zh" });
  assert.ok(html.includes("def5678".slice(0, 7)), "合入提交可核对");
  assert.ok(html.includes("r1"), "评审人可核对");
});

// ---------- 文本安全与双语 ----------

test("产物引用、证据、评审人与提交字段一律转义", async () => {
  const mod = await load();
  const html = mod.renderDeliveryDetail(delivery({
    delivery_id: '<img src=x onerror="alert(1)">',
    artifacts: [{
      artifact_id: "art_x", kind: "file", ref: '<script>alert("x")</script>',
      declared_by: "cli", exists: true, in_write_scope: true, sha256: SHA,
    }],
    verification: [{
      ...delivery().verification[0],
      evidence: ['<b onmouseover="x()">ok</b>', "go test: 3 ok"],
    }],
    review: { required: true, verdict: "approved", reviewer: "<i>r1</i>", reviewed_at: "2026-10-23T07:11:00Z" },
  }), { lang: "zh" });

  assert.ok(!html.includes("<script>"));
  assert.ok(!html.includes("<img"));
  assert.ok(!html.includes("<b onmouseover"));
  assert.ok(!html.includes("<i>r1</i>"));
  assert.ok(html.includes("&lt;script&gt;"));
});

test("中英字典键集合一致，英文渲染不残留中文", async () => {
  const mod = await load();
  const zh = Object.keys(mod.dictionary("zh")).sort();
  const en = Object.keys(mod.dictionary("en")).sort();
  assert.deepEqual(en, zh, "同一视图的中英键必须成对");
  assert.ok(zh.every(key => key.startsWith("view.deliveries.")), "键必须命名空间化");

  const html = mod.renderDeliveryDetail(delivery(), { lang: "en" });
  assert.doesNotMatch(html, /[\u4e00-\u9fa5]/);
  assert.ok(html.includes("Done"));

  const list = mod.renderDeliveryList({ deliveries: [delivery()] }, { lang: "en" });
  assert.doesNotMatch(list, /[\u4e00-\u9fa5]/);

  assert.equal(mod.t("view.deliveries.no.such.key", "en"), "view.deliveries.no.such.key", "缺失键原样返回");
  assert.notEqual(mod.t("view.deliveries.title", "en"), "", "不得返回空字符串");
});

// ---------- 列表状态 ----------

test("加载／空／错误／过期／未认证状态各走独立文案", async () => {
  const mod = await load();
  const states = {
    loading: { status: "loading" },
    empty: { deliveries: [] },
    error: { status: "error" },
    expired: { status: "expired" },
    unauth: { status: "unauthenticated" },
  };
  const seen = new Set();
  for (const [name, state] of Object.entries(states)) {
    const html = mod.renderDeliveryList(state, { lang: "zh" });
    assert.ok(html.length > 0, name);
    assert.ok(!seen.has(html), name + " 与其他状态重复");
    seen.add(html);
    if (name === "error") assert.match(html, /empty-state--error/);
    if (!["loading", "empty"].includes(name)) assert.match(html, /empty-state/);
  }
  assert.match(mod.renderDeliveryList(states.empty, { lang: "zh" }), /没有可显示的交付记录|empty-state/);
});

test("列表只有原始行时不替用户下结论", async () => {
  const mod = await load();
  const row = {
    delivery_id: "dlv_001", task_instance: "ti_x", task_version: 1, run_id: "run_a",
    mode: "code", baseline_commit: "dedc919", candidate_commit: "abc1234",
    delivered_at: "2026-10-23T07:09:00Z", fingerprint: "fp_1",
  };
  const html = mod.renderDeliveryList({ deliveries: [row] }, { lang: "zh" });
  assert.ok(html.includes(mod.t("view.deliveries.list.detail", "zh")));
  assert.ok(!html.includes(mod.t("view.deliveries.decision.done", "zh")));
  assert.ok(!html.includes(mod.t("view.deliveries.decision.pending", "zh")));

  const withDetail = mod.renderDeliveryList({ deliveries: [delivery()] }, { lang: "zh" });
  assert.ok(withDetail.includes(mod.t("view.deliveries.decision.done", "zh")));
});

// ---------- 不另造公共层 ----------

test("渲染只使用共享组件类名，不自带样式", async () => {
  const mod = await load();
  const allowed = new Set([
    "table-wrap", "table", "badge", "empty-state", "empty-state__text", "empty-state--error",
    "field", "field-hint", "button", "id", "name", "path", "num", "time",
    // 交付详情元信息条：视图私有布局类，不进共享组件清单。
    "deliveries-meta",
  ]);
  const html = [
    mod.renderDeliveryDetail(delivery(), { lang: "zh", run: { phase: "completed", exit_code: 0 } }),
    mod.renderDeliveryDetail(delivery({ run_id: "" }), { lang: "zh" }),
    mod.renderDeliveryList({ deliveries: [delivery()] }, { lang: "zh" }),
    mod.renderDeliveryList({ status: "error" }, { lang: "zh" }),
  ].join("\n");

  assert.ok(!/<style/i.test(html), "不得内联样式");
  assert.ok(!/style="/i.test(html), "不得写行内样式");

  const tokens = new Set();
  for (const match of html.matchAll(/class="([^"]*)"/g)) {
    for (const token of match[1].split(/\s+/).filter(Boolean)) tokens.add(token);
  }
  for (const token of tokens) {
    if (token.startsWith("badge--")) continue;
    assert.ok(allowed.has(token), "未登记的类名：" + token);
  }
});
