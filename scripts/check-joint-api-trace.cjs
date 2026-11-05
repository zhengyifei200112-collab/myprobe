'use strict';

const assert = require('node:assert/strict');
const { createHash } = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const readline = require('node:readline');

const traceName = 'joint-api-requests.jsonl';
const fields = new Set(['endpoint', 'node', 'started_ns', 'headers_ns', 'elapsed_ns', 'status', 'failure', 'error_class']);
const classes = new Set(['deadline_exceeded', 'context_canceled', 'network_timeout', 'unexpected_eof', 'request_error']);
const isCount = (value) => Number.isSafeInteger(value) && value >= 0;

async function verify(resultFile) {
  const result = JSON.parse(fs.readFileSync(resultFile, 'utf8'));
  const trace = result.APIRequestTrace;
  const duration = result.DurationRequestedSeconds;
  assert(result.format === 'myprobe.joint-load.v1' && duration >= 90 && duration <= 48 * 3600);
  assert(isCount(result.Nodes) && result.Nodes >= 1 && result.Nodes <= 100);
  assert(trace.format === 'myprobe.joint-api-requests.v1' && trace.file === traceName);
  assert(trace.synced_and_closed && trace.write_errors === 0 && /^[a-f0-9]{64}$/.test(trace.sha256));
  assert.equal(trace.limit_bytes, (Math.floor(duration) + 2) * 4 * 256);
  assert(isCount(trace.bytes) && trace.bytes > 0 && trace.bytes <= trace.limit_bytes);
  const file = path.join(path.dirname(resultFile), traceName);
  const stat = fs.lstatSync(file);
  assert(stat.isFile() && stat.size === trace.bytes, 'trace file length or identity mismatch');

  const dense = result.HistoryFixture === 'dense-year';
  assert(dense || result.HistoryFixture === 'current-run-only');
  const history = new Set(dense ? ['history_dense_30d', 'history_dense_1y'] : ['history_current_30d_range']);
  const endpoints = new Map(['overview', 'health', ...history].map((name) => [name, []]));
  const nodeMax = new Map();
  const hash = createHash('sha256');
  const input = fs.createReadStream(file, { end: trace.bytes - 1 });
  let bytes = 0;
  let lastByte;
  input.on('data', (chunk) => {
    bytes += chunk.length;
    lastByte = chunk.at(-1);
    hash.update(chunk);
  });
  const lines = readline.createInterface({ input, crlfDelay: Infinity });
  let records = 0;
  let failures = 0;
  let previousEnd = 0;
  try {
    for await (const line of lines) {
      records++;
      assert(Buffer.byteLength(line) < 256, `trace row ${records} exceeds bound`);
      const row = JSON.parse(line);
      const { endpoint, node, started_ns: start, elapsed_ns: elapsed, headers_ns: headers,
        status, failure, error_class: errorClass } = row;
      assert(Object.keys(row).every((field) => fields.has(field)) && endpoints.has(endpoint), `invalid trace row ${records}`);
      assert(isCount(start) && start >= previousEnd && isCount(elapsed) && isCount(start + elapsed), `invalid time on row ${records}`);
      previousEnd = start + elapsed;
      assert(history.has(endpoint) ? isCount(node) && node >= 1 && node <= result.Nodes : node === undefined, `invalid node on row ${records}`);
      assert(headers === undefined || (isCount(headers) && headers <= elapsed), `invalid headers time on row ${records}`);
      if (failure !== undefined) failures++;
      if (failure === 'transport') {
        assert(status === undefined && headers === undefined && classes.has(errorClass), `invalid transport outcome on row ${records}`);
        continue;
      }
      assert(isCount(status) && status >= 100 && status <= 999, `invalid status on row ${records}`);
      if (failure === 'response_read' || failure === 'http_status') {
        assert(failure === 'response_read' ? classes.has(errorClass) : status !== 200 && errorClass === undefined, `invalid response outcome on row ${records}`);
        continue;
      }
      assert(status === 200 && errorClass === undefined && (failure === undefined ||
        (failure === 'invalid_history' && dense && history.has(endpoint)) ||
        (failure === 'invalid_json' && endpoint === 'health')), `invalid response on row ${records}`);
      // The producer includes a successful response's time even when its body
      // fails validation, while counting that validation failure separately.
      const milliseconds = elapsed / 1e6;
      endpoints.get(endpoint).push(milliseconds);
      if (node !== undefined) {
        const key = `${endpoint}/${node}`;
        const previous = nodeMax.get(key);
        if (!previous || milliseconds > previous.ms) nodeMax.set(key, { endpoint, node, ms: milliseconds, row: records });
      }
    }
  } finally {
    lines.close();
    input.destroy();
  }
  assert(bytes === trace.bytes && lastByte === 10 && hash.digest('hex') === trace.sha256, 'trace bytes or digest mismatch');
  assert(records > 0 && records === trace.events && failures === result.APIErrors, 'trace accounting mismatch');
  const distributions = {};
  for (const [name, values] of endpoints) {
    if (values.length === 0) continue;
    values.sort((a, b) => a - b);
    distributions[name] = { count: values.length, P50: values[Math.floor((values.length - 1) / 2)],
      P95: values[Math.ceil(95 * values.length / 100) - 1], Max: values.at(-1) };
  }
  assert.deepEqual(distributions, result.APILatency, 'trace does not reproduce API latency summary');
  return { trace_verified: true, records, failures, distributions, history_node_maxima: [...nodeMax.values()].sort((a, b) => b.ms - a.ms) };
}

if (require.main === module) {
  if (process.argv.length !== 3) {
    process.stderr.write('Usage: node scripts/check-joint-api-trace.cjs <joint-load-result.json>\n');
    process.exitCode = 2;
  } else {
    verify(process.argv[2]).then((result) => process.stdout.write(`${JSON.stringify(result, null, 2)}\n`), (err) => {
      process.stderr.write(`API trace verification failed: ${err.code || err.name}\n`);
      process.exitCode = 1;
    });
  }
}

module.exports = verify;
