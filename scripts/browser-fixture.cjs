// Browser choice is an execution-environment detail; fixture assertions stay the same.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

function launchFixtureBrowser() {
  const channel = process.env.BROWSER_CHANNEL || (process.platform === 'win32' ? 'msedge' : undefined);
  return chromium.launch({ ...(channel ? { channel } : {}), headless: true });
}

module.exports = { launchFixtureBrowser };
