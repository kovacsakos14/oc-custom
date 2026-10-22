// admin: password, then Authelia makes them register a TOTP app (code by
// e-mail) and they sign in with it. Then, on Authelia's settings page, they
// remove the app and register a new one, and sign in with the new one.
const { O, A, launch, signIn, inFiles, shot, text, sleep, click, registerTotp, elevate, freshTotp, run } = require('./lib');
const PASS = process.env.ADMIN_PASSWORD, EMAIL = process.env.ADMIN_EMAIL;

// A fresh browser: password, TOTP, OpenCloud's files
async function signInWithTotp(b, secret) {
  const p = await b.newPage();
  await p.goto(O + '/', { waitUntil: 'networkidle2' });
  await signIn(p, 'admin', PASS);
  await p.waitForSelector('#otp-input input', { timeout: 20000 });
  await p.click('#otp-input input');
  await p.keyboard.type(await freshTotp(secret));
  await inFiles(p, 'Personal');
  return p;
}

let p;
run(async () => {
  let b = await launch();
  p = await b.newPage();
  await p.setViewport({ width: 1280, height: 800 });
  await p.goto(O + '/', { waitUntil: 'networkidle2' });
  await signIn(p, 'admin', PASS);
  await p.waitForSelector('#register-link', { timeout: 20000 });
  await click(p, '#register-link');
  const first = await registerTotp(p, EMAIL);
  console.log('OK admin had to register a TOTP app at the first sign-in');
  await b.close();

  b = await launch();
  p = await signInWithTotp(b, first);
  console.log('OK admin signs in with password + TOTP');

  // Their own 2FA on Authelia's settings page (the page "Change password" opens)
  // (a new tab, as "Change password" opens it: leaving OpenCloud's page never settles)
  p = await b.newPage();
  await p.setViewport({ width: 1280, height: 800 });
  await p.goto(A + '/settings/two-factor-authentication', { waitUntil: 'networkidle2' });
  const since = Date.now();
  await click(p, '#one-time-password-delete');
  if (!await elevate(p, EMAIL, since)) throw new Error('removing the app asked for no e-mailed code');
  await click(p, '#dialog-delete');
  await p.waitForFunction(() => !document.querySelector('#one-time-password-delete'), { timeout: 15000 });
  console.log('OK admin removed their TOTP app on the settings page (after an e-mailed code)');
  const second = await registerTotp(p, EMAIL);
  if (second === first) throw new Error('the new TOTP app has the old secret');
  console.log('OK admin registered a new TOTP app there');
  await shot(p, 'admin-2fa-settings');
  await b.close();

  b = await launch();
  p = await signInWithTotp(b, second);
  console.log('OK admin signs in with the new TOTP app');
  await b.close();
}, async () => { if (p) { await shot(p, 'admin-2fa-fail'); console.log('page:', p.url(), await text(p)); } });
