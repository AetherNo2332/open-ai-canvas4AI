'use strict';
const { app, BrowserWindow, dialog, safeStorage, session,ipcMain } = require('electron');
const path = require('node:path');
const { loadMasterKey } = require('./secret-store.cjs');
const { startHost } = require('./runtime.cjs');
const {createCommandJournalStore,authorizeJournalSender}=require('./command-journal.cjs');

app.setName('InfiniteCanvasDesktop');
const development=!app.isPackaged || process.argv.includes('--desktop-development');
const reopenMode=development && process.argv.includes('--desktop-smoke-reopen');
const settingsMode=development && (process.argv.includes('--desktop-settings-smoke') || process.argv.includes('--desktop-settings-reopen'));
const journalMode=development && (process.argv.includes('--desktop-journal-smoke') || process.argv.includes('--desktop-journal-reopen'));
const smokeMode=development && (process.argv.includes('--desktop-smoke') || reopenMode || settingsMode || journalMode);
const dataDir = !development ? path.join(app.getPath('appData'), 'InfiniteCanvasDesktop') : path.resolve(__dirname, journalMode?'../.local/desktop-journal-test':smokeMode ? '../.local/desktop-ui-test' : '../.local/desktop-dev');
app.setPath('userData', dataDir);
let window, host, stopping = false, quittingInFlight = false;
if (!app.requestSingleInstanceLock()) app.quit();
else {
  app.on('second-instance', () => { if (window) { if (window.isMinimized()) window.restore(); window.focus(); } });
  app.on('before-quit', event => {
    if (host && !stopping) {
      event.preventDefault();
      if(quittingInFlight)return;
      quittingInFlight=true;
      (async()=>{
        try {
          if(window && !window.isDestroyed()) {
            const saved=await window.webContents.executeJavaScript('window.__infiniteCanvasFlush ? window.__infiniteCanvasFlush() : true');
            if(!saved)throw new Error('尚有未确认的画布修改');
          }
          stopping=true;
          await host.stop();
          app.quit();
        } catch(err) {
          if(smokeMode){console.error(`desktop_shutdown_failed: ${err.message}`);stopping=true;await host.stop();window?.destroy();app.exit(1);}
          else dialog.showErrorBox('画布尚未保存', '保存未完成，应用保持打开。请处理保存错误后再关闭。');
        } finally {quittingInFlight=false;}
      })();
    }
  });
  app.on('window-all-closed', () => app.quit());
  app.whenReady().then(async () => {
    const masterKey = loadMasterKey({ dataDir, safeStorage });
    const resourcesDir = !development ? process.resourcesPath : path.resolve(__dirname, '../.local/desktop-dev-resources');
    try { host = await startHost({ resourcesDir, dataDir, masterKey }); }
    finally { masterKey.fill(0); }
    const desktopSession = session.fromPartition('desktop');
    await desktopSession.cookies.set({ url: host.origin, name: 'open_ai_canvas_session', value: host.sessionCookie,
      path: '/', httpOnly: true, sameSite: 'strict' });
    delete host.sessionCookie;
    desktopSession.setPermissionRequestHandler((webContents, permission, callback) => callback(false));
    window = new BrowserWindow({ width: 1440, height: 960, minWidth: 960, minHeight: 640, show: false,
      title: '无限画布', webPreferences: { preload: path.join(__dirname, 'preload.cjs'),
        nodeIntegration: false, contextIsolation: true, sandbox: true, session: desktopSession } });
    const commandJournal=createCommandJournalStore(dataDir);
    ipcMain.handle('canvas:command-journal:get',(event,key)=>{authorizeJournalSender(event,window?.webContents,host.origin);return commandJournal.get(key)});
    ipcMain.handle('canvas:command-journal:set',async(event,key,value)=>{authorizeJournalSender(event,window?.webContents,host.origin);await commandJournal.set(key,value)});
    window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
    window.webContents.on('will-navigate', (event, url) => { if (new URL(url).origin !== host.origin) event.preventDefault(); });
    window.once('ready-to-show', () => {if(!journalMode)window.show()});
    window.on('close',event=>{if(!stopping){event.preventDefault();app.quit();}});
    host.exited.then(() => { if (!stopping) { dialog.showErrorBox('本地主机已停止', '画布主机意外退出，请重新启动应用。'); app.quit(); } });
    await window.loadURL(`${host.origin}/canvas`);
    if(smokeMode) {
      const {runSmoke,runReopen}=require('./test/electron-smoke.cjs');
      try {
        if(journalMode) await require('./test/electron-command-journal-smoke.cjs').runJournal({window,host,artifactDir:path.resolve(__dirname,'../.local/evidence/B4'),reopen:process.argv.includes('--desktop-journal-reopen')});
        else if(settingsMode) await require('./test/electron-settings-smoke.cjs').runSettings({window,host,artifactDir:path.resolve(__dirname,'../.local/evidence/A4'),reopen:process.argv.includes('--desktop-settings-reopen')});
        else await (reopenMode ? runReopen : runSmoke)({window,host,artifactDir:path.resolve(__dirname,'../.local/evidence/A3')});
        app.quit();
      }
      catch(err){stopping=true;await host.stop();window.destroy();throw err;}
    }
  }).catch(err => {
    if(smokeMode){console.error(`desktop_smoke_failed: ${err.message}`);app.exit(1);return;}
    dialog.showErrorBox('无法启动无限画布', `启动失败：${err.message}`); app.quit();
  });
}
