'use strict';
const { contextBridge,ipcRenderer } = require('electron');
// Only command journals are exposed; no paths, tokens or general IPC channel.
contextBridge.exposeInMainWorld('infiniteCanvasDesktop', Object.freeze({ protocol: 1, mode: 'desktop',
 commandJournal:Object.freeze({get:key=>ipcRenderer.invoke('canvas:command-journal:get',key),set:(key,value)=>ipcRenderer.invoke('canvas:command-journal:set',key,value)})
}));
