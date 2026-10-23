const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

// Exercise the shipped application resources, with only startup and DOM edges stubbed.
function app() {
  const source = fs.readFileSync(__dirname + '/assets/legacy/board-app.js', 'utf8');
  const theme = fs.readFileSync(__dirname + '/assets/bootstrap/theme.js', 'utf8');
  const elements = new Map();
  const document = {
    documentElement: { lang: 'en' },
    title: '',
    getElementById(id) {
      if (!elements.has(id)) elements.set(id, { innerHTML: '', value: '', textContent: '', setAttribute() {}, addEventListener() {}, querySelectorAll() { return []; }, title: '' });
      return elements.get(id);
    },
    querySelector() { return { textContent: '' }; },
    querySelectorAll() { return []; },
    addEventListener() {},
  };
  const context = vm.createContext({
    document,
    location: { search: '' },
    navigator: { language: 'en-US' },
    localStorage: { getItem() { return null; }, setItem() {} },
    URLSearchParams,
    setTimeout,
    clearTimeout,
  });
  vm.runInContext(theme.split('// Theme bootstrap')[0], context);
  vm.runInContext(source.split('// Bootstrap stays separate')[0], context);
  return code => vm.runInContext(code, context);
}

test('every persisted status belongs to exactly one workflow stage, including cancelled', () => {
  const run = app();
  assert.equal(run('COLUMNS.every(([status]) => LANES.filter(lane => lane.statuses.includes(status)).length === 1)'), true);
  assert.equal(run('LANES.find(lane => lane.statuses.includes("cancelled")).id'), 'finished');
});

test('task identity is scoped by project even when task IDs collide', () => {
  const run = app();
  run('lastSnap = { tasks: [{id:"T-1", project_id:"one"}, {id:"T-1", project_id:"two"}] }');
  assert.equal(run('findTask("T-1", "two").project_id'), 'two');
  assert.equal(run('findTask("T-1", "missing")'), null);
});

test('search combines with exact filters and does not mutate the snapshot', () => {
  const run = app();
  run(`lastSnap = { tasks: [
    {id:"T-1", project_id:"one", title:"Implement API", status:"ready", owner:"Alice"},
    {id:"T-2", project_id:"one", title:"API docs", status:"done", owner:"Bob"},
    {id:"T-3", project_id:"two", title:"API tests", status:"ready", owner:"Alice"}
  ] }; filters.project="one"; filters.status="ready"; searchTerm="  API  ";`);
  assert.equal(run('visibleTasks(lastSnap).map(t => t.id).join()'), 'T-1');
  run('searchTerm="alice"; filters.agent="Bob"');
  assert.equal(run('visibleTasks(lastSnap).length'), 0);
  assert.equal(run('lastSnap.tasks.length'), 3);
});

test('later filter response wins when requests resolve out of order', async () => {
  const run = app();
  run('var pending=[]; var rendered=[]; loadSnapshot=()=>new Promise(resolve=>pending.push(resolve)); render=s=>rendered.push(s.total);');
  const old = run('tick()');
  const latest = run('tick()');
  run('pending[1]({total:2})');
  await latest;
  run('pending[0]({total:1})');
  await old;
  assert.equal(run('rendered.join()'), '2');
});

test('task and project content is escaped before entering HTML', () => {
  const run = app();
  assert.equal(run('esc(\'<img src=x onerror="alert(1)">\')'), '&lt;img src=x onerror=&quot;alert(1)&quot;&gt;');
});

test('fmtTime normalizes ISO timestamps to YYYY-MM-DD HH:mm:ss local', () => {
  const run = app();
  assert.equal(run('fmtTime("2026-10-21 11:43:17")'), '2026-10-21 11:43:17');
  assert.equal(run('fmtTime("")'), '');
  assert.equal(run('fmtTime("not-a-date")'), 'not-a-date');
  const out = run('fmtTime("2026-10-21T03:43:10Z")');
  assert.match(out, /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/);
  assert.equal(run('fmtTime("2026-10-21T03:43:10Z") === fmtTime("2026-10-21T03:43:10Z")'), true);
});

