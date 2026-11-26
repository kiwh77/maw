const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

test('Board routes filtered tasks into the yard, preserves project identity and stops it when hidden', () => {
  const elements = new Map();
  const element = id => {
    if (!elements.has(id)) elements.set(id, { classList: { toggle() {} }, setAttribute() {}, addEventListener() {}, querySelectorAll() { return []; }, hidden: false });
    return elements.get(id);
  };
  let received, visible;
  const context = vm.createContext({
    document: { getElementById: element, addEventListener() {}, querySelectorAll() { return []; }, documentElement: {} },
    window: { addEventListener() {}, MawBoard: { renderFactory(root, options) { received = options; }, setFactoryVisible(root, value) { visible = value; } } },
    localStorage: { getItem() { return null; }, setItem() {} },
    navigator: { language: 'en-US' }, location: { search: '' }, URLSearchParams, setTimeout, clearTimeout,
  });
  const source = fs.readFileSync(path.resolve(__dirname, '../../../assets/legacy/board-app.js'), 'utf8');
  vm.runInContext(source.split('// Bootstrap stays separate')[0], context);
  vm.runInContext(`filters.project='p'; searchTerm='needle'; boardView='factory'; renderWorkspace({tasks:[
    {id:'same',project_id:'p',title:'needle',status:'in_progress'},
    {id:'same',project_id:'q',title:'needle',status:'done'},
    {id:'other',project_id:'p',title:'different',status:'ready'}
  ]});`, context);
  assert.equal(received.tasks.length, 1);
  assert.equal(received.tasks[0].project_id, 'p');
  assert.equal(visible, true);
  assert.equal(typeof received.onTask, 'function');
  vm.runInContext('setView("board")', context);
  assert.equal(visible, false);
  assert.equal(element('factory').hidden, true);
});
