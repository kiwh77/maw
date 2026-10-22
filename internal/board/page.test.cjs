const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');

// Exercise the shipped inline application, with only startup and DOM edges stubbed.
function app() {
  const html = fs.readFileSync(__dirname + '/page.html', 'utf8');
  const source = html.match(/<script id="board-app">([\s\S]*?)<\/script>/)[1];
  const theme = html.match(/<script id="theme-init">([\s\S]*?)<\/script>/)[1];
  const elements = new Map();
  const document = {
    getElementById(id) {
      if (!elements.has(id)) elements.set(id, { innerHTML: '', value: '', addEventListener() {} });
      return elements.get(id);
    },
    addEventListener() {},
  };
  const context = vm.createContext({ document, URLSearchParams, setTimeout, clearTimeout });
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
    ['view-board','view-topo','columns','topology'].forEach(id=>{
      const el=document.getElementById(id);
      el.classList={_s:new Set(), toggle(name,on){ if(on) this._s.add(name); else this._s.delete(name); }};
      el.setAttribute=()=>{};
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