test('switching projects while a detail response is pending cannot overwrite the new task', async () => {
  const run = app();
  run(`var responses=[]; var detailRenders=0;
    fetch=()=>new Promise(resolve=>responses.push(resolve));
    renderDrawerBody=()=>detailRenders++;
    lastSnap={tasks:[{id:"T-1",project_id:"one"},{id:"T-1",project_id:"two"}]};
    selectedTid="T-1"; selectedPid="one";`);
  const first = run('refreshTaskDetail("T-1", "one")');
  run('selectedPid="two"');
  const second = run('refreshTaskDetail("T-1", "two")');
  run('responses[1]({ok:true,json:async()=>({task:{id:"T-1",project_id:"two",title:"latest"},events:[]})})');
  await second;
  run('responses[0]({ok:true,json:async()=>({task:{id:"T-1",project_id:"one",title:"stale"},events:[]})})');
  await first;
  assert.equal(run('detailRenders'), 1);
  assert.equal(run('lastSnap.tasks[1].title'), 'latest');
  assert.equal(run('lastSnap.tasks[0].title'), undefined);
});

test('theme defaults to system, validates saved choices and persists explicit choices', () => {
  const run = app();
  run(`document.documentElement={dataset:{}}; var saved={};
    localStorage={getItem:key=>saved[key]||null,setItem:(key,value)=>saved[key]=value};`);
  assert.equal(run('readTheme()'), 'system');
  run('saved["maw-theme"]="invalid"');
  assert.equal(run('readTheme()'), 'system');
  run('setTheme("light")');
  assert.equal(run('document.documentElement.dataset.theme'), 'light');
  assert.equal(run('saved["maw-theme"]'), 'light');
  run('setTheme("system")');
  assert.equal(run('document.documentElement.dataset.theme'), 'system');
});

test('theme remains usable when browser storage is unavailable', () => {
  const run = app();
  run(`document.documentElement={dataset:{}};
    localStorage={getItem:()=>{throw Error("disabled")},setItem:()=>{throw Error("disabled")}};`);
  assert.equal(run('readTheme()'), 'system');
  run('setTheme("dark")');
  assert.equal(run('document.documentElement.dataset.theme'), 'dark');
});

test('SSE notifications refresh current data and connection errors keep polling available', () => {
  const run = app();
  run(`var handlers={}; var refreshes=0; var streamClosed=false;
    EventSource=class {constructor(url){this.url=url;this.readyState=0;}
      addEventListener(name,fn){handlers[name]=fn} close(){streamClosed=true}};
    tick=()=>refreshes++;`);
  run('connectEvents()');
  assert.equal(run('eventStream.url'), '/api/events');
  run('eventStream.onopen()');
  assert.equal(run('sseConnected'), true);
  run('handlers["board-changed"]()');
  assert.equal(run('refreshes'), 1);
  run('eventStream.onerror()');
  assert.equal(run('sseConnected'), false);
  assert.equal(run('streamClosed'), false);
});

test('unsupported SSE browsers continue in polling mode', () => {
  const run = app();
  run('connectEvents()');
  assert.equal(run('sseConnected'), false);
  assert.equal(run('eventStream'), null);
});

test('a burst of realtime notifications coalesces while a fetch is in flight', async () => {
  const run = app();
  run('var syncs=[]; tick=()=>new Promise(resolve=>syncs.push(resolve));');
  const first=run('syncBoard()');
  await run('syncBoard()');
  await run('syncBoard()');
  assert.equal(run('syncs.length'),1);
  run('syncs[0]()');
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(run('syncs.length'),2);
  run('syncs[1]()');
  await first;
  assert.equal(run('syncs.length'),2);
});

test('topology layout places dependencies left of dependents and keeps projects separate', () => {
  const run = app();
  const bands = run(`layoutTopoBands([
    {id:"T-1", project_id:"p1", project_name:"Alpha", title:"root", status:"done", priority:10, deps:[]},
    {id:"T-2", project_id:"p1", project_name:"Alpha", title:"mid", status:"ready", priority:20, deps:["T-1"]},
    {id:"T-3", project_id:"p1", project_name:"Alpha", title:"leaf", status:"backlog", priority:30, deps:["T-2","T-MISSING"]},
    {id:"T-9", project_id:"p2", project_name:"Beta", title:"other", status:"ready", priority:10, deps:[]}
  ])`);
  assert.equal(bands.length, 2);
  assert.equal(bands[0].name, 'Alpha');
  assert.equal(bands[0].edgeCount, 3);
  assert.equal(bands[0].positions.get('T-1').x < bands[0].positions.get('T-2').x, true);
  assert.equal(bands[0].positions.get('T-2').x < bands[0].positions.get('T-3').x, true);
  assert.equal(bands[0].positions.get('T-MISSING').stub, true);
  assert.equal(bands[1].name, 'Beta');
  assert.equal(bands[1].edgeCount, 0);
});

