// README screenshots and a browser smoke test, against a demo instance (DEMO_DATA=true seeds a few lived-in boards):
//
//   DATA_DIR=./demo DEMO_DATA=true ADMIN_EMAIL=demo@example.com ADMIN_PASSWORD=demodemo1 ADMIN_NAME=Maya PORT=18306 ./pocket-boards
//   BASE=http://127.0.0.1:18306 node scripts/screenshots.mjs           # CHROMIUM=/path/to/chromium to pick a browser
//   CDP=http://127.0.0.1:19271 … node scripts/screenshots.mjs           # or any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                              # checks only
// playwright-core comes from web/node_modules, or set PW_FROM to a package.json next to an install of it.
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(process.env.PW_FROM || new URL('../web/package.json', import.meta.url))
let pw
try { pw = require('playwright') } catch { pw = require('playwright-core') }
const { chromium } = pw
const base = process.env.BASE || 'http://127.0.0.1:18306'
const shots = process.env.SHOTS !== '0'
const dir = new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP)
  : await chromium.launch(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM, args: ['--no-sandbox'] } : {})
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light', locale: 'en-GB',
    userAgent: phone ? 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1' : undefined,
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/Failed to load resource|pocket-ask/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'demo@example.com', password: 'demodemo1' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
async function go(page, path, wait = 1200) {
  await page.goto(base + path, { waitUntil: 'networkidle' })
  await page.addStyleTag({ content: '*, *::before, *::after { transition: none !important; animation: none !important; }' })
  await page.evaluate(() => document.fonts.ready)
  await page.waitForTimeout(wait)
}
async function shot(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }
const has = async (page, text) => (await page.content()).includes(text)

// ---- desktop ----
{
  const { ctx, page } = await session()
  await go(page, '/b/1')
  assert.ok(await has(page, 'Home page hero'), 'the board shows its cards')
  await shot(page, 'desktop-board')
  const card = await page.evaluate(() => fetch('/api/v1/cards?assigned=0&q=About').then(r => r.json()))
  await go(page, `/b/${card[0].board_id}/c/${card[0].id}`)
  assert.ok(await has(page, 'First draft'), 'the card shows its checklist')
  await shot(page, 'desktop-card')
  await go(page, '/')
  assert.ok(await has(page, 'Website relaunch'), 'home lists the boards')
  await shot(page, 'desktop-home')
  await go(page, '/my')
  await shot(page, 'desktop-my-cards')
  await go(page, '/')
  const newBtn = page.getByRole('button', { name: /new board/i }).first()
  await newBtn.click()
  await page.waitForTimeout(400)
  const tpl = page.getByText(/from a template/i).first()
  if (await tpl.count()) { await tpl.click(); await page.waitForTimeout(600) }
  await shot(page, 'desktop-new-board')
  await ctx.close()
}

// ---- phone ----
{
  const { ctx, page } = await session({ phone: true })
  await go(page, '/b/2')
  assert.ok(await has(page, 'Fix the dripping tap'), 'the shared board on a phone')
  await shot(page, 'phone-board')
  const card = await page.evaluate(() => fetch('/api/v1/cards?assigned=0&q=dripping').then(r => r.json()))
  await go(page, `/b/${card[0].board_id}/c/${card[0].id}`)
  await shot(page, 'phone-card')
  await ctx.close()
}

await browser.close()
if (errors.length) { console.error(errors.join('\n')); process.exit(1) }
console.log('screenshots ok')
