// A disabled user (users.sh disable) can't sign in
const { O, launch, signIn, sleep, text, run } = require('./lib');
run(async () => {
  const b = await launch();
  const p = await b.newPage();
  await p.goto(O + '/', { waitUntil: 'networkidle2' });
  await signIn(p, process.env.USER_NAME, process.env.USER_PASSWORD);
  await sleep(4000);
  if (p.url().startsWith(O + '/files')) throw new Error('the disabled user got in');
  if (!p.url().startsWith(O + '/authelia')) throw new Error('unexpected page ' + p.url());
  console.log(`OK disabled ${process.env.USER_NAME} stays on the sign-in page:`, (await text(p)).slice(0, 80));
  await b.close();
});
