// The desktop app's flow: PKCE, loopback redirect on a random port, tokens,
// WebDAV and Graph with the access token, then a refresh. Twice, each in a
// fresh browser. Authelia asks for consent on every sign-in that grants a
// refresh token (offline_access), so both rounds show it.
process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
const crypto = require('crypto');
const http = require('http');
const { O, launch, signIn, sleep, text, run } = require('./lib');
const USER = process.env.USER_NAME, PASS = process.env.USER_PASSWORD;
const b64 = b => b.toString('base64url');

async function signInOnce(round) {
  const verifier = b64(crypto.randomBytes(32));
  const challenge = b64(crypto.createHash('sha256').update(verifier).digest());
  // Like the app: listen on a random loopback port for the redirect
  let code;
  const server = http.createServer((req, res) => {
    const q = new URL(req.url, 'http://127.0.0.1').searchParams;
    if (q.has('code') || q.has('error')) code = q.get('code') || 'ERR ' + req.url;
    res.end('signed in');
  });
  await new Promise(r => server.listen(0, '127.0.0.1', r));
  const redirect = `http://127.0.0.1:${server.address().port}`;
  const disc = await (await fetch(O + '/.well-known/openid-configuration')).json();
  if (round === 1) console.log('discovery via OpenCloud: issuer', disc.issuer, '| authorize', disc.authorization_endpoint);
  const url = disc.authorization_endpoint + '?' + new URLSearchParams({
    client_id: 'OpenCloudDesktop', response_type: 'code', redirect_uri: redirect,
    scope: 'openid profile email offline_access', state: b64(crypto.randomBytes(16)),
    code_challenge: challenge, code_challenge_method: 'S256',
  });
  const b = await launch();
  const p = await b.newPage();
  await p.goto(url, { waitUntil: 'networkidle2' }).catch(() => {});
  await signIn(p, USER, PASS);
  let consent = false;
  for (let i = 0; i < 40 && !code; i++) {
    if (await p.$('#openid-consent-accept').catch(() => null)) {
      consent = true;
      await p.click('#openid-consent-accept');
      break;
    }
    await sleep(500);
  }
  for (let i = 0; i < 40 && !code; i++) await sleep(500);
  if (!code) console.log('stuck at', p.url(), await text(p));
  await b.close();
  server.close();
  if (!code || code.startsWith('ERR')) throw new Error('no code: ' + code);
  console.log(`round ${round}: consent ${consent ? 'shown' : 'not shown'}, redirect to ${redirect} with a code`);
  const tok = async body => (await fetch(disc.token_endpoint, { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: new URLSearchParams(body) })).json();
  const t = await tok({ grant_type: 'authorization_code', code, redirect_uri: redirect, client_id: 'OpenCloudDesktop', code_verifier: verifier });
  if (!t.access_token) throw new Error('token: ' + JSON.stringify(t));
  const dav = async at => (await fetch(`${O}/remote.php/webdav/`, { method: 'PROPFIND', headers: { Authorization: 'Bearer ' + at, Depth: '1' } })).status;
  const me = await (await fetch(`${O}/graph/v1.0/me`, { headers: { Authorization: 'Bearer ' + t.access_token } })).json();
  console.log(`round ${round}: WebDAV ${await dav(t.access_token)}, graph /me ${me.onPremisesSamAccountName}`);
  const r = await tok({ grant_type: 'refresh_token', refresh_token: t.refresh_token, client_id: 'OpenCloudDesktop' });
  console.log(`round ${round}: refresh ${r.access_token ? 'ok' : JSON.stringify(r)}, WebDAV with the new token ${await dav(r.access_token)}`);
  // Reusing a spent refresh token looks like theft: Authelia refuses it and
  // revokes the whole chain, so the new access token stops working too
  const again = await tok({ grant_type: 'refresh_token', refresh_token: t.refresh_token, client_id: 'OpenCloudDesktop' });
  console.log(`round ${round}: old refresh token reused: ${again.error || 'accepted'}, then WebDAV with the new token ${await dav(r.access_token)}`);
  return consent;
}

run(async () => {
  const first = await signInOnce(1);
  const second = await signInOnce(2);
  console.log(`OK desktop flow twice (consent page: ${first ? 'shown' : 'not shown'}, then ${second ? 'shown' : 'not shown'})`);
});
