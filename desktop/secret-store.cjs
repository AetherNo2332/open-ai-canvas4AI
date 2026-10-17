'use strict';
const fs = require('node:fs');
const path = require('node:path');
const { randomBytes } = require('node:crypto');

function loadMasterKey({ dataDir, safeStorage }) {
  if (!safeStorage.isEncryptionAvailable() ||
      (process.platform === 'linux' && safeStorage.getSelectedStorageBackend?.() === 'basic_text')) {
    throw new Error('protected_storage_unavailable');
  }
  fs.mkdirSync(dataDir, { recursive: true, mode: 0o700 });
  const file = path.join(dataDir, 'master-key.protected');
  function readExisting() {
    try {
      const plaintext = safeStorage.decryptString(fs.readFileSync(file));
      const key = Buffer.from(plaintext, 'base64');
      if (key.length !== 32 || key.toString('base64') !== plaintext) throw new Error('invalid');
      return key;
    } catch { throw new Error('protected_key_unavailable'); }
  }
  if (fs.existsSync(file)) return readExisting();
  const key = randomBytes(32);
  let encrypted;
  try { encrypted = safeStorage.encryptString(key.toString('base64')); }
  catch { key.fill(0); throw new Error('protected_storage_unavailable'); }
  const temporary = path.join(dataDir, `master-key.${randomBytes(8).toString('hex')}.tmp`);
  try {
    fs.writeFileSync(temporary, encrypted, { flag: 'wx', mode: 0o600 });
    // Hard-link publication is atomic and cannot replace another first starter.
    fs.linkSync(temporary, file);
  } catch (err) {
    key.fill(0);
    if (err.code === 'EEXIST') return readExisting();
    throw new Error('protected_key_write_failed');
  } finally { if (fs.existsSync(temporary)) fs.unlinkSync(temporary); }
  return key;
}
module.exports = { loadMasterKey };
