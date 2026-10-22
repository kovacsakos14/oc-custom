// A user (no 2FA): password only; OpenCloud's Profile has no "Edit";
// Preferences -> "Change password" opens Authelia's page in a new tab, where
// they change their password (after an e-mailed code); then they sign in with
// the new one, and the old one no longer works. "Log out" ends Authelia's
// session too: signing in again asks for the password (another user can).
const { O, A, launch, signIn, inFiles, shot, text, sleep, click, elevate, openProfile, run } = require('./lib');
const USER = process.env.USER_NAME, PASS = process.env.USER_PASSWORD, NEW = process.env.USER_NEW_PASSWORD, EMAIL = process.env.USER_EMAIL;

let p;
run(async () => {
  let b = await launch();
  p = await b.newPage();
  await p.setViewport({ width: 1280, height: 800 });
  await p.goto(O + '/', { waitUntil: 'networkidle2' });
  await signIn(p, USER, PASS);
  await inFiles(p, 'Shared with me');
  console.log(`OK ${USER} signs in with a password only`);

  await openProfile(p);
  await sleep(1000);
  if (await p.$('[data-testid="account-page-edit-url-btn"]')) throw new Error('Profile shows an "Edit" button');
  console.log('OK Profile has no "Edit" button');
  await click(p, 'a[href*="account/preferences"]');
  const btn = await p.waitForSelector('[data-testid="account-page-edit-password-btn"]', { timeout: 20000 });
  const link = await btn.evaluate(e => ({ tag: e.tagName, href: e.getAttribute('href'), target: e.getAttribute('target') }));
  if (link.tag !== 'A' || link.href !== A + '/settings/security' || link.target !== '_blank') throw new Error('Change password: ' + JSON.stringify(link));
  const tab = new Promise(r => b.once('targetcreated', t => r(t.page())));
  await btn.click();
  const s = await tab;
  await s.setViewport({ width: 1280, height: 800 });
  await s.waitForFunction(() => document.body.innerText.includes('Password'), { timeout: 20000 });
  console.log('OK "Change password" opened', s.url());
  const since = Date.now();
  await click(s, '#change-password-button');
  if (!await elevate(s, EMAIL, since)) throw new Error('changing the password asked for no e-mailed code');
  await s.waitForSelector('#old-password', { visible: true, timeout: 15000 });
  await sleep(500);
  await s.type('#old-password', PASS);
  await s.type('#new-password', NEW);
  await s.type('#repeat-new-password', NEW);
  await click(s, '#password-change-dialog-submit');
  await s.waitForSelector('#change-password-form', { hidden: true, timeout: 15000 });
  await shot(s, 'user-password-changed');
  console.log('OK the password was changed on Authelia\'s page (after an e-mailed code)');
  await b.close();

  // A fresh browser: the old password is refused, the new one works
  b = await launch();
  p = await b.newPage();
  await p.goto(O + '/', { waitUntil: 'networkidle2' });
  await signIn(p, USER, PASS);
  await sleep(3000);
  if (p.url().startsWith(O + '/files')) throw new Error('the old password still works');
  await p.reload({ waitUntil: 'networkidle2' });
  await signIn(p, USER, NEW);
  await inFiles(p, 'Shared with me');
  console.log('OK the old password is refused, the new one signs in');

  // Log out (patch 12 + WEB_OPTION_LOGOUT_URL): Authelia's session ends as well
  await click(p, '#_userMenuButton');
  await click(p, '#oc-topbar-account-logout');
  await p.waitForSelector('#username-textfield', { timeout: 30000 });
  await p.goto(O + '/', { waitUntil: 'networkidle2' });
  await sleep(3000);
  if (!await p.$('#username-textfield')) throw new Error('after "Log out" the next visit signed in without a password: ' + p.url());
  await shot(p, 'user-logged-out');
  console.log('OK "Log out" ends Authelia\'s session too: the next visit asks for the password');
  await b.close();
}, async () => { if (p) { await shot(p, 'user-fail'); console.log('page:', p.url(), await text(p)); } });
