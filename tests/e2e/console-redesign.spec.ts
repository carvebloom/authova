import { test, expect, type Page } from '@playwright/test'
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs'
import { createHmac, randomBytes } from 'node:crypto'

const bootstrap = JSON.parse(readFileSync('.dev/bootstrap.json', 'utf8'))
const base = 'http://127.0.0.1:18090/'
const output = 'output/console-redesign-browser'
mkdirSync(output, { recursive: true })
function totp(secret: string) {
  let bits = ''
  for (const char of secret)
    bits += 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(char).toString(2).padStart(5, '0')
  const key = Buffer.from(bits.match(/.{8}/g)!.map((byte) => parseInt(byte, 2)))
  const counter = Buffer.alloc(8)
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)))
  const hash = createHmac('sha1', key).update(counter).digest()
  return String((hash.readUInt32BE(hash[hash.length - 1]! & 15) & 0x7fffffff) % 1000000).padStart(
    6,
    '0',
  )
}
async function operatorLogin(
  page: Page,
  credentials: { email: string; password: string; totp_secret: string },
) {
  await new Promise((resolve) => setTimeout(resolve, 31000 - (Date.now() % 30000)))
  await page.goto(base)
  try {
    await page.getByLabel(/^邮箱/).fill(credentials.email)
    await page.getByLabel(/^密码/).fill(credentials.password)
    await page.getByLabel(/^动态口令/).fill(totp(credentials.totp_secret))
  } catch {
    throw new Error('Private credential input failed; diagnostics suppressed')
  }
  await page.getByRole('button', { name: '登录', exact: true }).click()
  await expect(page.getByRole('heading', { name: '应用', exact: true })).toBeVisible()
}
async function appPage(page: Page, appId: string, appName: string, section = '') {
  await page.goto(`${base}#/apps/${appId}${section ? '/' + section : ''}`)
  await expect(page.getByRole('heading', { name: appName, exact: true })).toBeVisible()
}

async function loadOperatorRow(page: Page, email: string) {
  // Wait for this route before checking pagination; the previous route also has a table.
  await expect(page.getByRole('heading', { name: '平台运营账号', exact: true })).toBeVisible()
  // The demo is persistent; newly created operators can be beyond page one.
  await expect(page.getByRole('table')).toBeVisible()
  const row = page.getByRole('row').filter({ hasText: email })
  for (let pageNumber = 0; pageNumber < 100 && await row.count() === 0; pageNumber++) {
    const more = page.getByRole('button', { name: '加载更多', exact: true })
    if (await more.count() === 0) break
    const rowCount = await page.getByRole('row').count()
    await more.click()
    await expect.poll(() => page.getByRole('row').count()).toBeGreaterThan(rowCount)
  }
  await expect(row).toBeVisible()
  return row
}

