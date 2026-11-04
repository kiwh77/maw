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

test('built web bundle inlines tokens.css so production pages define shared density vars', () => {
  const source = fs.readFileSync(__dirname + '/assets/dist/board-web.js', 'utf8');
  // injectStyles 必须以 tokens 为唯一字级／间距源；缺定义时组件 var(--font-body) 等在浏览器为空。
  assert.match(source, /--font-body:\s*13px/);
  assert.match(source, /--line-body:\s*1\.5/);
  assert.match(source, /--row-height:\s*36px/);
  assert.match(source, /--space-block:\s*16px/);
  assert.match(source, /maw-vnext-styles/);
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
  // flow 三项 + archive（tasks/runs/deliveries）+ 统计六项 + 设置三项 + execution。
  assert.deepEqual(views.ids.slice(0, 6), ['board', 'factory', 'topo', 'tasks', 'runs', 'deliveries']);
  assert.deepEqual(views.legacyIds, ['board', 'factory', 'topo']);
  assert.equal(views.get('nodes').group, 'settings');
  assert.equal(views.get('problems').group, 'review');
  assert.equal(views.get('logs').group, 'review');
  assert.equal(views.ids.length, views.legacyIds.length + views.vnextIds.length);
  assert.equal(views.get('nodes').headingKey, 'heading.nodes');
  assert.equal(views.get('nope'), null);
  // 未知视图不猜测，回退到默认视图（EV-003 起执行视图是默认页）。
  assert.equal(registry.resolveView(views, 'nope').id, 'execution');
  const groups = views.navModel();
  assert.deepEqual(groups.map(group => group.id), ['flow', 'archive', 'review', 'settings']);
  assert.deepEqual(groups[1].items.map(def => def.id), ['tasks', 'runs', 'deliveries']);
  assert.deepEqual(groups[3].items.map(def => def.id), ['projects', 'nodes', 'settings']);

  // 置顶工作台：唯一进侧栏的 flow 项，菜单名单独给（nav.workbench），不占用视图标题。
  assert.deepEqual(views.sidebarPinned().map(def => def.id), ['board']);
  assert.equal(views.sidebarPinned()[0].navKey, 'nav.workbench');
  assert.equal(registry.navLabelKey(views.sidebarPinned()[0]), 'nav.workbench');
  assert.equal(registry.navLabelKey(views.get('nodes')), 'view.nodes');
  const sidebarIds = views.sidebarNavModel().flatMap(group => group.items.map(def => def.id));
  assert.equal(sidebarIds.includes('board'), false, '置顶项不再重复出现在分组里');
  assert.equal(sidebarIds.includes('factory'), false);
  assert.equal(sidebarIds.includes('tasks'), false, 'archive 深链不进侧栏');
  assert.equal(sidebarIds.includes('nodes'), true, '节点在设置组');
  assert.equal(sidebarIds.includes('problems'), true);
  assert.equal(sidebarIds.includes('logs'), true);
  // 统计 6 + 设置 3（projects/nodes/settings）
  assert.equal(sidebarIds.length, 9);
});

