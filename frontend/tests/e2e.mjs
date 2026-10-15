import { chromium, _electron as electron, expect } from '@playwright/test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'

let base = process.env.RUILIAN_TEST_URL || 'http://127.0.0.1:19088'
const output = path.resolve('../artifacts/screenshots')
const defaultBrand = JSON.parse(await fs.readFile(path.resolve('../branding.json'), 'utf8'))
const testBrand = { app_name:'本地经营管理 · 验收', brand_short:'经营管理' }
await fs.mkdir(output, { recursive: true })
let browser, electronApp, context, page
const desktopData = process.env.RUILIAN_TEST_DATA || path.resolve('../artifacts/client-ui-' + Date.now())
const nativeDownloads = path.join(desktopData, 'ui-downloads')
if (process.env.RUILIAN_ELECTRON_PATH) {
  await fs.mkdir(nativeDownloads, { recursive:true })
  electronApp = await electron.launch({ executablePath: process.env.RUILIAN_ELECTRON_PATH, args: ['--data-dir', desktopData], env: { ...process.env, RUILIAN_TEST_HIDDEN: '1' }, timeout: 60000 })
  page = await electronApp.firstWindow()
  await page.waitForURL(url => url.hostname === '127.0.0.1', { timeout: 35000 })
  base = new URL(page.url()).origin
  context = electronApp.context()
  await electronApp.evaluate(({ session }, directory) => {
    globalThis.__acceptanceDownloads = []
    session.defaultSession.on('will-download', (_event, item) => {
      const record = { path:directory + '/' + item.getFilename().replace(/[\\/]/g, '_'), state:'started' }
      globalThis.__acceptanceDownloads.push(record)
      item.setSavePath(record.path)
      item.once('done', (_event, state) => { record.state = state; record.bytes = item.getReceivedBytes(); record.total = item.getTotalBytes() })
    })
  }, nativeDownloads)
} else {
  browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: 'zh-CN', acceptDownloads: true })
  page = await context.newPage()
}
await context.route('**/*', route => { const url = route.request().url(); if (url.startsWith(base) || url.startsWith('blob:') || url.startsWith('data:')) route.continue(); else route.abort() })
const errors = [], checkpoints = []
page.on('pageerror', e => errors.push(e.stack || e.message))
page.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
async function api(url, method = 'GET', data) {
  const response = await context.request.fetch(base + '/api' + url, { method, data, headers: { 'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID() } })
  const json = await response.json(); assert.equal(json.ok, true, method + ' ' + url + ': ' + JSON.stringify(json)); return json.data
}
const check = (name, data = {}) => { checkpoints.push({ name, ...data }); process.stdout.write(JSON.stringify({ checkpoint: name, ...data }) + '\n') }
let navigation = 0
async function goto(hash) {
  // Force a document navigation so networkidle observes the new page's requests,
  // even when moving between two query variants of the same Vue route.
  await page.goto(base + '/?acceptance=' + (++navigation) + '#' + hash)
  await page.waitForLoadState('networkidle')
  await page.locator('.page-head h1').waitFor({ state: 'visible' })
  await expect(page.locator('.el-loading-mask:visible')).toHaveCount(0, { timeout: 10000 })
  await expect(page.locator('.main-shell > .trial-banner')).toContainText('试用期：剩余 10 天')
  await expect(page.locator('.main-shell > .trial-banner').getByRole('button', { name: '立即授权' })).toBeVisible()
}
function drawer() { return page.locator('.el-drawer:visible') }
function field(container, label) { return container.locator('.el-form-item').filter({ has: page.locator('.el-form-item__label').filter({ hasText: label }) }).first() }
async function select(container, label, value) { await field(container, label).locator('.el-select').click(); await page.locator('.el-select-dropdown:visible').getByText(value, { exact: false }).first().click() }
async function selectProduct() { await drawer().locator('.doc-lines .el-table__body-wrapper tbody tr').first().locator('.el-select').first().click(); await page.locator('.el-select-dropdown:visible').getByText('验收商品', { exact: false }).first().click() }
async function setQuantity(n) { await drawer().locator('.doc-lines .el-table__body-wrapper tbody tr').first().locator('td').nth(2).locator('input').fill(String(n)); await drawer().locator('.doc-title').click() }
async function saveAndApprove() {
  await expect(drawer().locator('.trial-banner')).toContainText('试用期：剩余 10 天')
  await drawer().getByRole('button', { name: '保存并提交', exact: true }).click()
  await drawer().getByRole('button', { name: '审核入账', exact: true }).waitFor({ state: 'visible' })
  await drawer().getByRole('button', { name: '审核入账', exact: true }).click()
  await page.locator('.el-message-box:visible').getByRole('button', { name: '审核入账', exact: true }).click()
  await drawer().locator('.doc-title .el-tag').filter({ hasText: '已完成' }).waitFor({ state: 'visible', timeout: 15000 })
  await expect(page.locator('.el-message-box:visible')).toHaveCount(0)
}
async function closeDrawer() { await drawer().getByRole('button', { name: '关闭', exact: true }).click(); await page.locator('.el-drawer:visible').waitFor({ state: 'hidden' }) }

try {
  await page.goto(base)
  await page.getByRole('button', { name: '登录系统', exact: true }).waitFor({ state: 'visible' })
  await expect(page.locator('.trial-banner')).toContainText('试用期：剩余 10 天')
  await expect(page).toHaveTitle(defaultBrand.app_name)
  await expect(page.locator('.login-story .brand')).toContainText(defaultBrand.brand_short)
  await page.evaluate(() => document.fonts.load('16px "Noto Sans SC"', '进销存管理'))
  const cdp = await context.newCDPSession(page)
  await cdp.send('DOM.enable'); await cdp.send('CSS.enable')
  const document = await cdp.send('DOM.getDocument')
  const heading = await cdp.send('DOM.querySelector', { nodeId: document.root.nodeId, selector: '.login-card h2' })
  const renderedFonts = (await cdp.send('CSS.getPlatformFontsForNode', { nodeId: heading.nodeId })).fonts
  assert.ok(renderedFonts.length && renderedFonts.every(font => font.isCustomFont && /Noto.*Sans.*SC/i.test(font.familyName)), 'Chinese heading must render with the bundled free font')
  await cdp.detach()
  check('bundled_noto_font_renders_chinese_offline', { fonts:renderedFonts.map(font => font.familyName) })
  await page.screenshot({ path: path.join(output, '01-first-run.png'), fullPage: true })
  await page.locator('.trial-banner').getByRole('button', { name: '立即授权' }).click()
  await page.getByPlaceholder('请输入用户名').fill('admin')
  await page.getByPlaceholder('请输入密码', { exact: true }).fill('1qaz@WSX3edc')
  await page.getByRole('button', { name: '登录系统', exact: true }).click()
  await expect(page.locator('.license-card')).toContainText('试用中')
  check('default_admin_login_and_trial_authorization_entry')
  await page.locator('.user-menu').hover()
  await page.getByRole('menuitem', { name: '修改密码', exact: true }).click()
  const passwordDialog = page.locator('.el-dialog:visible')
  await field(passwordDialog, /^原密码$/).locator('input').fill('1qaz@WSX3edc')
  await field(passwordDialog, /^新密码$/).locator('input').fill('Browser-Test-2026')
  await passwordDialog.getByRole('button', { name: '保存并重新登录', exact: true }).click()
  await page.getByRole('button', { name: '登录系统', exact: true }).waitFor({ state: 'visible' })
  await page.getByPlaceholder('请输入密码', { exact: true }).fill('Browser-Test-2026')
  await page.getByRole('button', { name: '登录系统', exact: true }).click()
  await page.locator('.page-head h1').waitFor({ state: 'visible' })
  const oldPassword = await context.request.post(base + '/api/auth/login', { data: { username:'admin', password:'1qaz@WSX3edc' } })
  assert.equal((await oldPassword.json()).ok, false)
  await api('/system/settings', 'PUT', { company_name: '验收测试企业' })
  check('password_changed_inside_client_old_password_rejected')
  if (electronApp) {
    assert.equal(await page.evaluate(() => window.ruilianDesktop?.isDesktop), true)
    assert.equal(await page.evaluate(() => typeof window.require), 'undefined')
    check('native_desktop_window_with_isolated_renderer')
  }

  await goto('/system?tab=settings')
  await field(page, /^软件名称$/).locator('input').fill(testBrand.app_name)
  await field(page, /^品牌简称$/).locator('input').fill(testBrand.brand_short)
  const customLogo = await page.evaluate(() => { const canvas = document.createElement('canvas'); canvas.width = canvas.height = 64; const ctx = canvas.getContext('2d'); ctx.fillStyle = '#087f6b'; ctx.fillRect(0,0,64,64); ctx.fillStyle = '#fff1bd'; ctx.fillRect(16,16,32,32); return canvas.toDataURL('image/png') })
  const logoFile = { name:'验收 Logo.png', mimeType:'image/png', buffer:Buffer.from(customLogo.split(',')[1], 'base64') }
  await page.locator('.brand-settings input[type="file"]').setInputFiles(logoFile)
  await expect(page.locator('.logo-editor .brand-mark')).toHaveAttribute('src',customLogo)
  await page.getByRole('button', { name:'保存品牌设置',exact:true }).click()
  await expect(page).toHaveTitle(testBrand.app_name)
  await expect(page.locator('.sidebar .brand')).toContainText(testBrand.brand_short)
  await expect(page.locator('.sidebar .brand-mark')).toHaveAttribute('src',customLogo)
  if (electronApp) await expect.poll(() => electronApp.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].getTitle())).toBe(testBrand.app_name)
  await expect(page.getByRole('button', { name:'保存品牌设置',exact:true })).toBeEnabled()
  await page.getByRole('button', { name:'恢复默认图标',exact:true }).click()
  await page.getByRole('button', { name:'保存品牌设置',exact:true }).click()
  await expect(page.locator('.sidebar .brand-mark')).toHaveAttribute('src','/brand-default.png')
  await expect(page.getByRole('button', { name:'保存品牌设置',exact:true })).toBeEnabled()
  await page.locator('.brand-settings input[type="file"]').setInputFiles(logoFile)
  await expect(page.locator('.logo-editor .brand-mark')).toHaveAttribute('src',customLogo)
  await page.getByRole('button', { name:'保存品牌设置',exact:true }).click()
  await expect(page.locator('.sidebar .brand-mark')).toHaveAttribute('src',customLogo)
  const publicBrand = (await api('/status')).branding
  assert.deepEqual(publicBrand,{...testBrand,brand_logo:customLogo})
  await expect(page.locator('.el-message')).toHaveCount(0, { timeout:7000 })
  await page.screenshot({ path:path.join(output,'11-brand-settings.png'), fullPage:true })
  await goto('/system?tab=license')
  await expect(page.locator('.license-card h2')).toHaveText(testBrand.app_name)
  await api('/auth/logout','POST',{})
  await page.reload()
  await expect(page.locator('.login-story .brand')).toContainText(testBrand.brand_short)
  await expect(page.locator('.login-story .brand-mark')).toHaveAttribute('src',customLogo)
  await page.getByPlaceholder('请输入密码', { exact:true }).fill('Browser-Test-2026')
  await page.getByRole('button', { name:'登录系统',exact:true }).click()
  await expect(page.locator('.license-card h2')).toHaveText(testBrand.app_name)
  check('configurable_brand_name_logo_reset_and_public_login_persistence')

  const supplier = await api('/masters/supplier', 'POST', { name: '验收供应商', code: 'E2E-SUP', active: true, data: { contact: '李经理', phone: '13800000001', term_days: 30 } })
  const customer = await api('/masters/customer', 'POST', { name: '验收客户', code: 'E2E-CUS', active: true, data: { contact: '王经理', phone: '13800000002', level: '普通客户', term_days: 30, credit_limit: '1000000' } })
  await goto('/masters/product')
  await page.getByRole('button', { name: '新增商品', exact: true }).click()
  await field(drawer(), /^商品名称/).locator('input').fill('验收商品')
  await field(drawer(), /^编码$/).locator('input').fill('E2E-P001')
  await field(drawer(), /^参考采购价$/).locator('input').fill('10')
  await field(drawer(), /^基础销售价$/).locator('input').fill('15')
  await field(drawer(), /^零售价$/).locator('input').fill('18')
  await field(drawer(), /^库存下限$/).locator('input').fill('5')
  await drawer().getByRole('button', { name: '保存资料' }).click()
  await page.locator('.el-drawer:visible').waitFor({ state: 'hidden' })
  const product = (await api('/masters/product')).items.find(p => p.code === 'E2E-P001')
  assert.ok(product); check('master_product_created_in_ui', { product_id: product.id })

  await goto('/purchase')
  await page.getByRole('tab', { name: '采购入库', exact: true }).click()
  await page.getByRole('button', { name: '新建采购入库', exact: true }).click()
  await select(drawer(), /^供应商/, '验收供应商')
  await selectProduct(); await setQuantity(40)
  await saveAndApprove()
  await page.screenshot({ path: path.join(output, '02-purchase-approved.png'), fullPage: true })
  await closeDrawer()
  assert.equal((await api('/inventory/balances')).items[0].quantity, '40')
  assert.equal((await api('/finance/bills?side=ap')).items[0].remaining, '400.00')
  check('purchase_receipt_stock_and_payable')

  await goto('/sales')
  await page.getByRole('tab', { name: '销售出库', exact: true }).click()
  await page.getByRole('button', { name: '新建销售出库', exact: true }).click()
  await select(drawer(), /^客户/, '验收客户')
  await selectProduct(); await setQuantity(5)
  await saveAndApprove(); await closeDrawer()
  const sale = (await api('/documents?kind=sales_delivery')).items[0]
  assert.equal(sale.amount, '75.00')
  assert.equal((await api('/inventory/balances')).items[0].quantity, '35')
  check('sales_delivery_price_stock_and_receivable', { document_id: sale.id })

  await goto('/finance')
  await page.getByRole('button', { name: '新建收付款', exact: true }).click()
  await page.getByRole('menuitem', { name: '客户收款 / 预收', exact: true }).click()
  await select(drawer(), /^客户/, '验收客户')
  await field(drawer(), /^金额/).locator('input').fill('50')
  await saveAndApprove(); await closeDrawer()
  assert.equal((await api('/finance/bills?side=ar')).items[0].remaining, '25.00')
  check('partial_receipt_and_writeoff')

  await goto('/sales?kind=sales_delivery&id=' + sale.id)
  await drawer().getByRole('button', { name: '生成下游单据', exact: false }).click()
  await page.getByRole('menuitem', { name: '销售退货', exact: true }).click()
  await drawer().locator('.doc-title h2').filter({ hasText: '销售退货' }).waitFor({ state: 'visible' })
  await setQuantity(1)
  await saveAndApprove(); await closeDrawer()
  assert.equal((await api('/inventory/balances')).items[0].quantity, '36')
  const bills = (await api('/finance/bills?side=ar')).items
  assert.equal(bills.reduce((sum, b) => sum + Math.round(Number(b.remaining) * 100), 0), 1000)
  check('source_return_and_receivable_offset')

  await goto('/sales')
  await page.getByRole('tab', { name: '销售出库', exact: true }).click()
  await page.getByRole('button', { name: '新建销售出库', exact: true }).click()
  await select(drawer(), /^客户/, '验收客户'); await selectProduct(); await setQuantity(999)
  await drawer().getByRole('button', { name: '保存并提交', exact: true }).click()
  await drawer().getByRole('button', { name: '审核入账', exact: true }).click()
  await page.locator('.el-message-box:visible').getByRole('button', { name: '审核入账', exact: true }).click()
  await page.locator('.el-message--error').filter({ hasText: '可用库存不足' }).waitFor({ state: 'visible' })
  assert.equal((await api('/inventory/balances')).items[0].quantity, '36')
  await closeDrawer(); check('insufficient_stock_keeps_document_pending')

  for (const route of ['/inventory','/finance?view=aging','/finance?view=statement','/prices','/reports','/warnings','/system?tab=settings','/system?tab=numbering','/system?tab=users','/system?tab=roles','/system?tab=audit','/system?tab=license']) {
    await goto(route)
    check('page_loaded', { route })
  }
  await goto('/prices')
  await page.getByRole('button', { name: '新增价格规则', exact: true }).click()
  await field(drawer(), /^规则名称/).locator('input').fill('验收客户专享价')
  await select(drawer(), /^适用商品/, '验收商品')
  await select(drawer(), /^指定客户/, '验收客户')
  await field(drawer(), /^规则单价/).locator('input').fill('14.5')
  await drawer().getByRole('button', { name: '保存规则', exact: true }).click()
  await page.locator('.el-drawer:visible').waitFor({ state: 'hidden' })
  await page.getByRole('button', { name: '试算销售价格', exact: true }).click()
  const quoteDialog = page.locator('.el-dialog:visible')
  await select(quoteDialog, /^商品$/, '验收商品')
  await select(quoteDialog, /^客户$/, '验收客户')
  await quoteDialog.getByRole('button', { name: '计算价格', exact: true }).click()
  await expect(quoteDialog.locator('.summary-strip')).toContainText('14.5')
  assert.equal((await api('/prices/quote?product_id=' + product.id + '&customer_id=' + customer.id + '&quantity=1')).price, '14.5')
  await quoteDialog.getByRole('button', { name: '关闭', exact: true }).click()
  check('customer_price_rule_and_quote_ui')

  await goto('/reports')
  if (electronApp) {
    const before = await electronApp.evaluate(() => globalThis.__acceptanceDownloads.length)
    await page.getByRole('button', { name: '导出报表', exact: true }).click()
    await expect.poll(() => electronApp.evaluate((_electron, index) => globalThis.__acceptanceDownloads[index]?.state || '', before), { timeout:15000 }).toMatch(/completed|interrupted|cancelled/)
    const exported = await electronApp.evaluate((_electron, index) => globalThis.__acceptanceDownloads[index], before)
    assert.equal(exported.state, 'completed', JSON.stringify(exported))
    await fs.copyFile(exported.path, path.join(output, 'sales-report.csv'))
  } else {
    const downloadPromise = page.waitForEvent('download')
    await page.getByRole('button', { name: '导出报表', exact: true }).click()
    const reportDownload = await downloadPromise
    await reportDownload.saveAs(path.join(output, 'sales-report.csv'))
  }
  const csv = await fs.readFile(path.join(output, 'sales-report.csv'), 'utf8')
  assert.ok(csv.includes('60.00'), 'net sales report missing expected 60.00')
  await page.screenshot({ path: path.join(output, '03-reports.png'), fullPage: true })
  check('report_csv_export')
  await page.locator('.filter-row .el-select').click()
  const monthlyResponse = page.waitForResponse(r => r.url().includes('/api/reports?') && new URL(r.url()).searchParams.get('group') === 'month')
  await page.locator('.el-select-dropdown:visible').getByText('按月统计', { exact: true }).click()
  assert.equal((await (await monthlyResponse).json()).data.items[0].amount, '60.00')
  check('monthly_sales_report_ui')
  for (const [key, label] of [['operations','经营分析'],['sales_returns','销售退货'],['purchase_returns','采购退货'],['inventory_count','盘点差异'],['inventory','库存分析'],['ar','客户应收'],['ap','供应商应付']]) {
    const responsePromise = page.waitForResponse(r => r.url().includes('/api/reports?') && new URL(r.url()).searchParams.get('category') === key)
    await page.getByRole('tab', { name: label, exact: true }).click()
    const report = await (await responsePromise).json()
    assert.equal(report.ok, true)
    await expect(page.locator('.el-loading-mask:visible')).toHaveCount(0)
    if (key === 'operations') {
      assert.equal(report.data.items[0].sales, '60.00')
      assert.equal(report.data.items[0].cash_received, '50.00')
      assert.equal(report.data.items[0].gross_profit, '20.00')
      await page.screenshot({ path: path.join(output, '08-operating-report.png'), fullPage: true })
    }
    if (key === 'sales_returns') assert.equal(report.data.items[0].amount, '-15.00')
    check('report_tab_ui', { category: key })
  }

  await goto('/finance?view=statement')
  await page.locator('.filter-row .el-select').click()
  await page.locator('.el-select-dropdown:visible').getByText('验收客户', { exact: true }).click()
  await expect(page.locator('.el-select-dropdown:visible')).toHaveCount(0)
  await page.waitForLoadState('networkidle')
  assert.ok((await page.locator('.summary-strip').innerText()).includes('10.00'))
  await page.screenshot({ path: path.join(output, '04-finance-statement.png'), fullPage: true })
  check('customer_statement_ui')
  await page.getByRole('button', { name: '账龄分析', exact: true }).click()
  await expect(page.locator('.subnav button.selected')).toHaveText('账龄分析')
  await page.goto(base + '/?acceptance=' + navigation + '#/finance?view=aging')
  await page.goto(base + '/?acceptance=' + navigation + '#/finance?view=statement')
  await expect(page.locator('.subnav button.selected')).toHaveText('往来对账')
  check('same_route_query_navigation')

  await goto('/system?tab=backups')
  await page.getByRole('button', { name: '立即备份', exact: true }).click()
  await page.locator('.el-message--success').filter({ hasText: '备份已完成' }).waitFor({ state: 'visible' })
  await page.locator('.el-table__body-wrapper tbody tr').first().getByRole('button', { name: '验证', exact: true }).click()
  await page.locator('.el-message--success').filter({ hasText: '恢复验证通过' }).waitFor({ state: 'visible' })
  await page.screenshot({ path: path.join(output, '05-backup.png'), fullPage: true })
  check('backup_creation_and_restore_validation_ui')

  await goto('/')
  await page.waitForTimeout(500)
  await page.screenshot({ path: path.join(output, '06-dashboard.png'), fullPage: true })
  const dashboard = await api('/dashboard')
  assert.equal(dashboard.sales, '60.00'); assert.equal(dashboard.gross_profit, '20.00'); assert.equal(dashboard.ar, '10.00'); assert.equal(dashboard.inventory_value, '360.00')
  check('dashboard_reconciles_to_business', { sales:dashboard.sales, profit:dashboard.gross_profit, ar:dashboard.ar, stock_value:dashboard.inventory_value })

  if (!electronApp) {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.waitForTimeout(300)
    await page.screenshot({ path: path.join(output, '07-mobile.png'), fullPage: true })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), 'mobile layout overflow')
    check('responsive_layout_390px')
  }

  const actualErrors = errors.filter(e => !e.includes('Failed to load resource: the server responded with a status of 400') && !e.includes('Failed to load resource: the server responded with a status of 401'))
  assert.deepEqual(actualErrors, [], 'browser runtime errors')
  await fs.writeFile(path.join(output, 'ui-verification.json'), JSON.stringify({ passed: true, mode: electronApp ? 'desktop' : 'browser', data_dir: electronApp ? desktopData : undefined, base, checkpoints, expectedBusinessValidationErrors: errors.filter(e => e.includes('status of 400')), expectedInitialAuthenticationErrors:errors.filter(e => e.includes('status of 401')), time: new Date().toISOString() }, null, 2))
  await fs.rm(path.join(output, 'ui-failure.json'), { force: true })
  await fs.rm(path.join(output, 'failure.png'), { force: true })
  process.stdout.write(electronApp ? 'DESKTOP_UI_VERIFICATION_PASSED\n' : 'BROWSER_VERIFICATION_PASSED\n')
} catch (error) {
  await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true }).catch(() => {})
  await fs.writeFile(path.join(output, 'ui-failure.json'), JSON.stringify({ message: String(error), errors, checkpoints, html: (await page.content()).slice(0, 18000) }, null, 2))
  throw error
} finally { if (electronApp) await electronApp.close(); if (browser) await browser.close() }