test('production Console workflows use real BFF/Core data, safe secrets and responsive navigation', async ({
  page,
}) => {
  test.setTimeout(240000)
  const faults: string[] = []
  page.on('pageerror', (error) => faults.push(error.name))
  await operatorLogin(page, bootstrap)
  await page.getByLabel('界面主题').selectOption('light')
  const suffix = Date.now()
  const appName = `Console 验收 / ${suffix}`
  await page.getByRole('button', { name: '创建应用', exact: true }).click()
  await page.getByRole('dialog').getByLabel('应用名称').fill(appName)
  await page.getByRole('dialog').getByLabel(/^Slug/).fill(`console-qa-${suffix}`)
  await page.getByRole('dialog').getByRole('button', { name: '创建', exact: true }).click()
  await expect(page.getByRole('heading', { name: appName, exact: true })).toBeVisible()
  const appId = new URL(page.url()).hash.match(/^#\/apps\/([^/]+)/)![1]!
  writeFileSync(`${output}/fixture.json`, JSON.stringify({ appId, appName }, null, 2) + '\n')
  await page.getByRole('link', { name: '全部应用', exact: true }).click()
  await page.screenshot({ path: `${output}/apps-light.png`, fullPage: true })
  await appPage(page, appId, appName)
  await expect(page.getByRole('heading', { name: '接入准备' })).toBeVisible()
  await expect(page.getByText('短信验证码', { exact: true })).toBeVisible()
  await page.screenshot({ path: `${output}/overview-light.png`, fullPage: true })

  await page.getByRole('link', { name: '配置', exact: true }).click()
  const name = page.getByLabel('应用名称')
  await name.fill(appName + ' · 已验证')
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(
    page.getByRole('heading', { name: appName + ' · 已验证', exact: true }),
  ).toBeVisible()
  await name.fill(appName)
  await page.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByRole('heading', { name: appName, exact: true })).toBeVisible()

  await page.getByRole('link', { name: '客户端', exact: true }).click()
  const system = page.getByRole('row').filter({ hasText: '系统' })
  await expect(system).toBeVisible()
  await expect(system.getByRole('button', { name: '编辑' })).toHaveCount(0)
  await page.getByRole('button', { name: '创建客户端', exact: true }).click()
  let dialog = page.getByRole('dialog')
  const clientName = `Browser client ${Date.now()}`
  await dialog.getByLabel('名称', { exact: true }).fill(clientName)
  await dialog.getByLabel('类型', { exact: true }).selectOption('confidential')
  await dialog.getByLabel('回调地址', { exact: false }).fill('http://127.0.0.1:18088/qa-callback')
  await dialog.getByLabel('允许的受众（allowed_audiences）').fill('urn:authova:local-qa-api')
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect(page.getByRole('heading', { name: '客户端密钥仅此一次显示' })).toBeVisible()
  const readable = await page.evaluate(() => ({
    cookie: document.cookie,
    local: Object.keys(localStorage),
    session: Object.keys(sessionStorage),
  }))
  expect(readable.cookie).not.toContain('av_console')
  expect(readable.local.every((key) => key === 'authova-theme')).toBe(true)
  expect(readable.session).toEqual([])
  await page.goBack({ waitUntil: 'commit', timeout: 1500 }).catch((error) => {
    if (error.name !== 'TimeoutError') throw error
  })
  await expect(page).toHaveURL(/\/clients$/)
  await expect(page.getByRole('heading', { name: '客户端密钥仅此一次显示' })).toBeVisible()
  await page.getByRole('button', { name: '我已保存，关闭' }).click()
  const client = page.getByRole('row').filter({ hasText: clientName })
  await expect(client).toBeVisible()
  await client.getByRole('button', { name: '编辑' }).click()
  dialog = page.getByRole('dialog')
  await dialog.getByLabel('状态', { exact: true }).selectOption('disabled')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(client).toContainText('已禁用')
  await page.screenshot({ path: `${output}/clients-light.png`, fullPage: true })

  await page.getByRole('link', { name: '连接', exact: true }).click()
  for (const provider of ['oidc', 'github', 'wechat', 'qq']) {
    const connectionName = `${provider} offline ${Date.now()}`
    await page.getByRole('button', { name: '创建连接', exact: true }).click()
    dialog = page.getByRole('dialog')
    await dialog.getByLabel('类型', { exact: true }).selectOption(provider)
    await dialog.getByLabel('名称', { exact: true }).fill(connectionName)
    if (provider === 'oidc')
      await dialog.getByLabel('Issuer', { exact: true }).fill('https://issuer.example.invalid')
    await dialog.getByLabel('Client ID', { exact: true }).fill('offline-fixture-appid')
    await dialog.getByLabel('Client Secret', { exact: true }).fill('offline-fixture-secret-value')
    await dialog.getByRole('button', { name: '创建', exact: true }).click()
    const row = page.getByRole('row').filter({ hasText: connectionName })
    await expect(row).toBeVisible()
    await row.getByRole('button', { name: '编辑', exact: true }).click()
    await dialog.getByLabel('状态', { exact: true }).selectOption('disabled')
    await dialog.getByLabel('操作原因').fill('Leave local protocol configuration fixture disabled')
    await dialog.getByRole('button', { name: '保存', exact: true }).click()
    await expect(row).toContainText('已禁用')
  }
  await page.screenshot({ path: `${output}/connections-light.png`, fullPage: true })
  await page.getByRole('link', { name: '审计日志', exact: true }).click()
  const audit = page
    .getByRole('row')
    .filter({ hasText: /connection\.update|连接.*更新/ })
    .first()
  await expect(audit).toBeVisible()
  await page.screenshot({ path: `${output}/audit-light.png`, fullPage: true })
  await page.getByRole('link', { name: '用户', exact: true }).click()
  await expect(page.getByPlaceholder('完整邮箱或 +8613800138000')).toBeVisible()

  await appPage(page, appId, appName)
  await page.getByRole('button', { name: '切换应用' }).click()
  await page.getByRole('button').filter({ hasText: 'Notes / App 1' }).click()
  await expect(page.getByRole('heading', { name: 'Notes / App 1', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: appName, exact: true })).toHaveCount(0)
  await page.getByLabel('界面主题').selectOption('dark')
  await page.screenshot({ path: `${output}/overview-dark.png`, fullPage: true })
  await page.setViewportSize({ width: 375, height: 812 })
  await expect(page.getByRole('button', { name: '打开导航' })).toBeVisible()
  await page.getByRole('button', { name: '打开导航' }).click()
  await expect(page.getByRole('dialog', { name: '导航' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog', { name: '导航' })).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: `${output}/mobile-dark.png`, fullPage: true })
  await page.emulateMedia({ reducedMotion: 'reduce' })
  const durations = await page
    .locator('.btn')
    .first()
    .evaluate((el) => getComputedStyle(el).transitionDuration)
  expect(durations.split(',').every((value) => parseFloat(value) <= 0.00001)).toBe(true)
  expect(faults).toEqual([])
})

test('platform owner provisions an auditor, read-only access is enforced, then disables the fixture', async ({
  page,
  browser,
}) => {
  test.setTimeout(180000)
  await operatorLogin(page, bootstrap)
  await page.getByRole('link', { name: '平台运营账号', exact: true }).click()
  const email = `console-auditor-${Date.now()}@example.invalid`
  const password = `Local acceptance ${randomBytes(20).toString('hex')}`
  await page.getByRole('button', { name: '创建运营账号', exact: true }).click()
  let dialog = page.getByRole('dialog')
  try {
    await dialog.getByLabel(/^邮箱/).fill(email)
    await dialog.getByLabel(/^初始密码/).fill(password)
  } catch {
    throw new Error('Private fixture credential input failed; diagnostics suppressed')
  }
  await dialog.getByLabel('角色').selectOption('auditor')
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'TOTP 配置密钥仅此一次显示' })).toBeVisible()
  const secret = await page.locator('.secret-value').textContent()
  await page.getByRole('button', { name: '我已安全传达，关闭' }).click()
  if (!secret || !/^[A-Z2-7]+$/.test(secret))
    throw new Error('TOTP provisioning failed; secret diagnostics suppressed')

  const context = await browser.newContext()
  try {
    const auditor = await context.newPage()
    await operatorLogin(auditor, { email, password, totp_secret: secret })
    await expect(auditor.getByRole('button', { name: '创建应用', exact: true })).toHaveCount(0)
    await auditor.getByRole('link', { name: '平台运营账号', exact: true }).click()
    await loadOperatorRow(auditor, email)
    await expect(auditor.getByRole('button', { name: '创建运营账号' })).toHaveCount(0)
    await expect(auditor.getByRole('button', { name: '变更', exact: true })).toHaveCount(0)
    // Exercise the real BFF with a legitimate auditor session and CSRF, not only hidden buttons.
    const status = await auditor.evaluate(async () => {
      const me = await (await fetch('/bff/me')).json()
      return (
        await fetch('/bff/apps', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'X-CSRF-Token': me.csrf_token,
            'Idempotency-Key': crypto.randomUUID(),
          },
          body: JSON.stringify({ name: 'Forbidden audit write', slug: 'forbidden-audit-write' }),
        })
      ).status
    })
    expect(status).toBe(403)
    await auditor.screenshot({ path: `${output}/operators-auditor.png`, fullPage: true })
  } finally {
    await context.close()
  }

  const row = await loadOperatorRow(page, email)
  await row.getByRole('button', { name: '变更' }).click()
  dialog = page.getByRole('dialog')
  await dialog.getByLabel('状态', { exact: true }).selectOption('disabled')
  await dialog.getByLabel('操作原因').fill('Disable completed local browser acceptance fixture')
  await dialog.getByRole('button', { name: '保存', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  // Saving reloads page one; read back the updated fixture through actual pagination.
  await page.reload()
  const updated = await loadOperatorRow(page, email)
  await expect(updated).toContainText('已禁用')
  await page.screenshot({ path: `${output}/operators-owner.png`, fullPage: true })
})
