// Signs in to OpenCloud as each given user in a fresh browser profile and
// records what they see: the sign-in page, where they land, and the file view
// mode (table or tiles). Screenshots and result.json go to /out.
// Run by test/browser in zenika/alpine-chrome:with-puppeteer.
//   USERS="admin:secret client1:secret"
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
      // ACTIONS="client1:Client folder/readme.txt": open the share, right-click the file and
      // record the actions its context menu offers
      for (const spec of (process.env.ACTIONS || '').split(' ').filter((s) => s.startsWith(user.name + ':'))) {
        const [share, file] = spec.slice(user.name.length + 1).split('/')
        // in-app, as a person would: page.goto doesn't settle inside the app
        if (!(await page.evaluate(() => location.pathname.startsWith('/files/shares/with-me')))) {
          await page.locator('a[href*="/files/shares/with-me"]').click()
          await page.waitForFunction(() => location.pathname.startsWith('/files/shares/with-me'))
        }
        await page.locator(`[data-test-resource-name="${share}"]`).click()
        await page.waitForSelector(`[data-test-resource-name="${file}"]`, { timeout: 30000 })
        const row = await page.$(`tr:has([data-test-resource-name="${file}"])`)
        await row.click({ button: 'right' })
        await page.waitForSelector('#oc-files-context-menu', { timeout: 10000 })
        await new Promise((r) => setTimeout(r, 1000))
        await page.screenshot({ path: `/out/${user.name}-actions.png` })
        result.users[user.name].actions = result.users[user.name].actions || {}
        result.users[user.name].actions[`${share}/${file}`] = await page.$$eval(
          '#oc-files-context-menu [class*="oc-files-actions-"][class*="-trigger"]',
          (els) => els.map((e) => [...e.classList].find((c) => c.endsWith('-trigger')).replace(/^oc-files-actions-|-trigger$/g, ''))
        )
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
