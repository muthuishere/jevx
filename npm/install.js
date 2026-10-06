#!/usr/bin/env node
// Downloads the jevx binary for this platform from the GitHub release that matches this package's version (so every
// install is counted by GitHub Releases), then runs `jevx install` to put the agent skill in place and register the
// hook entries only for enabled plugins (none by default). JEVX_NO_HOOK=1: skills only. JEVX_SKIP_SETUP=1: binary only.
'use strict';
const fs = require('fs');
const path = require('path');
const https = require('https');
const { spawnSync } = require('child_process');

const REPO = 'muthuishere/jevx';
const version = 'v' + require('./package.json').version;
const os = { darwin: 'darwin', linux: 'linux', win32: 'windows' }[process.platform];
const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
if (!os || !arch) {
  console.error(`jevx: no prebuilt binary for ${process.platform}/${process.arch}; build from source: go install github.com/${REPO}/cmd/jevx@latest`);
  process.exit(0); // never fail the npm install
}
const ext = os === 'windows' ? '.exe' : '';
const asset = `jevx_${os}_${arch}${ext}`;
const url = `https://github.com/${REPO}/releases/download/${version}/${asset}`;
const dest = path.join(__dirname, 'bin', 'jevx-bin' + ext);

function get(u, redirects = 0) {
  return new Promise((resolve, reject) => {
    https.get(u, { headers: { 'User-Agent': 'jevx-npm' } }, (res) => {
      if ([301, 302, 303, 307, 308].includes(res.statusCode) && res.headers.location && redirects < 5) {
        res.resume();
        return resolve(get(res.headers.location, redirects + 1));
      }
      if (res.statusCode !== 200) {
        res.resume();
        return reject(new Error(`HTTP ${res.statusCode} for ${u}`));
      }
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => resolve(Buffer.concat(chunks)));
    }).on('error', reject);
  });
}

(async () => {
  try {
    console.log(`jevx: downloading ${asset} (${version})`);
    const buf = await get(url);
    fs.mkdirSync(path.dirname(dest), { recursive: true });
    fs.writeFileSync(dest, buf, { mode: 0o755 });
  } catch (e) {
    console.error(`jevx: download failed: ${e.message}\n  install manually: curl -fsSL https://muthuishere.github.io/jevx/install.sh | sh`);
    process.exit(0);
  }
  if (process.env.JEVX_SKIP_SETUP) return;
  const args = ['install'].concat(process.env.JEVX_NO_HOOK ? ['--skills'] : []);
  const r = spawnSync(dest, args, { stdio: 'inherit' });
  if (r.status !== 0) console.error('jevx: setup did not finish; run `jevx install` yourself');
})();
