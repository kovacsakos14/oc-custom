// Without Authelia: OpenCloud's own sign-in page; Profile has no "Edit";
// Preferences -> "Change password" opens OpenCloud's own dialog
const { O, launch, shot, text, sleep, click, inFiles, openProfile, run } = require('./lib');
const USER = process.env.USER_NAME, PASS = process.env.USER_PASSWORD;

let p;
run(async () => {
  const b = await launch();
  p = await b.newPage();
  await p.setViewport({ width: 1280, height: 800 });
  await p.goto(O + '/', { waitUntil: 'networkidle2' });
  await p.waitForSelector('#oc-login-username', { timeout: 60000 });
  await p.type('#oc-login-username', USER);
  await p.type('#oc-login-password', PASS);
  await p.click('button[type=submit]');
  await inFiles(p);
  console.log(`OK ${USER} signs in on OpenCloud's own page`);
  await openProfile(p);
  await sleep(1000);
  if (await p.$('[data-testid="account-page-edit-url-btn"]')) throw new Error('Profile shows an "Edit" button');
  await click(p, 'a[href*="account/preferences"]');
  const btn = await p.waitForSelector('[data-testid="account-page-edit-password-btn"]', { timeout: 20000 });
  if (await btn.evaluate(e => e.tagName) !== 'BUTTON') throw new Error('"Change password" is a link without Authelia');
  await btn.click();
  await p.waitForSelector('.oc-modal', { timeout: 10000 });
  console.log('OK "Change password" opens OpenCloud\'s own dialog:', (await p.$eval('.oc-modal', e => e.innerText)).split('\n')[0]);
  await shot(p, 'builtin-password-dialog');
  await b.close();
}, async () => { if (p) { await shot(p, 'builtin-fail'); console.log('page:', p.url(), await text(p)); } });