test('project selection syncs URL hash and hash restores filter on load', () => {
  const run = app();
  run(`var loc={pathname:"/",search:"",hash:""};
    location=loc;
    history={replaceState:(a,b,url)=>{
      const i=String(url).indexOf("#");
      loc.hash=i>=0?String(url).slice(i):"";
      const q=String(url).indexOf("?");
      loc.pathname=String(url).split("#")[0].split("?")[0]||"/";
      loc.search=q>=0?String(url).slice(q).split("#")[0]:"";
    }};`);
  assert.equal(run('readProjectHash()'), '');
  run('setProjectFilter("931269ebc68abb88")');
  assert.equal(run('filters.project'), '931269ebc68abb88');
  assert.equal(run('location.hash'), '#931269ebc68abb88');
  run('setProjectFilter("")');
  assert.equal(run('filters.project'), '');
  assert.equal(run('location.hash'), '');
  run('location.hash="#7b24d390e1ee056d"; filters.project=""; filters._inited=false');
  assert.equal(run('readProjectHash()'), '7b24d390e1ee056d');
  run('setProjectFilter(readProjectHash(), {syncHash:false})');
  assert.equal(run('filters.project'), '7b24d390e1ee056d');
  assert.equal(run('location.hash'), '#7b24d390e1ee056d');
});

test('board view preference defaults to board and persists explicit topology choice', () => {
  const run = app();
  run(`var saved={};
    localStorage={getItem:key=>saved[key]||null,setItem:(key,value)=>saved[key]=value};
    lastSnap=null;
    filters.project='p1';
    ['view-board','view-topo','columns','topology'].forEach(id=>{
      const el=document.getElementById(id);
      el.classList={_s:new Set(), toggle(name,on){ if(on) this._s.add(name); else this._s.delete(name); }};
      el.setAttribute=()=>{};
      el.disabled=false;
      el.title='';
      el.hidden=false;
      el.textContent='';
    });`);
  assert.equal(run('readView()'), 'board');
  run('saved["maw-board-view"]="invalid"');
  assert.equal(run('readView()'), 'board');
  run('setView("topo")');
  assert.equal(run('boardView'), 'topo');
  assert.equal(run('saved["maw-board-view"]'), 'topo');
  assert.equal(run('document.getElementById("columns").hidden'), true);
  assert.equal(run('document.getElementById("topology").hidden'), false);
  run('setView("board")');
  assert.equal(run('boardView'), 'board');
});

test('topology requires a single project; all-projects forces board view', () => {
  const run = app();
  run(`var saved={};
    localStorage={getItem:key=>saved[key]||null,setItem:(key,value)=>saved[key]=value};
    lastSnap=null;
    filters.project='';
    ['view-board','view-topo','columns','topology'].forEach(id=>{
      const el=document.getElementById(id);
      el.classList={_s:new Set(), toggle(name,on){ if(on) this._s.add(name); else this._s.delete(name); }};
      el.setAttribute=(k,v)=>{ el['attr_'+k]=v; };
      el.disabled=false;
      el.title='';
      el.hidden=false;
      el.textContent='';
    });`);
  assert.equal(run('canUseTopo()'), false);
  run('setView("topo")');
  assert.equal(run('boardView'), 'board');
  assert.equal(run('document.getElementById("view-topo").disabled'), true);
  run('setProjectFilter("proj-a")');
  assert.equal(run('canUseTopo()'), true);
  assert.equal(run('document.getElementById("view-topo").disabled'), false);
  run('setView("topo")');
  assert.equal(run('boardView'), 'topo');
  run('setProjectFilter("")');
  assert.equal(run('boardView'), 'board');
  assert.equal(run('document.getElementById("topology").hidden'), true);
  assert.equal(run('document.getElementById("view-topo").disabled'), true);
});

test('uiLang switches board column labels between en and zh', () => {
  const run = app();
  run('setLang("en")');
  assert.equal(run('COLUMNS.find(([s]) => s === "ready")[1]'), 'Ready');
  run('setLang("zh")');
  assert.equal(run('COLUMNS.find(([s]) => s === "ready")[1]'), '可领取');
  assert.equal(run('uiLang'), 'zh');
});

