const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { loadMasterKey } = require('../secret-store.cjs');

test('unavailable protected storage cannot write a plaintext fallback', () => {
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'canvas-secret-'));
  assert.throws(()=>loadMasterKey({dataDir:dir,safeStorage:{isEncryptionAvailable:()=>false}}),/protected_storage_unavailable/);
  assert.deepEqual(fs.readdirSync(dir),[]);
});

test('decrypt failure retains original encrypted bytes', () => {
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'canvas-secret-'));
  const encrypted=Buffer.from('damaged');
  fs.writeFileSync(path.join(dir,'master-key.protected'),encrypted);
  assert.throws(()=>loadMasterKey({dataDir:dir,safeStorage:{isEncryptionAvailable:()=>true,decryptString:()=>{throw new Error('failed')}}}),/protected_key_unavailable/);
  assert.deepEqual(fs.readFileSync(path.join(dir,'master-key.protected')),encrypted);
});

test('restarts reuse key and only protected ciphertext is stored', () => {
  const dir=fs.mkdtempSync(path.join(os.tmpdir(),'canvas-secret-'));
  // Reversible test provider makes plaintext-vs-ciphertext observable; production uses safeStorage.
  const safeStorage={isEncryptionAvailable:()=>true,encryptString:s=>Buffer.from([...s].reverse().join('')),decryptString:b=>[...b.toString()].reverse().join('')};
  const key=loadMasterKey({dataDir:dir,safeStorage});
  assert.deepEqual(loadMasterKey({dataDir:dir,safeStorage}),key);
  assert.equal(fs.readFileSync(path.join(dir,'master-key.protected')).includes(key.toString('base64')),false);
});
