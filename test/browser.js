// Signs in to OpenCloud as each given user in a fresh browser profile and
// records what they see: the sign-in page, where they land, and the file view
// mode (table or tiles). Screenshots and result.json go to /out.
// Run by test/browser in zenika/alpine-chrome:with-puppeteer.
//   USERS="admin:secret dave:secret"
const fs = require('fs')
const puppeteer = require('puppeteer')

const BASE = process.env.BASE || 'https://localhost:9200'
const users = (process.env.USERS || '').split(' ').filter(Boolean).map((u) => {
  const i = u.indexOf(':')
  return { name: u.slice(0, i), password: u.slice(i + 1) }
})

async function main() {
  const result = { signin: {}, users: {} }
  for (const user of users) {
    // A fresh profile per user: no stored view mode, no session
    const browser = await puppeteer.launch({
      executablePath: process.env.PUPPETEER_EXECUTABLE_PATH || '/usr/bin/chromium-browser',
      args: ['--no-sandbox', '--disable-dev-shm-usage', '--ignore-certificate-errors'],
    })
    try {
      const page = await browser.newPage()
      await page.setViewport({ width: 1280, height: 800 })
      await page.goto(BASE, { waitUntil: 'networkidle2' })
      await page.waitForSelector('#oc-login-username', { timeout: 60000 })
      // The sign-in page once, for the branding
      if (!result.signin.title) {
        await page.screenshot({ path: '/out/signin.png' })
        result.signin = {
          title: await page.title(),
          logos: await page.$$eval('img', (imgs) => imgs.map((i) => i.getAttribute('src'))),
        }
      }
      await page.type('#oc-login-username', user.name)
      await page.type('#oc-login-password', user.password)
      await Promise.all([page.waitForNavigation({ waitUntil: 'networkidle2' }), page.click('button[type=submit]')])
      // The web UI redirects once more after the token exchange
      await page.waitForFunction(() => /\/files\//.test(location.pathname), { timeout: 60000 })
      await new Promise((r) => setTimeout(r, 4000))
      await page.screenshot({ path: `/out/${user.name}.png` })
      result.users[user.name] = {
        landed: await page.evaluate(() => location.pathname),
        title: await page.title(),
        table: !!(await page.$('table.files-table, .oc-table.files-table')),
        tiles: !!(await page.$('.oc-tiles, #tiles-view')),
        favicon: await page.$eval('link[rel~=icon]', (l) => l.getAttribute('href')).catch(() => null),
        logos: await page.$$eval('img', (imgs) => imgs.map((i) => i.getAttribute('src'))),
      }
    } finally {
      await browser.close()
    }
  }
  fs.writeFileSync('/out/result.json', JSON.stringify(result, null, 2))
  console.log(JSON.stringify(result, null, 2))
}

main().catch((e) => {
  console.error(e)
  process.exit(1)
})
