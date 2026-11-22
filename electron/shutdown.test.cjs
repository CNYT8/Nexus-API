const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

// Execute the real entry file with inert Electron, child process, and timers.
// No app, backend, network connection, or filesystem mutation is started.
function shutdownFixture({ platform = 'linux', timeout, closeOnSignal = false } = {}) {
  const app = new EventEmitter();
  let exits = 0;
  app.exit = () => { exits++; };
  app.whenReady = () => ({ then() {} });
  const timers = new Map();
  let now = 0;
  let nextTimer = 1;
  const child = new EventEmitter();
  child.exitCode = null;
  child.signalCode = null;
  const signals = [];
  child.kill = (signal) => {
    signals.push(signal);
    if (closeOnSignal) {
      child.exitCode = 0;
      child.emit('close', 0);
    }
    return true;
  };
  const pipeMessages = [];
  child.stdin = new EventEmitter();
  child.stdin.end = (message) => { pipeMessages.push(message); };
  const sandbox = {
    require(name) {
      if (name === 'electron') return { app };
      if (name === 'child_process') {
        return { spawn() { throw new Error('test must not spawn a backend'); } };
      }
      return require(name);
    },
    process: { platform, env: timeout === undefined ? {} : { SHUTDOWN_TIMEOUT_SECONDS: timeout } },
    console: { log() {}, error() {} },
    __dirname,
    Date: { now: () => now },
    setTimeout(callback, delay) {
      const id = nextTimer++;
      timers.set(id, { callback, delay });
      return id;
    },
    clearTimeout(id) { timers.delete(id); },
    child,
  };
  const context = vm.createContext(sandbox);
  vm.runInContext(fs.readFileSync(path.join(__dirname, 'main.js'), 'utf8'), context, { filename: 'electron/main.js' });
  vm.runInContext('serverProcess = child;', context);
  let prevented = 0;
  return {
    child, signals, pipeMessages, timers,
    get exits() { return exits; },
    get prevented() { return prevented; },
    quit() { app.emit('before-quit', { preventDefault() { prevented++; } }); },
    fireNextTimer() {
      const [id, timer] = timers.entries().next().value;
      timers.delete(id);
      now += timer.delay;
      timer.callback();
    },
  };
}

test('SIGTERM gets the default 120 second drain budget plus exit margin', () => {
  const fixture = shutdownFixture();
  fixture.quit();
  assert.deepEqual(fixture.signals, ['SIGTERM']);
  assert.equal(fixture.exits, 0);
  assert.equal(fixture.timers.values().next().value.delay, 125000);
  fixture.quit();
  assert.equal(fixture.prevented, 2);
  assert.equal(fixture.timers.size, 1);
  assert.deepEqual(fixture.signals, ['SIGTERM']);
  fixture.fireNextTimer();
  assert.deepEqual(fixture.signals, ['SIGTERM', 'SIGKILL']);
  assert.equal(fixture.exits, 1);
});

test('configured shutdown duration is respected and fast close cancels force kill', () => {
  const fixture = shutdownFixture({ timeout: '240' });
  fixture.quit();
  assert.equal(fixture.timers.values().next().value.delay, 245000);
  fixture.child.emit('close', 0);
  assert.equal(fixture.timers.size, 0);
  assert.equal(fixture.exits, 1);
  assert.deepEqual(fixture.signals, ['SIGTERM']);
});

test('close listener is installed before sending SIGTERM', () => {
  const fixture = shutdownFixture({ closeOnSignal: true });
  fixture.quit();
  assert.equal(fixture.exits, 1);
  assert.equal(fixture.timers.size, 0);
  assert.deepEqual(fixture.signals, ['SIGTERM']);
});

test('already exited child does not incur a shutdown delay', () => {
  const fixture = shutdownFixture();
  fixture.child.exitCode = 0;
  fixture.quit();
  assert.equal(fixture.exits, 1);
  assert.equal(fixture.timers.size, 0);
  assert.deepEqual(fixture.signals, []);
});

test('Windows asks the Go helper to drain over its private stdin pipe', () => {
  const fixture = shutdownFixture({ platform: 'win32', timeout: '+15' });
  fixture.quit();
  assert.deepEqual(fixture.pipeMessages, ['shutdown\n']);
  assert.deepEqual(fixture.signals, []);
  assert.equal(fixture.timers.values().next().value.delay, 20000);
  fixture.fireNextTimer();
  assert.deepEqual(fixture.signals, ['SIGKILL']);
  assert.equal(fixture.exits, 1);
});

test('large valid timeouts are chunked below the Node timer overflow limit', () => {
  const fixture = shutdownFixture({ timeout: '2147484' });
  fixture.quit();
  assert.equal(fixture.timers.values().next().value.delay, 2147483647);
  fixture.fireNextTimer();
  assert.equal(fixture.exits, 0);
  assert.deepEqual(fixture.signals, ['SIGTERM']);
  assert.equal(fixture.timers.values().next().value.delay, 5353);
  fixture.fireNextTimer();
  assert.deepEqual(fixture.signals, ['SIGTERM', 'SIGKILL']);
});

test('invalid Go timeout values cannot cause immediate Node timer overflow', () => {
  for (const timeout of ['0', '-1', 'NaN', '1.5', ' 1 ', '1e3', '9223372037']) {
    const fixture = shutdownFixture({ timeout });
    fixture.quit();
    assert.equal(fixture.timers.values().next().value.delay, 125000, timeout);
  }
});