test('published page loads the built web bundle by relative path', () => {
  const html = fs.readFileSync(__dirname + '/page.html', 'utf8');
  assert.match(html, /<script id="board-web" src="assets\/board-web\.js" defer><\/script>/);
  // Relative src keeps the HTTP page and the file:// static export on the same URL.
  assert.equal(/src="\/assets\//.test(html), false);
  const dist = fs.statSync(__dirname + '/assets/dist/board-web.js');
  assert.equal(dist.isFile(), true);
  assert.ok(dist.size > 1024);
});

test('built web bundle exposes the mount placeholder API', () => {
  const source = fs.readFileSync(__dirname + '/assets/dist/board-web.js', 'utf8');
  assert.ok(source.startsWith('var MawBoard='), 'bundle is an IIFE assigned to MawBoard');
  assert.match(source, /mountBoard/);
});

// ---------- VN-704：统一布局挂载层 ----------

const shellUrl = path => require('node:url').pathToFileURL(__dirname + path).href;

async function loadShellModules() {
  return {
    registry: await import(shellUrl('/assets/shell/view-registry.mjs')),
    router: await import(shellUrl('/assets/shell/router.mjs')),
    i18n: await import(shellUrl('/assets/i18n/board-i18n.mjs')),
    context: await import(shellUrl('/assets/shell/view-context.mjs')),
  };
}

test('VN-704 注册表同时登记 legacy 与 vnext 视图且分组稳定', async () => {
  const { registry } = await loadShellModules();
  const views = registry.createRegistry();
  assert.deepEqual(views.ids, ['board', 'factory', 'topo', 'nodes', 'tasks', 'runs', 'deliveries']);
  assert.deepEqual(views.legacyIds, ['board', 'factory', 'topo']);
  assert.deepEqual(views.vnextIds, ['nodes', 'tasks', 'runs', 'deliveries']);
  assert.equal(views.get('nodes').headingKey, 'heading.nodes');
  assert.equal(views.get('nope'), null);
  // 未知视图不猜测，回退到看板。
  assert.equal(registry.resolveView(views, 'nope').id, 'board');
  const groups = views.navModel();
  assert.deepEqual(groups.map(group => group.id), ['flow', 'control']);
  assert.deepEqual(groups[1].items.map(def => def.id), ['nodes', 'tasks', 'runs', 'deliveries']);
});

test('VN-704 路由保留旧工程锚点并让 vnext 视图可往返', async () => {
  const { registry, router } = await loadShellModules();
  const views = registry.createRegistry();
  const known = id => Boolean(views.get(id));

  const legacy = router.parseRoute('', '#7b24d390e1ee056d', known);
  assert.equal(legacy.view, 'board');
  assert.equal(legacy.project, '7b24d390e1ee056d');
  assert.equal(legacy.fromHash, true);
  assert.equal(router.serializeRoute(legacy), '/#7b24d390e1ee056d');

  const detail = router.parseRoute('?view=runs&project=abc&id=run_1', '', known);
  assert.equal(detail.view, 'runs');
  assert.equal(detail.project, 'abc');
  assert.equal(detail.detail, 'run_1');
  assert.equal(router.serializeRoute(detail), '/?view=runs&project=abc&id=run_1#abc');

  // board 视图忽略 id，旧详情抽屉行为不被改写。
  assert.equal(router.parseRoute('?id=t-1', '', known).detail, '');
  // 未知视图回退看板，且不把 id 带进来。
  const unknown = router.parseRoute('?view=ghost&id=x', '', known);
  assert.equal(unknown.view, 'board');
  assert.equal(unknown.detail, '');
  assert.equal(router.serializeQuery({ view: 'board', project: 'abc' }), '');
  assert.equal(router.sameRoute(detail, router.parseRoute('?view=runs&project=abc&id=run_1', '', known)), true);
});

test('VN-704 全局语言只由语言簿收口并回退不产生 undefined', async () => {
  const { i18n } = await loadShellModules();
  assert.equal(i18n.normalizeLang('zh-CN'), 'en');
  assert.equal(i18n.normalizeLang('zh'), 'zh');
  assert.equal(i18n.translate('view.nodes', 'zh'), '节点');
  assert.equal(i18n.translate('view.nodes', 'en'), 'Nodes');
  assert.equal(i18n.translate('missing.key', 'zh'), 'missing.key');
  assert.equal(i18n.translate('missing.key', 'zz'), 'missing.key');
  assert.equal(i18n.translate('heading.nodes', 'zh', {}), '执行节点');
  // 语言偏好优先级：query > storage > navigator > 默认。
  assert.equal(i18n.readLangPreference({ search: '?lang=zh', storage: { getItem: () => 'en' }, navigatorLanguage: 'en-US' }), 'zh');
  assert.equal(i18n.readLangPreference({ search: '', storage: { getItem: () => 'zh' }, navigatorLanguage: 'en-US' }), 'zh');
  assert.equal(i18n.readLangPreference({ search: '', storage: null, navigatorLanguage: 'zh-CN' }), 'zh');
  assert.equal(i18n.readLangPreference({ search: '', storage: null, navigatorLanguage: 'fr' }), 'en');
  assert.equal(i18n.documentLang('zh'), 'zh-CN');
  const doc = { documentElement: { lang: 'en' } };
  assert.equal(i18n.applyDocumentLang(doc, 'zh'), 'zh-CN');
  assert.equal(doc.documentElement.lang, 'zh-CN');
});

test('VN-704 数据适配显式区分失败与待核实，pending/unknown 不被改写成成功', async () => {
  const { context } = await loadShellModules();
  assert.equal(context.tasksStateFromSnapshot(null).status, 'error');
  assert.equal(context.tasksStateFromSnapshot({}).status, 'error');

  const snapshot = { tasks: [
    { id: 'VN-1', status: 'ready', title: 'A' },
    { id: 'VN-2', status: 'claimed', title: 'B' },
  ] };
  const tasks = context.tasksStateFromSnapshot(snapshot);
  assert.equal(tasks.status, 'ok');
  assert.equal(tasks.tasks.length, 2);
  assert.equal(tasks.allTasks.length, 2);
  assert.equal(tasks.inProgressTasks.length, 1);
  assert.equal(tasks.inProgressTasks[0].id, 'VN-2');

  const nodes = context.nodesStateFrom({ nodes: [{ id: 'n1', presence: { state: 'unknown' } }] });
  assert.equal(nodes.status, 'ok');
  assert.equal(nodes.nodes[0].presence.state, 'unknown');
  const runs = context.runsStateFrom({ runs: [{ run_id: 'run_1', reconciliation: 'unknown' }] });
  assert.equal(runs.runs[0].reconciliation, 'unknown');
  const deliveries = context.deliveriesStateFrom({ deliveries: [{ delivery_id: 'dlv_1', verification: { state: 'pending' } }] });
  assert.equal(deliveries.deliveries[0].verification.state, 'pending');

  // 端点缺失 → 显式错误，绝不构造空成功。
  assert.equal(context.nodesStateFrom({ unavailable: true }).status, 'error');
  assert.equal(context.runsStateFrom(null).status, 'error');
  assert.equal(context.deliveriesStateFrom({ deliveries: null }).status, 'error');
  assert.equal(context.stateForKind('ghost', {}).status, 'error');

  assert.equal(context.findDetail('nodes', { nodes: [{ id: 'n1' }] }, 'n1').id, 'n1');
  assert.equal(context.findDetail('nodes', { nodes: [{ id: 'n1' }] }, 'n2'), null);

  const ctx = context.createViewContext({
    view: { id: 'nodes', legacy: false },
    lang: 'zh',
    route: { view: 'nodes', project: 'p1', detail: 'n1' },
    registry: { ids: ['board', 'nodes'] },
  });
  assert.equal(ctx.filters.project, 'p1');
  assert.equal(ctx.detailId, 'n1');
  assert.equal(Object.isFrozen(ctx), true);
  assert.equal(Reflect.set(ctx, 'lang', 'en'), false);
  assert.equal(ctx.lang, 'zh');
});

test('VN-704 页面在 legacy 之后挂载 shell，且不强制依赖 bundle', () => {
  const html = fs.readFileSync(__dirname + '/page.html', 'utf8');
  assert.match(html, /id="vnext-nav"/);
  assert.match(html, /id="vnext-view"/);
  assert.match(html, /id="vnext-detail"/);
  const legacyAt = html.indexOf('id="board-app"');
  const bundleAt = html.indexOf('id="board-web"');
  const bootAt = html.indexOf('id="workbench-bootstrap"');
  assert.ok(legacyAt >= 0 && bundleAt > legacyAt, 'bundle loads after the legacy app');
  assert.ok(bootAt > bundleAt, 'bootstrap runs after the bundle tag');
  assert.match(html, /typeof window\.MawBoard\.mountShell !== "function"\) return;/);
  const shell = fs.readFileSync(__dirname + '/assets/shell/board-shell.mjs', 'utf8');
  assert.match(shell, /export function mountShell/);
  assert.match(shell, /export function createShell/);
  // 样式只消费共享 token，不写死色值。
  const css = fs.readFileSync(__dirname + '/assets/shell/shell.css', 'utf8');
  assert.equal(/#[0-9a-fA-F]{3,6}/.test(css), false);
});
