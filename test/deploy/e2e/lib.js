// Shared bits for the browser checks of the deploy/ setup (test/deploy/run.sh).
// They run in a container on the test network, where OC_DOMAIN is SWAG.
const puppeteer = require('puppeteer');
const crypto = require('crypto');

const O = exports.O = 'https://' + process.env.OC_DOMAIN;
exports.A = O + '/authelia';
const MAILPIT = 'http://' + process.env.MAILPIT + ':8025';

exports.launch = () => puppeteer.launch({
  executablePath: '/usr/bin/chromium-browser',
  acceptInsecureCerts: true,
  args: ['--no-sandbox', '--ignore-certificate-errors'],
});

exports.sleep = ms => new Promise(r => setTimeout(r, ms));
exports.shot = (page, name) => page.screenshot({ path: `/out/${name}.png` });
exports.text = async p => (await p.evaluate(() => document.body.innerText)).replace(/\n+/g, ' | ').slice(0, 400);

// Authelia's first-factor form
exports.signIn = async (p, user, pass) => {
  await p.waitForSelector('#username-textfield', { timeout: 20000 });
  await p.type('#username-textfield', user);
  await p.type('#password-textfield', pass);
  await p.click('#sign-in-button');
};

// Waits until OpenCloud's files view shows the given text (by default its app bar,
// files-app-bar: user types land on different pages, and the menu is collapsed in
// small windows)
exports.inFiles = (p, needle = 'files-app-bar') => p.waitForFunction(
  (o, n) => location.href.startsWith(o + '/files') && document.documentElement.innerHTML.includes(n),
  { timeout: 40000 }, O, needle);

// The newest one-time code Authelia e-mailed to an address (Mailpit's API),
// newer than `since` (ms)
exports.mailCode = async (to, since) => {
  for (let i = 0; i < 30; i++) {
    const list = await (await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent('to:' + to)}`)).json();
    const m = (list.messages || []).find(m => Date.parse(m.Created) >= since - 1000);
    if (m) {
      const body = await (await fetch(`${MAILPIT}/api/v1/message/${m.ID}`)).json();
      const code = (body.Text || '').match(/^\s*([A-Z0-9]{8})\s*$/m);
      if (code) return code[1];
      throw new Error('no code in the e-mail: ' + (body.Text || '').slice(0, 300));
    }
    await exports.sleep(500);
  }
  throw new Error('no e-mail to ' + to);
};

// RFC 6238 TOTP, 6 digits, 30 s, SHA1 (Authelia's defaults)
exports.totp = (base32, offset = 0) => {
  const alpha = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const c of base32.replace(/=+$/, '').toUpperCase()) bits += alpha.indexOf(c).toString(2).padStart(5, '0');
  const key = Buffer.from(bits.match(/.{8}/g).map(b => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 1000 / 30) + offset));
  const h = crypto.createHmac('sha1', key).update(counter).digest();
  const o = h[h.length - 1] & 0xf;
  return String((h.readUInt32BE(o) & 0x7fffffff) % 1e6).padStart(6, '0');
};

// A fresh code: one can't be used twice, so wait for the next 30 s window
exports.freshTotp = async secret => {
  await exports.sleep((31 - (Date.now() / 1000) % 30) * 1000);
  return exports.totp(secret);
};

// Clicks once the element is there and stays attached (Authelia's dialogs
// re-render between steps)
exports.click = async (p, sel, tries = 20) => {
  for (let i = 1; ; i++) {
    try {
      await p.waitForSelector(sel, { visible: true, timeout: 5000 });
      await exports.sleep(300);
      await p.click(sel);
      return;
    } catch (e) {
      if (i >= tries) throw new Error(`${sel}: ${e.message}`);
      await exports.sleep(500);
    }
  }
};

// Authelia asks for an e-mailed one-time code before changes to the
// account, unless the user confirmed one in the last few minutes
exports.elevate = async (p, email, since) => {
  // whichever comes first: the code field, or the dialog the click opened
  const asked = await p.waitForFunction(() => {
    const code = document.querySelector('#one-time-code');
    if (code && code.offsetParent) return 'code';
    return document.body.innerText.includes('Register One-Time Password') || !!document.querySelector('[role=dialog] #dialog-confirm, [role=dialog] #dialog-delete') ? 'dialog' : false;
  }, { timeout: 20000 }).then(h => h.jsonValue());
  if (asked !== 'code') return false;
  await p.type('#one-time-code', await exports.mailCode(email, since));
  await exports.click(p, '#dialog-verify');
  return true;
};

// Registers a TOTP app in Authelia's dialog (reached from the sign-in's
// "Register device" or the settings page); returns its secret. The one-time
// code that confirms the user's identity comes by e-mail.
exports.registerTotp = async (p, email) => {
  const since = Date.now();
  await exports.click(p, '#one-time-password-add');
  await exports.elevate(p, email, since);
  await p.waitForFunction(() => document.body.innerText.includes('Register One-Time Password'), { timeout: 15000 });
  await exports.click(p, '#dialog-next'); // Start -> Register (QR code)
  await p.waitForSelector('#secret-url', { timeout: 15000 });
  const secret = (await p.content()).match(/secret=([A-Z2-7=]+)/)[1];
  await exports.click(p, '#dialog-next'); // Register -> Confirm
  await p.waitForSelector('#otp-input input', { visible: true, timeout: 15000 });
  await exports.sleep(500);
  // the dialog submits by itself after the sixth digit. A code from the same
  // 30 s window as the user's last one is refused (replay), so on a refusal
  // try again with the next window's code.
  const closed = t => p.waitForFunction(() => !document.body.innerText.includes('Register One-Time Password'), { timeout: t })
    .then(() => true, () => false);
  for (let attempt = 1; ; attempt++) {
    await p.click('#otp-input input');
    await p.keyboard.type(attempt === 1 ? exports.totp(secret) : await exports.freshTotp(secret));
    if (await closed(attempt === 1 ? 8000 : 15000)) return secret;
    if (attempt === 2) throw new Error('Authelia refused the new TOTP app\'s code twice');
  }
};

// In-app navigation to OpenCloud's account pages (page.goto into the web app
// never settles: it keeps a notifications connection open)
exports.openProfile = async p => {
  await exports.click(p, '#_userMenuButton');
  await exports.click(p, '#oc-topbar-account-manage');
  await p.waitForSelector('#account-information', { timeout: 20000 });
};

// onFail: e.g. a screenshot of where it stopped
exports.run = (fn, onFail) => fn().catch(async e => {
  console.error('FAIL', e.message);
  if (onFail) await onFail().catch(() => {});
  process.exit(1);
});