test('VN-704 路由保留旧工程锚点并让 vnext 视图可往返', async () => {
  const { registry, router } = await loadShellModules();
  const views = registry.createRegistry();
  const known = id => Boolean(views.get(id));

  const legacy = router.parseRoute('', '#7b24d390e1ee056d', known);
  assert.equal(legacy.view, 'execution', '旧锚点只带工程，视图走默认页');
  assert.equal(legacy.project, '7b24d390e1ee056d');
  assert.equal(legacy.fromHash, true);
  assert.equal(router.serializeRoute(legacy), '/#7b24d390e1ee056d');

  const detail = router.parseRoute('?view=runs&project=abc&id=run_1', '', known);
  assert.equal(detail.view, 'runs');
  assert.equal(detail.project, 'abc');
  assert.equal(detail.detail, 'run_1');
  // 工程只写锚点，不重复写进查询串（同一份筛选不留两个出处）。
  assert.equal(router.serializeRoute(detail), '/?view=runs&id=run_1#abc');

  // 没有合法 view 时不分有无工程，都回默认页；只有能开详情的视图才收 id。
  assert.equal(router.parseRoute('?id=t-1', '', known).view, 'execution');
  assert.equal(router.parseRoute('?id=t-1', '', known).detail, '');
  // 旧看板深链仍然可达，且它不吃 id（详情在 legacy 抽屉里）。
  assert.equal(router.parseRoute('?view=board', '', known).view, 'board');
  assert.equal(router.parseRoute('?view=board&id=t-1', '', known).detail, '');
  const unknown = router.parseRoute('?view=ghost&id=x', '', known);
  assert.equal(unknown.view, 'execution');
  assert.equal(unknown.detail, '', '未知视图回退到默认页，不把 id 当详情');
  assert.equal(router.parseRoute('?view=ghost&project=p-9&id=x', '', known).view, 'execution');
  // 默认视图不参与序列化：执行视图的地址就是不带 view 参数的 /。
  assert.equal(router.serializeQuery({ view: 'execution', project: 'abc' }), '');
  assert.equal(router.serializeQuery({ view: 'board', project: 'abc' }), '?view=board');
  assert.equal(router.sameRoute(detail, router.parseRoute('?view=runs&project=abc&id=run_1', '', known)), true);
  assert.deepEqual(
    views.sidebarNavModel().map(group => group.id),
    ['review', 'settings'],
  );
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

test('VN-704 页面以 Vue 挂载点 + board-web bundle 启动', () => {
  const html = fs.readFileSync(__dirname + '/page.html', 'utf8');
  assert.match(html, /id="app"/);
  assert.match(html, /id="board-web"/);
  assert.match(html, /src="assets\/board-web\.js"/);
  assert.equal(html.includes('id="board-app"'), false);
  assert.equal(html.includes('workbench-bootstrap'), false);
  const shell = fs.readFileSync(__dirname + '/assets/shell/board-shell.mjs', 'utf8');
  assert.match(shell, /export function mountShell/);
  assert.match(shell, /export function createShell/);
  // 样式只消费共享 token，不写死色值。
  const css = fs.readFileSync(__dirname + '/assets/shell/shell.css', 'utf8');
  assert.equal(/#[0-9a-fA-F]{3,6}/.test(css), false);
});

// ---------- VN-705：挂载回顾统计与设置入口 ----------

async function loadReviewModules() {
  return {
    registry: await import(shellUrl('/assets/shell/view-registry.mjs')),
    router: await import(shellUrl('/assets/shell/router.mjs')),
    i18n: await import(shellUrl('/assets/i18n/board-i18n.mjs')),
    context: await import(shellUrl('/assets/shell/view-context.mjs')),
    shell: await import(shellUrl('/assets/shell/board-shell.mjs')),
  };
}

test('VN-705 回顾／统计／设置登记为独立分组，与执行视图共用同一份注册表', async () => {
  const { registry, i18n } = await loadReviewModules();
  const views = registry.createRegistry();

  assert.deepEqual(views.navModel().map(group => group.id), ['flow', 'archive', 'review', 'settings']);
  const review = views.navModel().find(group => group.id === 'review');
  assert.deepEqual(review.items.map(def => def.id), ['timeline', 'activity', 'sessions', 'analytics', 'problems', 'logs']);
  const settings = views.navModel().find(group => group.id === 'settings');
  assert.deepEqual(settings.items.map(def => def.id), ['projects', 'nodes', 'settings']);
  assert.equal(i18n.translate('nav.review', 'zh'), '统计');

  // 每个视图的四组语言键都必须存在，且中英都有；缺一版等于没做完。
  views.all.forEach(def => {
    const keys = [def.titleKey, def.headingKey, def.descKey, def.eyebrowKey];
    if (def.navKey) keys.push(def.navKey);
    keys.forEach(key => {
      assert.notEqual(i18n.translate(key, 'zh'), key, 'missing zh: ' + key);
      assert.notEqual(i18n.translate(key, 'en'), key, 'missing en: ' + key);
    });
  });
  assert.deepEqual(Object.keys(i18n.DICTIONARY.zh).sort(), Object.keys(i18n.DICTIONARY.en).sort());

  // 分组标题也是语言键，不是硬编码中文。
  views.navModel().forEach(group => {
    assert.notEqual(i18n.translate(group.labelKey, 'zh'), group.labelKey);
  });

  // 观测类视图按工程物化，必须在注册表里声明，不能到渲染时再猜。
  assert.deepEqual(views.vnextIds.filter(id => views.requiresProject(id)),
    ['timeline', 'activity', 'sessions', 'analytics']);
  assert.equal(views.requiresProject('projects'), false);
  assert.equal(views.requiresProject('settings'), false);
});

test('VN-705 切换回顾与统计保留同一套筛选，URL 与面包屑能恢复上下文', async () => {
  const { registry, router } = await loadReviewModules();
  const views = registry.createRegistry();
  const known = id => Boolean(views.get(id));

  const route = router.parseRoute('?view=timeline&project=p-9&agent=a-1&status=done&q=VN-70', '#p-9', known);
  assert.equal(route.view, 'timeline');
  assert.equal(route.project, 'p-9');
  assert.equal(route.agent, 'a-1');
  assert.equal(route.status, 'done');
  assert.equal(route.search, 'VN-70');

  // 同一份路由切到统计，筛选原样带走；地址可再次解析回相同状态。
  const switched = router.withRoute(route, { view: 'analytics', detail: '' });
  assert.equal(router.serializeRoute(switched), '/?view=analytics&agent=a-1&status=done&q=VN-70#p-9');
  const back = router.parseRoute('?view=analytics&project=p-9&agent=a-1&status=done&q=VN-70', '#p-9', known);
  const { fromHash, ...comparable } = back;
  assert.deepEqual(comparable, switched);
  assert.equal(fromHash, false, '显式 ?project= 优先，不再算作仅靠锚点恢复');

  // 面包屑：分组格由 shell 独占，工程写在 #workspace-name（旧看板同一口径）。
  assert.equal(registry.groupLabelKey(views.get(back.view).group), 'nav.review');
  assert.equal(back.project, 'p-9');

  // 未知视图仍然回退看板，不会留在一个空白页上。
  assert.equal(router.parseRoute('?view=ghost&project=p-9', '', known).view, 'execution');
});

test('VN-705 观测适配：能力缺失降级为「未提供该数据」，不构造空成功', async () => {
  const { context } = await loadReviewModules();

  const unsupported = { status: 'unsupported', capability: { name: 'stats', supported: false, reason: 'source_not_configured' } };
  const missing = context.analyticsStateFrom(unsupported);
  assert.equal(missing.status, 'error');
  assert.equal(missing.reason, 'unavailable');
  assert.equal(missing.capability, 'source_not_configured');

  // 端点整条缺失与「能力没接」走同一条降级路径。
  assert.equal(context.timelineStateFrom({ unavailable: true }).reason, 'unavailable');
  assert.equal(context.activityStateFrom({ unsupported: [{ capability: 'activity', reason: 'window_too_large' }] }).status, 'error');
  assert.equal(context.sessionsStateFrom(null).status, 'error');
  assert.equal(context.analyticsStateFrom({ status: 'ready' }).status, 'error');

  // 覆盖不全仍然可用：状态保留 unknown，不降级成失败，也不补成满值。
  const partial = context.timelineStateFrom({
    status: 'partial',
    completeness: 'partial',
    items: [{ interval_id: 'iv_1', kind: 'cli_process', start_at: '2026-10-24T01:00:00Z', end_at: '2026-10-24T01:30:00Z', quality: 'observed' }],
    window: { start: '2026-10-24T00:00:00Z', end: '2026-10-24T06:00:00Z', timezone: 'Asia/Shanghai', hours: 6 },
    watermark: { cursor: 41, complete_before: 39, received_at: '2026-10-24T06:00:00Z', gaps: [{ from: 39, to: 41, reason: 'ingest_lag' }] },
  });
  assert.equal(partial.status, 'ready');
  assert.equal(partial.completeness, 'partial');
  assert.equal(partial.intervals.length, 1);
  assert.equal(partial.cursors.state_cursor, 41);
  assert.equal(partial.cursors.projection_cursor, 39);
  assert.equal(partial.cursors.gap_count, 1);
  assert.equal(partial.window.hours, 6);

  const unknown = context.timelineStateFrom({ status: 'unknown', completeness: 'unknown', items: [] });
  assert.equal(unknown.status, 'unknown');
  assert.equal(unknown.cursors, null, '没有水位就不编游标数字');

  const sessions = context.sessionsStateFrom({ status: 'ready', completeness: 'complete', items: [{ session_id: 's1', turns: [] }] });
  assert.equal(sessions.sessions.length, 1);
  assert.equal(context.activityStateFrom({ status: 'ready', hours: null }).status, 'error');
});

test('VN-705 缺工程时不发请求也不出空表：状态是「未选上下文」', async () => {
  const { context, shell } = await loadReviewModules();

  assert.equal(context.needsProject('timeline'), true);
  assert.equal(context.needsProject('nodes'), false);
  assert.equal(shell.needsProject('analytics'), true);
  assert.deepEqual(context.NO_PROJECT_STATE, { status: 'no_project', reason: 'project_required' });

  // 地址按工程拼，工程身份必须转义，避免拼出另一个路径。
  assert.equal(shell.projectResourcePath('timeline', 'p 1/2'), '/api/v1/projects/p%201%2F2/timeline');
  assert.equal(shell.projectResourcePath('analytics', 'p-1'), '/api/v1/projects/p-1/analytics');
  assert.equal(shell.projectResourcePath('nodes', 'p-1'), '');
  assert.equal(shell.projectResourcePath('timeline', '  '), '');
  assert.equal(shell.capabilitiesPath('p-1'), '/api/v1/projects/p-1/observability/capabilities');

  // 没有工程的默认数据源不打网络：直接判不可用，由挂载层换成「先选工程」。
  let asked = 0;
  const sources = shell.createDefaultSources({ fetch: () => { asked += 1; return Promise.resolve({ ok: true, json: async () => ({}) }); } });
  assert.deepEqual(await sources.timeline({ project: '' }), { unavailable: true, status: 0 });
  assert.equal(asked, 0);
});

test('VN-705 设置页只搬字段不补默认值，只读由地址与中枢版本决定', async () => {
  const { context, shell } = await loadReviewModules();
  const { normalizePolicy } = await import(shellUrl('/assets/views/settings/settings-view.mjs'));

  const state = context.settingsStateFrom(
    { ok: true, auto_execution: { enabled: true, managed_execution_limit: 2, config_version: 7, platform_supported: true } },
    { normalizePolicy }
  );
  assert.equal(state.status, 'ready');
  // 命名对齐：中枢说 enabled / config_version，策略认 auto_enabled / revision。
  assert.equal(state.policy.autoEnabled, true);
  assert.equal(state.policy.limit, 2);
  assert.equal(state.policy.revision, 7);
  assert.equal(state.platformSupported, true);
  // 缺的字段保持 null，不替服务端宣布 0 或 1。
  assert.equal(state.policy.capacity, null);
  assert.equal(state.policy.ref, null);
  assert.equal(state.policy.retentionDays, null);
  assert.equal(state.readOnly, false);

  const readOnly = context.settingsStateFrom({ auto_execution: { enabled: false } }, { normalizePolicy, readOnly: true });
  assert.equal(readOnly.readOnly, true);
  assert.equal(readOnly.readOnlyReason, 'static_export');

  // 服务端没说平台是否支持时保持 undefined，不猜成「支持」。
  assert.equal(context.settingsStateFrom({ policy: {} }, { normalizePolicy }).platformSupported, undefined);
  assert.equal(context.settingsStateFrom({ unavailable: true }).status, 'error');

  // 静态导出就是 file:// 打开的页面：没有同源中枢，写入口必须关。
  assert.equal(shell.isStaticExport({ location: { protocol: 'file:' } }), true);
  assert.equal(shell.isStaticExport({ location: { protocol: 'http:' } }), false);

  const exports = shell.createDefaultSources(null);
  assert.equal(typeof exports.settings, 'function');
  assert.equal(typeof exports.saveSettings, 'undefined', '本构建没有策略写入口，不提供假的保存函数');
});

test('VN-705 工程清单复用看板快照，并说明快照缺策略而不是默认成关闭', async () => {
  const { context } = await loadReviewModules();
  const state = context.projectsStateFrom({
    projects: [{ id: 'p-1', name: 'Alpha', max_parallel: 3 }],
  });
  assert.equal(state.status, 'ok');
  assert.equal(state.projects[0].max_parallel_reference, 3, 'max_parallel 与 max_parallel_reference 是同一个建议并行数');
  // 参考值出现不代表中枢说过自动模式开还是关：策略仍算未知。
  assert.equal(state.policyKnown, false);

  const noPolicy = context.projectsStateFrom({ projects: [{ id: 'p-2', name: 'Beta' }] });
  assert.equal(noPolicy.policyKnown, false);
  const withPolicy = context.projectsStateFrom({ projects: [{ id: 'p-3', policy: { auto_enabled: true, managed_execution_limit: 2 } }] });
  assert.equal(withPolicy.policyKnown, true);

  assert.equal(context.projectsStateFrom({}).status, 'error');
  assert.equal(context.projectsStateFrom({ unavailable: true }).status, 'error');
  assert.equal(context.projectsStateFrom({ projects: [] }).status, 'ok');
});

test('工程清单的任务计数按工程归组，快照没有任务行时不写 0', async () => {
  const { context } = await loadReviewModules();
  const withTasks = context.projectsStateFrom({
    projects: [{ id: 'p-1', name: 'Alpha' }, { id: 'p-2', name: 'Beta' }],
    tasks: [
      { project_id: 'p-1', status: 'ready' },
      { project_id: 'p-1', status: 'in_progress' },
      { project_id: 'p-1', status: 'done' },
      { project_id: 'p-2', status: 'claimed' },
      { project_id: '', status: 'ready' },
      { status: 'ready' },
    ],
  });
  assert.deepEqual(withTasks.taskCounts['p-1'], { total: 3, running: 1 });
  assert.deepEqual(withTasks.taskCounts['p-2'], { total: 1, running: 1 });
  // 没有 project_id 的行不记到任何工程名下。
  const totals = Object.values(withTasks.taskCounts).reduce((sum, c) => sum + c.total, 0);
  assert.equal(totals, 4);

  // 快照根本没有 tasks 字段：不写 taskCounts，视图据此写「任务数未知」。
  assert.equal('taskCounts' in context.projectsStateFrom({ projects: [{ id: 'p-1' }] }), false);
  // 有 tasks 但一道都不属于这些工程：仍是「已知」，只是都是 0。
  const empty = context.projectsStateFrom({ projects: [{ id: 'p-1' }], tasks: [] });
  assert.deepEqual(empty.taskCounts, {});
});

test('VN-705 页面骨架留出面包屑范围位，静态导出不引入绝对资源路径', () => {
  const html = fs.readFileSync(__dirname + '/page.html', 'utf8');
  assert.match(html, /id="app"/);
  assert.equal(/src="\/assets\//.test(html), false);
  // 面包屑范围位迁到 Vue App；相对资源路径仍由 page 引用。
  const app = fs.readFileSync(__dirname + '/web/src/App.vue', 'utf8');
  assert.match(app, /id="crumb-scope"/);
  assert.match(app, /id="workspace-name"/);
  assert.ok(app.indexOf('id="crumb-scope"') < app.indexOf('id="workspace-name"'));

  const css = fs.readFileSync(__dirname + '/assets/shell/shell.css', 'utf8');
  assert.match(css, /\.vnext-notice/);
  assert.equal(/#[0-9a-fA-F]{3,6}/.test(css), false, '样式只消费共享 token，不写死色值');
});

// ---------- BP-STA-001：统计组的问题与日志 ----------

test('BP-STA-001 问题与日志的适配层不把缺失当空成功', async () => {
  const { context } = await loadReviewModules();

  // 问题页：契约用 items，兼容旧载荷的 problems；两者都没有就是「没读全」。
  assert.equal(context.problemsStateFrom({ items: [{ kind: 'blocked' }] }).status, 'ok');
  assert.equal(context.problemsStateFrom({ problems: [{ kind: 'run' }] }).status, 'ok');
  assert.equal(context.problemsStateFrom({ items: [] }).status, 'ok', '空列表是查过了没有');
  assert.equal(context.problemsStateFrom({}).status, 'error');
  assert.equal(context.problemsStateFrom({ unavailable: true }).status, 'error');
  assert.equal(context.problemsStateFrom({ status: 401 }).status, 'unauthenticated');

  // 日志页：契约用 items，兼容 events；缺两者判失败，不用空表冒充「最近没事件」。
  assert.equal(context.logsStateFrom({ items: [{ kind: 'x' }] }).status, 'ok');
  assert.equal(context.logsStateFrom({ events: [{ kind: 'x' }] }).status, 'ok');
  assert.equal(context.logsStateFrom({ items: [] }).status, 'ok');
  assert.equal(context.logsStateFrom({}).status, 'error');
  assert.equal(context.logsStateFrom({ unavailable: true }).status, 'error');
  assert.equal(context.logsStateFrom({ status: 401 }).status, 'unauthenticated');

  // 两类都走自己的分支，不能被 default 的 unknown_kind 吃掉。
  assert.equal(context.stateForKind('problems', { items: [] }).status, 'ok');
  assert.equal(context.stateForKind('logs', { items: [] }).status, 'ok');
  assert.equal(context.stateForKind('ghost', {}).status, 'error');
  assert.equal(context.DATA_KINDS.includes('problems'), true);
  assert.equal(context.DATA_KINDS.includes('logs'), true);
});

test('BP-STA-001 日志页从 /api/v1/events 取数，问题页从快照与运行提炼信号', async () => {
  const sources = fs.readFileSync(__dirname + '/web/src/lib/sources.ts', 'utf8');
  assert.match(sources, /'\/api\/v1\/events\?/, '日志页必须走事件补拉接口');
  assert.match(sources, /async logs\(route: RouteLike\)/);
  // 问题页复用看板快照并按当前筛选提炼，不再造第二个聚合接口。
  assert.match(sources, /async problems\(route: RouteLike\)/);
  ['blocked', 'run', 'agent'].forEach(kind => {
    assert.match(sources, new RegExp("kind: '" + kind + "'"), '问题信号缺少类型：' + kind);
  });

  // 渲染器必须同时接进线上宿主与旧挂载层，否则注册表里多出两个点不开的入口。
  const host = fs.readFileSync(__dirname + '/web/src/components/VnextHost.vue', 'utf8');
  assert.match(host, /problems: renderProblems as never/);
  assert.match(host, /logs: renderLogs as never/);
  const shell = fs.readFileSync(__dirname + '/assets/shell/board-shell.mjs', 'utf8');
  assert.match(shell, /problems: renderProblems/);
  assert.match(shell, /logs: renderLogs/);
});
