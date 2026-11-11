'use strict';
function attachTerminalContextMenu(term){
 const events=new AbortController(),options={capture:true,signal:events.signal};
 const menu=document.createElement('div');menu.className='terminal-context-menu';menu.hidden=true;menu.setAttribute('role','menu');menu.setAttribute('aria-label','终端操作');
 const copy=document.createElement('button'),all=document.createElement('button'),withSource=document.createElement('button');
 withSource.type='button';withSource.textContent='复制（带来源）';withSource.setAttribute('role','menuitem');
 copy.type=all.type='button';copy.textContent='复制';all.textContent='全选';
 copy.setAttribute('role','menuitem');all.setAttribute('role','menuitem');menu.append(copy,withSource,all);document.body.append(menu);
 let selected='',sourced='';
 const close=(focus=false)=>{menu.hidden=true;selected='';if(focus)term.focus();};
 const notice=text=>{const el=document.getElementById('status');if(el)el.textContent=text;};
 const legacyCopy=text=>{
  const input=document.createElement('textarea');input.value=text;input.readOnly=true;input.style.cssText='position:fixed;left:-9999px;top:0';document.body.append(input);
  try{input.select();if(!document.execCommand('copy'))throw Error('copy unavailable');}finally{input.remove();}
 };
 const copyText=async(text)=>{if(!text)return;close();
  try{
   if(navigator.clipboard?.writeText){try{await navigator.clipboard.writeText(text);}catch(_){legacyCopy(text);}}
   else legacyCopy(text);
   notice('已复制选中的终端文本。');
  }catch(_){notice('复制失败，请使用 Ctrl+Shift+C 或浏览器复制功能。');}
  if(!events.signal.aborted)term.focus();
 };
 copy.onclick=()=>copyText(selected);withSource.onclick=()=>copyText(sourced);
 all.onclick=()=>{term.selectAll();close(true);};
 // Preserve xterm's existing selection when opening the menu. Never send a
 // right mouse button event to a remote application in mouse tracking mode.
 for(const type of ['pointerdown','mousedown'])term.element.addEventListener(type,event=>{if(event.button===2){event.preventDefault();event.stopImmediatePropagation();}},options);
 term.element.addEventListener('contextmenu',event=>{
  event.preventDefault();event.stopImmediatePropagation();selected=term.getSelection();copy.disabled=!selected;sourced=typeof transcript!=='undefined'&&transcript?.term===term?transcript.selectionWithSources():'';withSource.hidden=!sourced;
  menu.hidden=false;menu.style.left='0px';menu.style.top='0px';
  const box=term.element.getBoundingClientRect(),x=event.clientX||box.left+24,y=event.clientY||box.top+24;
  menu.style.left=`${Math.max(4,Math.min(x,window.innerWidth-menu.offsetWidth-4))}px`;
  menu.style.top=`${Math.max(4,Math.min(y,window.innerHeight-menu.offsetHeight-4))}px`;
  (copy.disabled?all:copy).focus();
 },options);
 document.addEventListener('pointerdown',event=>{if(!menu.hidden&&!menu.contains(event.target))close();},options);
 document.addEventListener('keydown',event=>{
  if(menu.hidden)return;
  if(event.key==='Escape'){event.preventDefault();event.stopImmediatePropagation();close(true);}
  else if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();event.stopImmediatePropagation();const items=[copy,withSource,all].filter(b=>!b.hidden).filter(b=>!b.disabled);items[(items.indexOf(document.activeElement)+1)%items.length].focus();}
  else if(event.key==='Tab')close();
 },options);
 for(const type of ['resize','blur'])window.addEventListener(type,()=>close(),{signal:events.signal});
 document.addEventListener('scroll',()=>close(),options);
 term.register({dispose:()=>{events.abort();menu.remove();}});
}
