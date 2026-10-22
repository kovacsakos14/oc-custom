// After users.sh reset-2fa admin: the admin's password is accepted, and
// Authelia asks them to register a 2FA app again
const { O, launch, signIn, shot, run } = require('./lib');
run(async () => {
  const b = await launch();
  const p = await b.newPage();
  await p.goto(O + '/', { waitUntil: 'networkidle2' });
  await signIn(p, 'admin', process.env.ADMIN_PASSWORD);
  await p.waitForSelector('#register-link', { timeout: 20000 });
  console.log('OK after reset-2fa the admin is asked to register a 2FA app again');
  await shot(p, 'admin-reset');
  await b.close();
});
