const { contextBridge, ipcRenderer } = require('electron')
contextBridge.exposeInMainWorld('ruilianDesktop', Object.freeze({
  isDesktop: true,
  quit: () => ipcRenderer.invoke('ruilian:quit'),
  restore: () => ipcRenderer.invoke('ruilian:restore'),
  openDataDirectory: () => ipcRenderer.invoke('ruilian:open-data'),
  refreshBranding: () => ipcRenderer.invoke('ruilian:refresh-branding')
}))
