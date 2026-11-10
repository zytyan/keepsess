'use strict';
const LIGHT_TERMINAL_THEME={background:'#ffffff',foreground:'#172b42',cursor:'#087565',selectionBackground:'#c5ddef',black:'#334155',red:'#b42332',green:'#166534',yellow:'#805300',blue:'#174ea6',magenta:'#7e22a2',cyan:'#086479',white:'#526377',brightBlack:'#5c6c80',brightRed:'#b42332',brightGreen:'#166534',brightYellow:'#805300',brightBlue:'#174ea6',brightMagenta:'#7e22a2',brightCyan:'#086479',brightWhite:'#172b42'};
const systemTheme=window.matchMedia('(prefers-color-scheme: light)');
let themeMode='dark';
try{const saved=localStorage.getItem('keepsess.theme');if(['dark','light','system'].includes(saved))themeMode=saved;}catch(_){}
function applyTheme(){
 const light=themeMode==='light'||themeMode==='system'&&systemTheme.matches;
 document.documentElement.dataset.theme=light?'light':'dark';
 const theme=light?LIGHT_TERMINAL_THEME:DARK_TERMINAL_THEME;
 TERMINAL_APPEARANCE.theme=theme;
 for(const h of terminalHighlights){h.term.options.theme=theme;h.invalidate();}
 document.getElementById('theme-mode').value=themeMode;
}
document.getElementById('theme-mode').addEventListener('change',event=>{
 themeMode=event.target.value;
 try{localStorage.setItem('keepsess.theme',themeMode);}catch(_){}
 applyTheme();
});
systemTheme.addEventListener('change',()=>{if(themeMode==='system')applyTheme();});
applyTheme();
