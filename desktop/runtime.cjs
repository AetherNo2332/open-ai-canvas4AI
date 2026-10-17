'use strict';
const { spawn } = require('node:child_process');
const path = require('node:path');
const { randomBytes } = require('node:crypto');
const { createInterface } = require('node:readline');

async function startHost({ resourcesDir, dataDir, masterKey, timeoutMs = 60000, spawnProcess = spawn, fetchImpl = fetch }) {
  if (!Buffer.isBuffer(masterKey) || masterKey.length !== 32) throw new Error('invalid_master_key');
  const bootstrapToken = randomBytes(32).toString('base64url');
  const env = {};
  for (const name of ['SystemRoot', 'WINDIR', 'PATH', 'TEMP', 'TMP', 'APPDATA', 'LOCALAPPDATA', 'USERPROFILE']) {
    if (process.env[name]) env[name] = process.env[name];
  }
  const child = spawnProcess(path.join(resourcesDir, 'bin', process.platform === 'win32' ? 'canvas-host.exe' : 'canvas-host'), [],
    { windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'], env, cwd: resourcesDir });
  let settledExit = false;
  const exited = new Promise(resolve => child.once('close', (code, signal) => { settledExit = true; resolve({ code, signal }); }));
  let stopPromise;
  function stop() {
    if (!stopPromise) stopPromise = (async () => {
      if (settledExit) return;
      child.stdin.end();
      const timer = setTimeout(() => { if (!settledExit) child.kill(); }, 3000);
      await exited;
      clearTimeout(timer);
    })();
    return stopPromise;
  }
  // Never propagate unstructured host stdout/stderr into a public Error or UI.
  child.stderr.resume();
  child.stdin.on('error', () => {});
  const lines = createInterface({ input: child.stdout });
  try {
    const ready = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('host_start_timeout')), timeoutMs);
      function done(err, value) { clearTimeout(timer); err ? reject(err) : resolve(value); }
      child.once('error', () => done(new Error('host_spawn_failed')));
      child.once('exit', () => done(new Error('host_start_failed')));
      lines.on('line', line => {
        if (line.length > 65536) { done(new Error('invalid_host_ready')); return; }
        let message;
        try { message = JSON.parse(line); } catch { return; }
        if (message.type === 'error') { done(new Error('host_start_failed')); return; }
        if (message.type === 'ready') done(null, message);
      });
      child.stdin.write(JSON.stringify({ protocol: 1, dataDir, webDir: path.join(resourcesDir, 'web'),
        masterKey: masterKey.toString('base64'), bootstrapToken }) + '\n');
    });
    let url;
    try { url = new URL(ready.origin); } catch { throw new Error('invalid_host_ready'); }
    if (ready.protocol !== 1 || typeof ready.instanceId !== 'string' || !ready.instanceId ||
        url.protocol !== 'http:' || url.hostname !== '127.0.0.1' || !url.port || url.origin !== ready.origin) {
      throw new Error('invalid_host_ready');
    }
    const response = await fetchImpl(`${url.origin}/api/desktop/session`, { method: 'POST',
      headers: { Authorization: `Bearer ${bootstrapToken}` }, signal: AbortSignal.timeout(10000), redirect: 'error' });
    if (!response.ok) throw new Error('host_session_failed');
    const payload = await response.json();
    if (payload.code !== 0 || !payload.data?.user?.id) throw new Error('host_session_failed');
    const cookie = response.headers.get('set-cookie') || '';
    const match = /^open_ai_canvas_session=([A-Za-z0-9._-]+);/i.exec(cookie);
    if (!match || match[1].length > 512) throw new Error('host_session_failed');
    return { origin: url.origin, instanceId: ready.instanceId, sessionCookie: match[1], stop, exited };
  } catch (err) {
    // Reap only the exact child created for this invocation.
    if (!settledExit) { child.kill(); await exited; }
    throw new Error(['host_start_timeout','host_spawn_failed','host_start_failed','invalid_host_ready','host_session_failed'].includes(err.message) ? err.message : 'host_start_failed');
  } finally { lines.close(); }
}
module.exports = { startHost };
