import { appendFileSync, readFileSync, writeFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export function resolveReleaseMetadata(raw, tag) {
  const internalVersion = raw.trim();
  const match = /^(v\d+\.\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?)(?:\(([0-9a-f]{7})\))?$/.exec(internalVersion);
  if (!match) throw new Error(`Invalid internal version: ${internalVersion}`);
  const releaseVersion = match[1];
  const imageTag = releaseVersion.slice(1);
  if (!/^[\w][\w.-]{0,127}$/.test(imageTag)) throw new Error('Invalid version image tag');
  if (tag && tag !== releaseVersion) throw new Error(`Release ${releaseVersion} does not match tag ${tag}`);
  return { internalVersion, releaseVersion, imageTag };
}

export function previousFormalRelease(releases, currentTag) {
  return releases.find((release) => release.tag_name !== currentTag &&
    !release.draft && !release.prerelease &&
    /^v\d+\.\d+\.\d+$/.test(release.tag_name))?.tag_name;
}

export function releaseNotes(changelog, internalVersion, previousInternalVersion) {
  const lines = changelog.replace(/\r/g, '').split('\n');
  const start = lines.indexOf(`## ${internalVersion}`);
  if (start < 0) throw new Error(`Missing exact CHANGELOG heading: ## ${internalVersion}`);
  // A fork's first release only advertises its explicitly curated current entry.
  let end = lines.findIndex((line, index) => index > start && line.startsWith('## '));
  if (end < 0) end = lines.length;
  if (previousInternalVersion) {
    end = lines.indexOf(`## ${previousInternalVersion}`, start + 1);
    if (end < 0) throw new Error(`Previous release ${previousInternalVersion} missing from CHANGELOG`);
  }
  const notes = lines.slice(start + 1, end).join('\n').trim();
  if (!notes) throw new Error('Release notes are empty');
  return notes + '\n';
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const args = process.argv.slice(2);
    const value = (flag) => args.includes(flag) ? args[args.indexOf(flag) + 1] : undefined;
    if (value('--releases-file')) {
      process.stdout.write((previousFormalRelease(JSON.parse(readFileSync(value('--releases-file'), 'utf8')), value('--tag')) || '') + '\n');
      process.exit(0);
    }
    const metadata = resolveReleaseMetadata(readFileSync('VERSION', 'utf8'), value('--tag'));
    const changelog = readFileSync('CHANGELOG.md', 'utf8');
    if (value('--tag') || value('--notes-file')) {
      const previousRelease = value('--previous-release');
      const previousInternalVersion = value('--previous-internal-version');
      if (Boolean(previousRelease) !== Boolean(previousInternalVersion)) throw new Error('Previous release tag and internal version are both required');
      if (previousRelease) resolveReleaseMetadata(previousInternalVersion, previousRelease);
      const notes = releaseNotes(changelog, metadata.internalVersion, previousInternalVersion);
      if (value('--notes-file')) writeFileSync(value('--notes-file'), notes);
    }
    if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT,
      `version=${metadata.internalVersion}\nrelease_version=${metadata.releaseVersion}\nimage_tag=${metadata.imageTag}\n`);
    process.stdout.write(JSON.stringify(metadata) + '\n');
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
