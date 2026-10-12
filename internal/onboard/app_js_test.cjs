// Exercise the shipped script with controlled HTTP responses and timers.
// No browser, desktop, credentials, or real network is used.
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const script = fs.readFileSync(path.join(__dirname, 'web/app.js'), 'utf8');
const html = fs.readFileSync(path.join(__dirname, 'web/index.html'), 'utf8');
class Element {
  constructor(tag, attrs = '') {
    this.tag = tag; this.children = []; this.value = ''; this.disabled = false;
    this.dataset = {}; this.listeners = {}; this.text = '';
    this.id = /\bid="([^"]+)"/.exec(attrs)?.[1];
    this.dataset.action = /\bdata-action="([^"]+)"/.exec(attrs)?.[1];
  }
  set textContent(v) { this.text = v; this.children = []; }
  get textContent() { return this.text + this.children.map(c => c.textContent).join(' '); }
  replaceChildren(...children) { this.text = ''; this.children = children; }
  append(...children) { this.children.push(...children); }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  click() {}
}
function status(overrides = {}) {
  return {
    device: {name: 'Fixture device', device_id: 'fixture', os: 'darwin', paused: true, activity: {available: true, locked: false}},
    tunnel: {ready: true}, permissions: {state: 'granted'}, autostart: true,
    autostart_status: {reason: 'registered'}, driver_connected: true,
    plugin: {devices_seen: true, image_seen: true, image_confirmed: false}, ...overrides
  };
}
const response = (data, code = 200) => ({ok: code === 200, status: code, json: async () => data});
const flush = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };
async function harness(initial = status()) {
  const nodes = [...html.matchAll(/<([a-z]+)\b([^>]*)>/g)].map(m => new Element(m[1], m[2]));
  const ids = new Map(nodes.filter(n => n.id).map(n => [n.id, n]));
  const timers = new Map(), calls = [], queue = [response({csrf: 'fixture-csrf'}), response(initial)];
  let nextTimer = 0;
  const context = vm.createContext({
    document: {
      getElementById: id => ids.get(id), createElement: tag => new Element(tag),
      querySelectorAll: selector => nodes.filter(n => {
        if (selector === 'button, input') return ['button', 'input'].includes(n.tag);
        if (selector === '[data-action]') return !!n.dataset.action;
        if (selector === '[data-action^=permissions]') return n.dataset.action?.startsWith('permissions');
        throw new Error('unexpected selector');
      })
    },
    location: {hash: '', pathname: '/onboard/'}, history: {replaceState() {}},
    AbortController, Blob, URL, confirm: () => true,
    navigator: {clipboard: {writeText: async () => {}}},
    setTimeout: fn => { const id = ++nextTimer; timers.set(id, fn); return id; },
    clearTimeout: id => timers.delete(id),
    fetch: async (url, options) => {
      calls.push({url, body: JSON.parse(options.body), csrf: options.headers['X-DotControl-CSRF']});
      assert.ok(queue.length, 'unexpected request');
      const next = queue.shift(); if (next instanceof Error) throw next; return await next;
    }
  });
  await vm.runInContext(script, context); await flush();
  return {ids, nodes, timers, calls, queue, context, refresh: () => vm.runInContext('refresh()', context)};
}
test('auth expiry clears status and CSRF, stops polling, and disables actions', async () => {
  const h = await harness(); h.ids.get('runtime-key').value = 'private-fixture';
  assert.equal(h.timers.size, 1);
  h.queue.push(response({}, 401)); await h.refresh();
  assert.equal(h.timers.size, 0);
  assert.equal(vm.runInContext('state', h.context), null);
  assert.equal(vm.runInContext('csrf', h.context), '');
  assert.equal(h.ids.get('runtime-key').value, '');
  assert.ok(h.nodes.filter(n => ['button', 'input'].includes(n.tag)).every(n => n.disabled));
  assert.match(h.ids.get('message').textContent, /重新打开/);
  assert.doesNotMatch(h.ids.get('checks').textContent, /Fixture device|已授权/);
});
test('late response cannot restore an expired session', async () => {
  const h = await harness(); let complete;
  h.queue.push(new Promise(resolve => { complete = resolve; }));
  const old = h.refresh();
  h.queue.push(response({}, 403)); await h.refresh();
  complete(response(status())); await old;
  assert.equal(vm.runInContext('state', h.context), null);
  assert.equal(h.ids.get('confirm-image').disabled, true);
});
test('action finally does not re-enable controls after CSRF rejection', async () => {
  const h = await harness(); h.queue.push(response({}, 403));
  await h.ids.get('confirm-image').onclick();
  assert.ok(h.nodes.filter(n => n.tag === 'button').every(n => n.disabled));
  assert.equal(h.timers.size, 0);
});
test('transient network failure clears stale status and next poll restores valid state', async () => {
  const h = await harness(); h.queue.push(new Error('offline')); await h.refresh();
  assert.equal(vm.runInContext('state', h.context), null);
  assert.equal(h.ids.get('confirm-image').disabled, true);
  assert.equal(h.timers.size, 1);
  h.queue.push(response(status())); await h.refresh();
  assert.equal(h.ids.get('confirm-image').disabled, false);
  assert.match(h.ids.get('checks').textContent, /Fixture device/);
  assert.equal(h.calls.at(-1).csrf, 'fixture-csrf');
});
test('older status response cannot replace a newer one', async () => {
  const h = await harness(); let complete;
  h.queue.push(new Promise(resolve => { complete = resolve; })); const old = h.refresh();
  h.queue.push(response(status({plugin: {image_seen: true, image_confirmed: true}}))); await h.refresh();
  complete(response(status())); await old;
  assert.match(h.ids.get('image-delivery-state').textContent, /你已确认/);
  assert.match(h.ids.get('checks').textContent, /图片显示已确认/);
});
test('image confirmation stays human-triggered and updates permission wording', async () => {
  const h = await harness(status({plugin: {image_seen: false}}));
  assert.equal(h.ids.get('confirm-image').disabled, true);
  h.queue.push(response(status())); await h.refresh();
  assert.equal(h.ids.get('confirm-image').disabled, false);
  assert.equal(h.calls.filter(c => c.url.endsWith('/action')).length, 0);
  h.queue.push(response({}), response(status({plugin: {image_seen: true, image_confirmed: true}})));
  await h.ids.get('confirm-image').onclick();
  assert.deepEqual(h.calls.find(c => c.url.endsWith('/action')).body, {action: 'confirm_image'});
  assert.equal(h.ids.get('confirm-image').disabled, true);
  assert.match(h.ids.get('checks').textContent, /图片显示已确认/);
});
test('helper failure is not presented as missing permission', async () => {
  const s = status({activity_status: {reason: 'helper_unavailable'}}); s.device.activity.available = false;
  const h = await harness(s);
  assert.match(h.ids.get('checks').textContent, /辅助程序未运行/);
  assert.doesNotMatch(h.ids.get('checks').textContent, /等待.*输入监控权限/);
});
