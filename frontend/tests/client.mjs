import { _electron as electron, expect } from '@playwright/test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'

const executablePath = process.env.RUILIAN_ELECTRON_PATH
const dataDir = process.env.RUILIAN_TEST_DATA
assert.ok(executablePath && dataDir, 'Set RUILIAN_ELECTRON_PATH and RUILIAN_TEST_DATA; first run e2e.mjs on this directory')
const root = path.resolve('..'), output = path.join(root, 'artifacts/screenshots'), directory = path.dirname(dataDir)
const backend = path.join(path.dirname(executablePath), 'resources/backend/数据服务.exe')
const issuer = path.join(root, 'publisher-private/授权管理工具.exe'), key = path.join(root, 'publisher-private/issuer-private.pem')
const checkpoints = [], errors = []
let client, page, context, base, currentData = dataDir
const check = (name, extra = {}) => { checkpoints.push({ name, ...extra }); console.log(JSON.stringify({ checkpoint:name, ...extra })) }
const sha = bytes => createHash('sha256').update(bytes).digest('hex')
async function start(data) {
  currentData = data
  client = await electron.launch({ executablePath, args:['--data-dir',data], env:{...process.env,RUILIAN_TEST_HIDDEN:'1'}, timeout:60000 })
  page = await client.firstWindow()
  await page.waitForURL(url => url.hostname === '127.0.0.1', { timeout:35000 })
  context = client.context(); base = new URL(page.url()).origin
  await context.route('**/*', route => route.request().url().startsWith(base) || route.request().url().startsWith('data:') || route.request().url().startsWith('blob:') ? route.continue() : route.abort())
  page.on('pageerror', error => errors.push(error.message))
  await api('/auth/login','POST',{username:'admin',password:'Browser-Test-2026'})
  await page.goto(base)
  await page.locator('.page-head h1').waitFor()
}
async function close() {
  if (!client) return
  await client.close(); client = undefined
  await expect.poll(async () => fs.stat(path.join(currentData,'.runtime.json')).then(() => true, () => false),{timeout:10000}).toBe(false)
}
async function api(url, method='GET', data) {
  const response = await context.request.fetch(base+'/api'+url,{method,data,headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()}})
  const body = await response.json(); assert.equal(body.ok,true,url+': '+JSON.stringify(body)); return body.data
}
async function go(route) { await page.goto(base+'/?clienttest='+Date.now()+'#'+route); await page.waitForLoadState('networkidle'); await expect(page.locator('.el-loading-mask:visible')).toHaveCount(0) }
async function quantity() { return (await api('/inventory/balances')).items[0].quantity }
function issue(installation, customer, name) {
  const file = path.join(directory, name)
  execFileSync(issuer,['issue','--key',key,'--installation',installation,'--customer',customer,'--out',file],{windowsHide:true,encoding:'utf8'})
  return file
}
async function activate(file) {
  await go('/system?tab=license')
  await page.locator('input[type="file"]').setInputFiles(file)
  await page.getByRole('button',{name:'激活永久授权',exact:true}).click()
  await expect(page.locator('.license-card')).toContainText('已授权')
  await expect(page.locator('.trial-banner')).toHaveCount(0)
}
async function chooseRestore(file) {
  await client.evaluate(({dialog},source)=>{
    dialog.showOpenDialog = async()=>({canceled:false,filePaths:[source]})
    dialog.showMessageBox = async(...args)=>({response:args.at(-1).title==='确认恢复数据库'?1:0,checkboxChecked:false})
  },file)
}
try {
  await start(dataDir)
  assert.equal(await quantity(),'36')
  assert.equal((await api('/status')).branding.app_name,'本地经营管理 · 验收')
  await expect(page).toHaveTitle('本地经营管理 · 验收')
  await expect(page.locator('.sidebar .brand-mark')).toHaveAttribute('src',/^data:image\/png;base64,/)
  check('custom_brand_name_and_logo_survive_native_client_restart')
  const info = await client.evaluate(({BrowserWindow,app})=>({version:app.getVersion(),windows:BrowserWindow.getAllWindows().length,preferences:BrowserWindow.getAllWindows()[0].webContents.getLastWebPreferences(),electron:process.versions.electron}))
  assert.equal(info.windows,1); assert.equal(info.preferences.nodeIntegration,false); assert.equal(info.preferences.contextIsolation,true)
  assert.equal(await page.evaluate(()=>window.ruilianDesktop.isDesktop),true)
  check('installed_native_client_restarts_with_changed_password_and_existing_stock',{electron:info.electron})
  for (const route of ['/','/masters/product','/purchase','/sales','/inventory','/finance','/prices','/reports','/warnings','/system?tab=settings']) {
    await go(route)
    await expect(page.locator('.trial-banner')).toContainText('试用期：剩余 10 天')
    await page.locator('.trial-banner').getByRole('button',{name:'立即授权',exact:true}).click()
    await expect(page.locator('.license-card')).toContainText('试用中')
    assert.ok(page.url().includes('tab=license'))
  }
  check('every_business_page_trial_banner_and_one_click_authorization')
  await go('/')
  await page.screenshot({path:path.join(output,'10-native-client-trial.png'),fullPage:true})
  const trial = await api('/license')
  const licenseFile = issue(trial.installation_id,'DESKTOP-ACCEPTANCE','桌面客户端测试.license')
  await activate(licenseFile)
  const active = await api('/license')
  assert.equal(active.license.license_type,'PERPETUAL')
  await page.screenshot({path:path.join(output,'09-perpetual-license.png'),fullPage:true})
  await close(); await start(dataDir)
  assert.equal((await api('/license')).license.license_id,active.license.license_id)
  await expect(page.locator('.trial-banner')).toHaveCount(0)
  check('native_license_file_import_and_offline_restart_persistence')

  const replicas = [path.join(directory,'客户端副本 A'),path.join(directory,'客户端副本 B')]
  await api('/system/settings','PUT',{backup_destinations:replicas})
  const backup = await api('/system/backups','POST',{})
  assert.equal(backup.status,'ok'); assert.equal(backup.replicas.length,2)
  const source = path.join(dataDir,'backups',backup.filename)
  for (const destination of replicas) assert.equal(sha(await fs.readFile(path.join(destination,backup.filename))),backup.sha256)
  execFileSync(backend,['--no-browser','--verify-backup',source],{windowsHide:true})
  check('native_client_backup_and_two_verified_directory_copies')
  const product = (await api('/masters/product')).items.find(p=>p.code==='E2E-P001')
  const supplier = (await api('/masters/supplier')).items[0]
  const warehouse = (await api('/masters/warehouse')).items[0]
  const date = new Date().toLocaleDateString('sv-SE')
  const extra = await api('/documents','POST',{kind:'purchase_receipt',date,partner_id:supplier.id,warehouse_id:warehouse.id,data:{},items:[{product_id:product.id,quantity:'1',price:'10'}]})
  await api(`/documents/${extra.id}/actions/submit`,'POST',{})
  await api(`/documents/${extra.id}/actions/approve`,'POST',{})
  assert.equal(await quantity(),'37')
  await go('/system?tab=backups')
  const invalid = path.join(directory,'故意损坏的桌面测试备份.sqlite')
  await fs.writeFile(invalid,'Invalid database fixture')
  const beforeRuntime = JSON.parse(await fs.readFile(path.join(dataDir,'.runtime.json'),'utf8'))
  await chooseRestore(invalid)
  assert.equal(await page.evaluate(()=>window.ruilianDesktop.restore()),false)
  assert.equal(await quantity(),'37')
  assert.equal(JSON.parse(await fs.readFile(path.join(dataDir,'.runtime.json'),'utf8')).pid,beforeRuntime.pid)
  check('native_restore_rejects_corrupt_backup_without_stopping_current_service')

  await chooseRestore(source)
  const previous = base
  // Restore restarts the local service on a new port. Allow its new loopback URL.
  await context.unroute('**/*')
  await context.route('**/*',route=> /^http:\/\/127\.0\.0\.1:\d+\//.test(route.request().url()) || route.request().url().startsWith('file:') ? route.continue() : route.abort())
  const reloaded = page.waitForURL(url=>url.hostname==='127.0.0.1' && url.origin!==previous,{timeout:45000})
  await page.getByRole('button',{name:'从备份恢复',exact:true}).click()
  await reloaded; base = new URL(page.url()).origin
  await page.locator('.page-head h1').waitFor()
  await api('/auth/login','POST',{username:'admin',password:'Browser-Test-2026'})
  assert.equal(await quantity(),'36')
  assert.equal((await api('/license')).status,'active')
  assert.equal((await api('/status')).branding.app_name,'本地经营管理 · 验收')
  const saved = (await fs.readdir(dataDir)).filter(name=>name.startsWith('pre-restore-'))
  assert.equal(saved.length,1)
  assert.ok((await fs.stat(path.join(dataDir,saved[0],'ruilian.db'))).size>0)
  check('restore_from_native_client_restarts_service_and_preserves_previous_database',{before_quantity:'37',after_quantity:'36',preserved:path.join(dataDir,saved[0])})
  await close()
  check('closing_native_client_gracefully_stops_its_backend')

  const migrated = path.join(directory,'桌面迁移测试')
  execFileSync(backend,['--no-browser','--data-dir',migrated,'--restore',source],{windowsHide:true})
  await start(migrated)
  const state = await api('/license')
  assert.equal(state.status,'abnormal'); assert.notEqual(state.installation_id,trial.installation_id)
  assert.equal(await quantity(),'36')
  await expect(page.locator('.trial-banner')).toContainText('软件授权需要处理')
  await api('/system/backups','POST',{})
  const report = await context.request.get(base+'/api/export?type=report&category=operations')
  assert.equal(report.status(),200); assert.ok((await report.text()).includes('净销售额'))
  const rebound = issue(state.installation_id,'DESKTOP-REBIND','桌面迁移测试.license')
  await activate(rebound)
  assert.equal((await api('/license')).status,'active')
  check('native_client_migration_keeps_read_export_backup_and_supports_rebinding')
  await close()
  assert.deepEqual(errors,[])
  await fs.writeFile(path.join(root,'artifacts/client-verification.json'),JSON.stringify({passed:true,checkpoints,time:new Date().toISOString()},null,2))
  console.log('NATIVE_CLIENT_ACCEPTANCE_PASSED')
} catch(error) {
  await fs.writeFile(path.join(root,'artifacts/client-verification.json'),JSON.stringify({passed:false,error:String(error),errors,checkpoints},null,2))
  throw error
} finally { await close() }
