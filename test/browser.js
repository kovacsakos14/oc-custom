// Signs in to OpenCloud as each given user in a fresh browser profile and
// records what they see: the sign-in page, where they land, the file view
// mode (table or tiles), the left menu, the app menu and what "Shared with
// me" offers. Screenshots and result.json go to /out.
// Run by test/browser in zenika/alpine-chrome:with-puppeteer.
//   USERS="admin:secret client1:secret"
//   NEW=1                     also list the "New" menu in Personal (and in ACTIONS' shares)
//   OPEN="client1:Client folder/readme.txt"
//                             also open the file in its default app: editable or not
//   PREFS=1                   also list the notification sections in Preferences
//   SEARCH="alice:dave"       also type "dave" into the share dialog of alice's
//                             first personal item and list who it offers
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
        // the menu's links (language-independent)
        nav: await page.$$eval('#web-nav-sidebar .oc-sidebar-nav-item a', (els) => els.map((e) => new URL(e.href).pathname)),
      }
      const r = result.users[user.name]
      // The app menu (top left)
      await page.locator('#_appSwitcherButton').click()
      await page.waitForSelector('#app-switcher-dropdown .applications-list', { visible: true, timeout: 10000 })
      r.apps = await page.$$eval('#app-switcher-dropdown .applications-list li', (els) => els.map((e) => e.innerText.trim()))
      await page.keyboard.press('Escape')
      await new Promise((res) => setTimeout(res, 500))
      // "Shared with me": the hide button and the Shares / Hidden Shares switch
      await page.locator('#web-nav-sidebar a[href*="/files/shares"]').click()
      await page.waitForFunction(() => location.pathname.startsWith('/files/shares/with-me'))
      await new Promise((res) => setTimeout(res, 3000))
      r.sharedWithMe = {
        items: await page.$$eval('[data-test-resource-name]', (els) => els.map((e) => e.getAttribute('data-test-resource-name'))),
        hideButtons: (await page.$$('.oc-files-actions-hide-share-trigger')).length,
        visibilityFilter: !!(await page.$('.share-visibility-filter')),
      }
      await page.screenshot({ path: `/out/${user.name}-shared-with-me.png` })
      const hasPersonal = r.nav.includes('/files/spaces/personal')
      if (process.env.NEW && hasPersonal) {
        await page.locator('#web-nav-sidebar a[href*="/files/spaces/personal"]').click()
        await page.waitForFunction(() => location.pathname.startsWith('/files/spaces/personal'))
        await new Promise((res) => setTimeout(res, 2000))
        // the "New" button: the one that opens the create list
        await page.locator('#web-nav-sidebar button, .files-app-bar button').filter((b) => /^(New|Neu)$/.test(b.innerText.trim())).click()
        await page.waitForSelector('#create-or-upload-drop', { visible: true, timeout: 10000 })
        r.newMenu = await page.$$eval('#create-or-upload-drop li', (els) => els.map((e) => e.innerText.trim()).filter(Boolean))
        await page.screenshot({ path: `/out/${user.name}-new.png` })
        await page.keyboard.press('Escape')
        await new Promise((res) => setTimeout(res, 500))
      }
      for (const spec of (process.env.SEARCH || '').split(' ').filter((s) => s.startsWith(user.name + ':'))) {
        const query = spec.slice(user.name.length + 1)
        if (!(await page.evaluate(() => location.pathname.startsWith('/files/spaces/personal')))) {
          await page.locator('#web-nav-sidebar a[href*="/files/spaces/personal"]').click()
          await page.waitForFunction(() => location.pathname.startsWith('/files/spaces/personal'))
          await new Promise((res) => setTimeout(res, 2000))
        }
        const row = await page.waitForSelector('tr:has([data-test-resource-name])', { timeout: 30000 })
        await row.click({ button: 'right' })
        await page.waitForSelector('#oc-files-context-menu', { timeout: 10000 })
        await page.locator('#oc-files-context-menu .oc-files-actions-show-shares-trigger').click()
        const input = await page.waitForSelector('#files-share-invite-input input, input#files-share-invite-input', { timeout: 30000 })
        await input.type(query, { delay: 50 })
        await new Promise((res) => setTimeout(res, 3000))
        await page.screenshot({ path: `/out/${user.name}-search.png` })
        r.search = r.search || {}
        r.search[query] = await page.$$eval('.vs__dropdown-option', (els) => els.map((e) => e.innerText.replace(/\s+/g, ' ').trim()))
      }
      // ACTIONS="client1:Client folder/readme.txt": open the share, right-click the file and
      // record the actions its context menu offers
      for (const spec of (process.env.ACTIONS || '').split(' ').filter((s) => s.startsWith(user.name + ':'))) {
        const [share, file] = spec.slice(user.name.length + 1).split('/')
        // in-app, as a person would: page.goto doesn't settle inside the app
        if (!(await page.evaluate(() => location.pathname.startsWith('/files/shares/with-me')))) {
          await page.locator('#web-nav-sidebar a[href*="/files/shares"]').click()
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
        await page.keyboard.press('Escape')
        await new Promise((r) => setTimeout(r, 500))
        // NEW=1: the "New" menu inside the share too
        if (process.env.NEW) {
          await page.locator('#web-nav-sidebar button, .files-app-bar button').filter((b) => /^(New|Neu)$/.test(b.innerText.trim())).click()
          await page.waitForSelector('#create-or-upload-drop', { visible: true, timeout: 10000 })
          r.newMenuInShare = r.newMenuInShare || {}
          r.newMenuInShare[share] = await page.$$eval('#create-or-upload-drop li', (els) =>
            els.map((e) => e.innerText.split('\n')[0].trim()).filter(Boolean))
          await page.keyboard.press('Escape')
          await new Promise((res) => setTimeout(res, 500))
        }
      }
      // OPEN="client1:Client folder/readme.txt": open the file from the share in its default
      // app and record whether it's editable (nothing is typed or saved)
      for (const spec of (process.env.OPEN || '').split(' ').filter((s) => s.startsWith(user.name + ':'))) {
        const [share, file] = spec.slice(user.name.length + 1).split('/')
        if (!(await page.evaluate(() => location.pathname.startsWith('/files/shares/with-me')))) {
          await page.locator('#web-nav-sidebar a[href*="/files/shares"]').click()
          await page.waitForFunction(() => location.pathname.startsWith('/files/shares/with-me'))
        }
        await page.locator(`[data-test-resource-name="${share}"]`).click()
        await page.locator(`[data-test-resource-name="${file}"]`).click()
        await page.waitForFunction(() => !location.pathname.startsWith('/files/'), { timeout: 30000 })
        await new Promise((res) => setTimeout(res, 5000))
        await page.screenshot({ path: `/out/${user.name}-open.png` })
        r.opened = r.opened || {}
        r.opened[`${share}/${file}`] = {
          app: await page.evaluate(() => location.pathname.split('/')[1]),
          editable: !!(await page.$('[contenteditable="true"]')),
          save: !!(await page.$('#app-save-action')),
        }
        await page.goBack()
        await page.waitForFunction(() => location.pathname.startsWith('/files/'), { timeout: 30000 })
        await new Promise((res) => setTimeout(res, 2000))
      }
      // PREFS=1: the notification sections on the Preferences page
      if (process.env.PREFS) {
        await page.locator('#_userMenuButton').click()
        await page.locator('#oc-topbar-account-manage').click()
        await page.locator('a[href*="/account/preferences"]').click()
        await page.waitForFunction(() => location.pathname.includes('/account/preferences'), { timeout: 20000 })
        await new Promise((res) => setTimeout(res, 4000))
        await page.screenshot({ path: `/out/${user.name}-preferences.png`, fullPage: true })
        r.preferences = {
          headings: await page.$$eval('main h2', (els) => els.map((e) => e.innerText.trim())),
          mailRow: !!(await page.$('[data-testid="notification-mails"]')),
        }
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
