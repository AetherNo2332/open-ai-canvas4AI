import test from 'node:test';
import assert from 'node:assert/strict';
import { resolveReleaseMetadata, releaseNotes } from './release-metadata.mjs';

test('internal version produces formal release and legal image tags', () => {
  assert.deepEqual(resolveReleaseMetadata('v1.6.0(abcdef0)\n', 'v1.6.0'), {
    internalVersion: 'v1.6.0(abcdef0)', releaseVersion: 'v1.6.0', imageTag: '1.6.0',
  });
  for (const version of ['v1.6.0', 'v1.6.0.1(abcdef0)', 'v1.6.0-rc.2(abcdef0)']) {
    assert.match(resolveReleaseMetadata(version).imageTag, /^[\w][\w.-]{0,127}$/);
  }
});

test('rejects mismatched tags, invalid SHA and invalid image characters', () => {
  for (const version of ['v1.6.0(no-sha)', 'v1.6.0(abc)', `v1.6.0(${'a'.repeat(40)})`, 'v1.6.0+build', 'v1.6.0\ninjected', 'v1.6.0-']) {
    assert.throws(() => resolveReleaseMetadata(version), /Invalid/);
  }
  for (const tag of ['v1.6.1', 'v1.6.0(abcdef0)']) {
    assert.throws(() => resolveReleaseMetadata('v1.6.0(abcdef0)', tag), /does not match/);
  }
});

test('release notes collect exact current entry and intervening changes', () => {
  const log = '# Changes\n## v1.7.0(abcdef0)\nNew feature\n## v1.6.0(1234567)\nFix\n## v1.5.0(7654321)\nOld\n';
  const notes = releaseNotes(log, 'v1.7.0(abcdef0)', 'v1.5.0');
  assert.match(notes, /New feature/);
  assert.match(notes, /Fix/);
  assert.doesNotMatch(notes, /Old/);
  assert.equal(releaseNotes(log, 'v1.7.0(abcdef0)'), 'New feature\n');
  assert.throws(() => releaseNotes(log, 'v1.7.0(0000000)'), /exact/);
  assert.throws(() => releaseNotes(log, 'v1.7.0(abcdef0)', 'v1.4.0'), /Previous release/);
});
