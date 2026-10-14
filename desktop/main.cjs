const { app, BrowserWindow, dialog, ipcMain, Menu, nativeImage, shell } = require('electron')
const { spawn } = require('node:child_process')
const { randomBytes } = require('node:crypto')
const fs = require('node:fs')
const path = require('node:path')
const defaultBrand = require('./branding.json')

app.setName('RuilianERP') // The HTTP user-agent product token must contain ASCII characters.
app.setAppUserModelId('cn.ruilian.erp')
const argv = process.argv.slice(app.isPackaged ? 1 : 2)
const argument = name => { const i = argv.indexOf(name); return i >= 0 ? argv[i + 1] : undefined }
const customData = argument('--data-dir') || process.env.RUILIAN_DATA_DIR
const dataDirectory = path.resolve(customData || path.join(app.getPath('appData'), 'RuilianERP', 'data'))
app.setPath('userData', customData ? path.join(dataDirectory, 'client-profile') : path.join(app.getPath('appData'), 'RuilianERP', 'client'))
const resources = app.isPackaged ? process.resourcesPath : path.resolve(__dirname, '..', 'artifacts', 'desktop-backend')
const backendPath = app.isPackaged ? path.join(resources, 'backend', '数据服务.exe') : path.join(resources, '数据服务.exe')
const iconPath = app.isPackaged ? path.join(resources, 'app.ico') : path.resolve(__dirname, '..', 'assets', 'app.ico')
const desktopToken = randomBytes(32).toString('hex')
let window, backend, address = '', stopping = false, quitting = false, allowExit = false, restoring = false
let displayBrand = { ...defaultBrand }
const delay = ms => new Promise(resolve => setTimeout(resolve, ms))
function log(message) {
  try { fs.mkdirSync(path.join(dataDirectory, 'logs'), { recursive: true }); fs.appendFileSync(path.join(dataDirectory, 'logs', 'client.log'), new Date().toISOString() + ' ' + String(message) + '\n') } catch {}
}
function runBackend(args) {
  return new Promise((resolve, reject) => {
    const child = spawn(backendPath, ['--no-browser', ...args], { windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] })
    let output = ''
    child.stdout.setEncoding('utf8').on('data', data => { output += data })
    child.stderr.setEncoding('utf8').on('data', data => { output += data })
    child.on('error', reject)
    child.on('exit', code => code === 0 ? resolve(output) : reject(new Error(output || '数据服务未能完成操作，退出码 ' + code)))
  })
}
async function startBackend() {
  if (!fs.existsSync(backendPath)) throw new Error('数据服务文件缺失，请重新安装客户端。')
  fs.mkdirSync(dataDirectory, { recursive: true })
  stopping = false
  address = ''
  let startupError = '', exited = false
  backend = spawn(backendPath, ['--no-browser', '--port', '0', '--data-dir', dataDirectory], {
    windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, RUILIAN_DESKTOP_TOKEN: desktopToken }
  })
  const child = backend
  child.stdout.setEncoding('utf8').on('data', log)
  child.stderr.setEncoding('utf8').on('data', data => { startupError += data; log(data) })
  child.on('error', error => { startupError = error.message; exited = true })
  child.on('exit', code => {
    exited = true
    log('数据服务退出：' + code)
    if (!stopping && address && !quitting) {
      address = ''
      void dialog.showMessageBox(window, { type: 'error', title: '数据服务已停止', message: '数据服务意外停止，请重新启动客户端。', detail: '数据目录：' + dataDirectory })
    }
  })
  for (let i = 0; i < 250; i++) {
    if (exited) throw new Error(startupError || '数据服务启动失败，请查看数据目录中的日志。')
    try {
      const runtime = JSON.parse(fs.readFileSync(path.join(dataDirectory, '.runtime.json'), 'utf8'))
      if (runtime.pid === child.pid && /^http:\/\/127\.0\.0\.1:\d+$/.test(runtime.url)) {
        const response = await fetch(runtime.url + '/api/health', { signal: AbortSignal.timeout(1000) })
        if (response.ok) { address = runtime.url; return address }
      }
    } catch {}
    await delay(100)
  }
  throw new Error('本地数据准备超时，请检查数据目录和日志。')
}
async function stopBackend() {
  stopping = true
  const child = backend
  if (!child || child.exitCode !== null) return
  const ended = new Promise(resolve => child.once('exit', resolve))
  if (address) {
    try { await fetch(address + '/api/desktop/shutdown', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Ruilian-Desktop-Token': desktopToken }, body: '{}', signal: AbortSignal.timeout(5000) }) } catch (error) { log(error.message) }
  }
  await Promise.race([ended, delay(32000)])
  if (child.exitCode === null) { log('数据服务停止超时'); child.kill(); await Promise.race([ended, delay(3000)]) }
  address = ''
  backend = undefined
}
async function quit() {
  if (quitting || restoring) return
  quitting = true
  try { await stopBackend() } finally { allowExit = true; app.quit() }
}
function trustedSender(event) {
  return window && event.sender === window.webContents && address && event.senderFrame.url.startsWith(address + '/')
}
async function refreshBranding() {
  if (!address || !window || window.isDestroyed()) return
  try {
    const response = await fetch(address + '/api/status', { signal: AbortSignal.timeout(5000) })
    const result = await response.json()
    if (!result.ok || typeof result.data.branding?.app_name !== 'string') return
    displayBrand = { ...defaultBrand, ...result.data.branding }
    window.setTitle(displayBrand.app_name)
    const logo = displayBrand.brand_logo
    if (typeof logo === 'string' && logo.length < 710000 && /^data:image\/(png|jpeg);base64,/.test(logo)) {
      const image = nativeImage.createFromDataURL(logo)
      if (!image.isEmpty()) window.setIcon(image)
    } else window.setIcon(iconPath)
  } catch (error) { log('更新显示名称：' + error.message) }
}
async function restore() {
  if (restoring || quitting) return false
  const selection = await dialog.showOpenDialog(window, { title: '选择数据库备份', properties: ['openFile'], filters: [{ name: 'SQLite 数据库备份', extensions: ['sqlite', 'db'] }] })
  if (selection.canceled || !selection.filePaths[0]) return false
  const source = selection.filePaths[0]
  const confirmation = await dialog.showMessageBox(window, { type: 'warning', title: '确认恢复数据库', message: '将使用所选备份恢复经营数据。', detail: '恢复前会自动保留当前数据库的完整副本。\n\n备份：' + source + '\n目标：' + dataDirectory + '\n\n未保存的编辑内容将不会保留。', buttons: ['取消', '验证并恢复'], defaultId: 0, cancelId: 0, noLink: true })
  if (confirmation.response !== 1) return false
  restoring = true
  try {
    await runBackend(['--verify-backup', source])
    await stopBackend()
    await window.loadFile(path.join(__dirname, 'loading.html'))
    await runBackend(['--data-dir', dataDirectory, '--restore', source])
    await startBackend()
    await refreshBranding()
    await window.loadURL(address)
    await dialog.showMessageBox(window, { type: 'info', title: '恢复完成', message: '数据库已恢复，原数据库副本已保留。', detail: '请核对库存、往来账款和软件授权状态。' })
    return true
  } catch (error) {
    log(error.stack)
    await dialog.showMessageBox(window, { type: 'error', title: '恢复未完成', message: error.message })
    if (!address) { try { await startBackend(); await window.loadURL(address) } catch (restartError) { log(restartError.message) } }
    return false
  } finally { restoring = false }
}
function createWindow() {
  window = new BrowserWindow({ width: 1440, height: 1000, minWidth: 1060, minHeight: 720,
    title: displayBrand.app_name, icon: iconPath, backgroundColor: '#f3f6fb',
    show: process.env.RUILIAN_TEST_HIDDEN !== '1', autoHideMenuBar: false,
    webPreferences: { preload: path.join(__dirname, 'preload.cjs'), contextIsolation: true, nodeIntegration: false, sandbox: true, spellcheck: false,
      backgroundThrottling: false, offscreen: process.env.RUILIAN_TEST_HIDDEN === '1' }
  })
  window.webContents.setWindowOpenHandler(({ url }) => ({
    action: url === 'about:blank' ? 'allow' : 'deny',
    overrideBrowserWindowOptions: { width: 1000, height: 800, autoHideMenuBar: true, webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true } }
  }))
  window.webContents.on('will-navigate', (event, url) => { if (!address || !url.startsWith(address + '/')) event.preventDefault() })
  window.webContents.on('will-redirect', (event, url) => { if (!address || !url.startsWith(address + '/')) event.preventDefault() })
  window.webContents.on('page-title-updated', event => { event.preventDefault(); window.setTitle(displayBrand.app_name) })
  window.webContents.session.setPermissionRequestHandler((_contents, permission, callback) => callback(permission === 'clipboard-sanitized-write'))
  window.webContents.session.on('will-download', (_event, item) => {
    item.setSaveDialogOptions({ title: '保存导出文件', defaultPath: path.join(app.getPath('downloads'), path.basename(item.getFilename())) })
  })
  window.on('close', event => { if (!allowExit) { event.preventDefault(); void quit() } })
  window.webContents.on('render-process-gone', (_event, details) => log('界面进程退出：' + details.reason))
  return window
}

const commandMode = argv.includes('--verify-backup') || argv.includes('--restore')
if (!commandMode && !app.requestSingleInstanceLock()) {
  allowExit = true
  app.quit()
} else {
  app.on('second-instance', () => { if (window) { if (window.isMinimized()) window.restore(); window.show(); window.focus() } })
  app.on('before-quit', event => { if (!allowExit && !commandMode) { event.preventDefault(); void quit() } })
  app.on('window-all-closed', () => { if (!quitting) void quit() })
  app.whenReady().then(async () => {
    if (commandMode) {
      try { const output = await runBackend(argv.filter(arg => arg !== '--no-browser')); process.stdout.write(output); app.exit(0) }
      catch (error) { process.stderr.write(error.message); app.exit(1) }
      return
    }
    ipcMain.handle('ruilian:quit', event => { if (trustedSender(event)) void quit() })
    ipcMain.handle('ruilian:restore', event => trustedSender(event) ? restore() : false)
    ipcMain.handle('ruilian:open-data', event => { if (trustedSender(event)) return shell.openPath(dataDirectory) })
    ipcMain.handle('ruilian:refresh-branding', event => { if (trustedSender(event)) return refreshBranding() })
    Menu.setApplicationMenu(Menu.buildFromTemplate([
      { label: '文件', submenu: [{ label: '从备份恢复…', click: () => void restore() }, { label: '打开数据目录', click: () => void shell.openPath(dataDirectory) }, { type: 'separator' }, { label: '退出软件', accelerator: 'Alt+F4', click: () => void quit() }] },
      { label: '编辑', submenu: [{ label: '撤销', role: 'undo' }, { label: '重做', role: 'redo' }, { type: 'separator' }, { label: '剪切', role: 'cut' }, { label: '复制', role: 'copy' }, { label: '粘贴', role: 'paste' }, { label: '全选', role: 'selectAll' }] },
      { label: '视图', submenu: [{ label: '刷新界面', role: 'reload' }, { label: '切换全屏', role: 'togglefullscreen' }, { label: '恢复默认缩放', role: 'resetZoom' }, { label: '放大', role: 'zoomIn' }, { label: '缩小', role: 'zoomOut' }] },
      { label: '帮助', submenu: [{ label: '关于本软件', click: () => void dialog.showMessageBox(window, { type: 'info', title: '关于本软件', message: displayBrand.app_name + ' V' + app.getVersion(), detail: '独立桌面客户端 · SQLite 本地数据\n默认账号：admin\n初始密码：admin（可在软件内修改）\n字体：Noto Sans SC（OFL 1.1）\n图标：Element Plus Icons（MIT）\n数据目录：' + dataDirectory }) }, { label: '第三方许可证', click: () => void shell.openPath(app.isPackaged ? path.join(resources, '第三方许可证.txt') : path.resolve(__dirname, '../docs/第三方许可证.txt')) }] }
    ]))
    createWindow()
    await window.loadFile(path.join(__dirname, 'loading.html'))
    try { await startBackend(); await refreshBranding(); await window.loadURL(address) }
    catch (error) {
      log(error.stack)
      const result = await dialog.showMessageBox(window, { type: 'error', title: '软件启动失败', message: error.message, detail: '数据目录：' + dataDirectory, buttons: ['退出', '从备份恢复'], defaultId: 0, cancelId: 0 })
      if (result.response === 1) { const restored = await restore(); if (!restored) void quit() } else void quit()
    }
  }).catch(error => { log(error.stack); allowExit = true; app.exit(1) })
}
