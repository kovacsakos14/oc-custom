// Signs USER_NAME in with USER_PASSWORD through Authelia and checks what
// comes next: WANT=register, Authelia asks them to register a 2FA app (admins,
// the 2fa group, after a reset); WANT=files, they land in OpenCloud straight
// away (password only)
const { O, launch, signIn, inFiles, shot, run } = require('./lib');
const { USER_NAME, USER_PASSWORD, WANT } = process.env;
run(async () => {
  const b = await launch();
  const p = await b.newPage();
  await p.goto(O + '/', { waitUntil: 'networkidle2' });
  await signIn(p, USER_NAME, USER_PASSWORD);
  if (WANT === 'register') {
    await p.waitForSelector('#register-link', { timeout: 20000 });
    console.log(`OK ${USER_NAME} is asked to register a 2FA app`);
  } else {
    await inFiles(p);
    console.log(`OK ${USER_NAME} is in OpenCloud with the password only`);
  }
  await shot(p, `twofa-${USER_NAME}-${WANT}`);
  await b.close();
});
