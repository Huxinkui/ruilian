import { chromium, expect } from '@playwright/test'
import assert from 'node:assert/strict'
import path from 'node:path'

const base = process.env.RUILIAN_TEST_URL
const licenseFile = process.env.RUILIAN_TEST_LICENSE
assert.ok(base && licenseFile, 'Set RUILIAN_TEST_URL and RUILIAN_TEST_LICENSE')
const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
try {
  const context = await browser.newContext({ viewport: { width: 1440, height: 1050 }, locale: 'zh-CN' })
  await context.route('**/*', route => route.request().url().startsWith(base) ? route.continue() : route.abort())
  const errors = []
  const login = await context.request.post(base + '/api/auth/login', { data: { username: 'admin', password: 'Browser-Test-2026' } })
  assert.equal((await login.json()).ok, true)
  const page = await context.newPage()
  page.on('pageerror', e => errors.push(e.message))
  await page.goto(base + '/#/system?tab=license')
  await page.waitForLoadState('networkidle')
  await expect(page.locator('.license-card')).toContainText('试用中')
  await page.locator('input[type="file"]').setInputFiles(licenseFile)
  await expect(page.getByRole('button', { name: '激活永久授权', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: '激活永久授权', exact: true }).click()
  await expect(page.locator('.license-card')).toContainText('已授权')
  await expect(page.locator('.license-card')).toContainText('离线永久授权')
  await expect(page.locator('.el-loading-mask:visible')).toHaveCount(0)
  const response = await context.request.get(base + '/api/license')
  const status = (await response.json()).data
  assert.equal(status.status, 'active')
  assert.equal(status.license.license_type, 'PERPETUAL')
  assert.equal(status.license.customer_id, 'RELEASE-ACCEPTANCE')
  assert.deepEqual(errors, [])
  await page.screenshot({ path: path.resolve('../artifacts/screenshots/09-perpetual-license.png'), fullPage: true, animations: 'disabled' })
  process.stdout.write('OFFLINE_LICENSE_FILE_IMPORT_PASSED\n')
} finally { await browser.close() }
