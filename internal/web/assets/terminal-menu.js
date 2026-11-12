'use strict';
function attachTerminalContextMenu(term){
 const events=new AbortController(),options={capture:true,signal:events.signal};
 const menu=document.createElement('div');menu.className='terminal-context-menu';menu.hidden=true;menu.setAttribute('role','menu');menu.setAttribute('aria-label','终端操作');
 const copy=document.createElement('button'),all=document.createElement('button'),withSource=document.createElement('button'),find=document.createElement('button'),findSelection=document.createElement('button'),findTask=document.createElement('button');
 for(const [button,label]of [[find,'查找'],[findSelection,'查找所选内容'],[findTask,'在此任务内查找']]){button.type='button';button.textContent=label;button.setAttribute('role','menuitem');}
 withSource.type='button';withSource.textContent='复制（带来源）';withSource.setAttribute('role','menuitem');
 copy.type=all.type='button';copy.textContent='复制';all.textContent='全选';
 copy.setAttribute('role','menuitem');all.setAttribute('role','menuitem');menu.append(copy,withSource,all,find,findSelection,findTask);document.body.append(menu);
 let selected='',sourced='',taskTarget='';
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
 find.onclick=()=>{close();openSearch(true);};
 findSelection.onclick=()=>{const text=selected;close();openSearch(true,text);};
 findTask.onclick=()=>{const text=selected,target=taskTarget;close();openSearch(true,text||undefined,target);};
 // Preserve xterm's existing selection when opening the menu. Never send a
 // right mouse button event to a remote application in mouse tracking mode.
 for(const type of ['pointerdown','mousedown'])term.element.addEventListener(type,event=>{if(event.button===2){event.preventDefault();event.stopImmediatePropagation();}},options);
 term.element.addEventListener('contextmenu',event=>{
  event.preventDefault();event.stopImmediatePropagation();selected=term.getSelection();copy.disabled=!selected;sourced=typeof transcript!=='undefined'&&transcript?.term===term?transcript.selectionWithSources():'';withSource.hidden=!sourced;
  const screen=term.element.querySelector('.xterm-screen'),rect=screen?.getBoundingClientRect();const row=rect?term.buffer.active.viewportY+Math.max(0,Math.min(term.rows-1,Math.floor((event.clientY-rect.top)/(rect.height/term.rows)))):term.buffer.active.viewportY;
  taskTarget=typeof transcript!=='undefined'&&transcript?.term===term?transcript.spanAt(row)?.handle||'':typeof handle!=='undefined'&&terminalQuery?.term===term?handle:'';findSelection.disabled=!selected;findTask.disabled=!taskTarget;
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
  else if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();event.stopImmediatePropagation();const items=[copy,withSource,all,find,findSelection,findTask].filter(b=>!b.hidden).filter(b=>!b.disabled);items[(items.indexOf(document.activeElement)+1)%items.length].focus();}
  else if(event.key==='Tab')close();
 },options);
 for(const type of ['resize','blur'])window.addEventListener(type,()=>close(),{signal:events.signal});
 document.addEventListener('scroll',()=>close(),options);
 term.register({dispose:()=>{events.abort();menu.remove();}});
}
