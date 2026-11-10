/* Shared sans-serif typography, served entirely from embedded local assets. */
'use strict';
const WEB_FONT_FAMILY='"JetBrains Mono", "Noto Sans SC", monospace';
const terminalFontsLoaded=document.fonts?Promise.all([
 document.fonts.load('400 14px "JetBrains Mono"','Keepsess 0123456789'),
 document.fonts.load('700 14px "JetBrains Mono"','Keepsess 0123456789'),
 document.fonts.load('400 14px "Noto Sans SC"','中文终端'),
 document.fonts.load('700 14px "Noto Sans SC"','中文终端'),
]).then(()=>true,()=>false):Promise.resolve(false);
async function waitForTerminalFonts(){
 let timer;
 try{return await Promise.race([terminalFontsLoaded,new Promise(resolve=>{timer=setTimeout(()=>resolve(false),5000);})]);}
 finally{clearTimeout(timer);}
}
function remeasureTerminalFonts(terminal){
 // xterm measures cell dimensions when its font option changes. A late font
 // download must refresh those dimensions without replaying output or input.
 terminal.options.fontFamily='sans-serif';terminal.options.fontFamily=WEB_FONT_FAMILY;
 terminal.refresh(0,terminal.rows-1);
}
