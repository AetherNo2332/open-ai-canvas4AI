const { test } = require('node:test');
const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const { startHost } = require('../runtime.cjs');

test('failed startup reaps only its child, and does not disclose credentials', async () => {
  const masterKey = Buffer.alloc(32, 7);
  const sentinel = spawn(process.execPath, ['-e', 'setInterval(()=>{},1000)'], { windowsHide: true });
  let child, observedArgs;
  try {
    await assert.rejects(startHost({
      resourcesDir: 'C:/中文 空格/resources', dataDir: 'C:/中文 空格/data', masterKey,
      timeoutMs: 200, spawnProcess: (exe, args, opts) => {
        observedArgs = [exe, ...args];
        child = spawn(process.execPath, ['-e', 'process.stdin.resume();setInterval(()=>{},1000)'], {...opts,cwd:process.cwd()});
        return child;
      },
    }), /host_start_timeout/);
    assert.equal(observedArgs.join(' ').includes(masterKey.toString('base64')), false);
    assert.ok(child.exitCode !== null || child.signalCode !== null);
    assert.equal(sentinel.exitCode, null);
    assert.equal(sentinel.killed, false);
  } finally { sentinel.kill(); }
});

test('host error output cannot expose private input', async () => {
  const masterKey = Buffer.alloc(32, 8);
  let child;
  const err = await startHost({ resourcesDir:'.', dataDir:'.', masterKey,
    spawnProcess: (exe,args,opts) => {
      child = spawn(process.execPath, ['-e', `process.stdin.once('data',d=>{process.stderr.write(d);process.stdout.write(JSON.stringify({type:'error',code:'host_start_failed'})+String.fromCharCode(10));process.exit(1)})`], opts);
      return child;
    },
  }).then(()=>null,e=>e);
  assert.match(err.message, /host_start_failed/);
  assert.equal(err.message.includes(masterKey.toString('base64')), false);
  assert.ok(child.exitCode !== null || child.signalCode !== null);
});

test('remote readiness origin is refused before session exchange', async () => {
  let exchanged = false;
  await assert.rejects(startHost({resourcesDir:'.',dataDir:'.',masterKey:Buffer.alloc(32),
    spawnProcess:(exe,args,opts)=>spawn(process.execPath,['-e',`process.stdout.write(JSON.stringify({type:'ready',protocol:1,origin:'http://evil.test',instanceId:'x'})+String.fromCharCode(10));process.stdin.resume()`],opts),
    fetchImpl:()=>{exchanged=true;throw new Error('unexpected')},
  }), /invalid_host_ready/);
  assert.equal(exchanged,false);
});

test('spawn failure settles instead of waiting for an exit event that never occurs', async () => {
  await assert.rejects(startHost({resourcesDir:'C:/nonexistent-canvas-test',dataDir:'.',masterKey:Buffer.alloc(32)}),/host_spawn_failed/);
});
