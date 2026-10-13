// Pure navigation contract checks: no browser, server, network or generated file.
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const ts = require('../node_modules/typescript');
const source = fs.readFileSync(path.join(__dirname, '../src/notification-links.ts'), 'utf8');
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
const moduleBox = { exports: {} };
new Function('module', 'exports', compiled)(moduleBox, moduleBox.exports);
const { parseNotificationTarget: parse, notificationPath: route, rememberNotificationTarget: save, pendingNotificationTarget: pending, serviceDiagnosticAt: at } = moduleBox.exports;
const target = { kind: 'service', id: 'service-id', observer: 'observer-id', start: '2026-01-01T00:00:00.000000001Z', end: '2026-02-01T00:00:00.000000001Z' };
assert.deepEqual(parse(route(target).slice(6)).target, target);
const q = new URLSearchParams(route(target).slice(7));
for (const [key, value] of [['end', '2026-02-01T00:00:00.000000002Z'], ['start', '2026-02-30T00:00:00Z'], ['start', '2026-01-01T24:00:00Z'], ['start', '1969-12-31T23:59:59Z'], ['end', '2101-01-01T00:00:00Z'], ['observer', '//private.test'], ['service', 'a b'], ['end', target.start]]) {
  const changed = new URLSearchParams(q); changed.set(key, value); assert.equal(parse(`?${changed}`).target, null);
}
for (const suffix of ['&observer=duplicate', '&service=duplicate', '&start=x', '&end=x', '&incident=one', '&aggregate=one', '&snapshot=' + 'a'.repeat(64)]) assert.equal(parse(`?${q}${suffix}`).target, null);
for (const incomplete of ['?service=one', '?observer=one', '?start=2026-01-01T00:00:00Z', '?incident=one&observer=two']) assert.equal(parse(incomplete).target, null);
assert.deepEqual(parse('?incident=one').target, { kind: 'incident', id: 'one' });
assert.deepEqual(parse('?aggregate=one&snapshot=' + 'a'.repeat(64)).target, { kind: 'aggregate', id: 'one', snapshot: 'a'.repeat(64) });
const storage = new Map();
global.sessionStorage = { setItem: (k, v) => storage.set(k, v), getItem: k => storage.get(k) || null, removeItem: k => storage.delete(k) };
save(target); assert.deepEqual(pending(), target);
const key = 'myprobe-notification-navigation-v1';
for (const record of [{ path: route(target), savedAt: Date.now() - 3600001 }, { path: route(target), savedAt: Date.now() + 60000 }, { path: 'https://outside.test/admin', savedAt: Date.now() }, { path: route(target) + '&extra=one', savedAt: Date.now() }]) {
  storage.set(key, JSON.stringify(record)); assert.equal(pending(), null); assert.equal(storage.has(key), false);
}
assert.equal(at('service', 'node', 'invalid'), '');
assert.equal(at('service', 'node', '+275760-09-13T00:00:00.000Z'), '');
assert.equal(parse(at('service', 'node', '2026-01-01T00:00:00Z').slice(6)).target.kind, 'service');
console.log('Notification navigation contracts passed.');
