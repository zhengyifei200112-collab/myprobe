// Browser choice is an execution-environment detail; fixture assertions stay the same.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

function launchFixtureBrowser() {
  const channel = process.env.BROWSER_CHANNEL || (process.platform === 'win32' ? 'msedge' : undefined);
  // Chromium renderers use TMPDIR for shared memory when /dev/shm is disabled.
  // A fixture's 0700 log/database parent is unsuitable for that renderer access.
  // Keep Playwright profiles/screenshots and Go fixtures private, but let the
  // browser use its normal Linux temporary area for shared-memory handles.
  const env = process.platform === 'linux' ? { ...process.env, TMPDIR: '/tmp', TMP: '/tmp', TEMP: '/tmp' } : process.env;
  return chromium.launch({ ...(channel ? { channel } : {}), headless: true, env });
}

module.exports = { launchFixtureBrowser };
