// Display timestamps in the browser's local time; IDs remain private routing keys.
function instanceTime(started, now=new Date(), absolute=false){
 const date=new Date(started);
 if(!Number.isFinite(date.getTime()))return '时间未知';
 const pad=n=>String(n).padStart(2,'0'),clock=`${pad(date.getHours())}:${pad(date.getMinutes())}`;
 const full=`${date.getFullYear()}年${pad(date.getMonth()+1)}月${pad(date.getDate())}日 ${clock}`;
 if(absolute)return full;
 const age=now-date;
 if(age>=0&&age<3600000)return age<60000?'刚刚':`${Math.floor(age/60000)}分钟前`;
 if(age<0)return full;
 if(date.toDateString()===now.toDateString())return clock;
 const day=`${pad(date.getMonth()+1)}月${pad(date.getDate())}日 ${clock}`;
 return date.getFullYear()===now.getFullYear()?day:full;
}
const instanceButtons=new Map();
function paintInstance(node, item, now){
 const name=item.human_name||item.name||'未命名实例',time=instanceTime(item.started,now);
 node.classList.toggle('instance-live',!!item.current);
 if(!node.firstElementChild){node.replaceChildren(document.createElement('strong'),document.createElement('small'),document.createElement('small'));}
 node.firstChild.textContent=name;
 const ai=node.children[1];ai.className='instance-ai-name';ai.textContent=item.name?'AI：'+item.name:'';ai.hidden=!item.name;
 node.lastChild.textContent=(item.current&&item.pid?`PID ${item.pid} · `:'')+time;
 node.title=instanceTime(item.started,now,true);
 node.setAttribute('aria-label',`${name}${item.name?'，AI：'+item.name:''}，${item.current?'活动实例':'归档，只读'}${item.current&&item.pid?'，PID '+item.pid:''}，${time}，启动于 ${node.title}`);
}
function renderInstancePicker(list, selected, onselect, now=new Date(), onrename=()=>{}){
 const picker=document.getElementById('instances'),options=document.getElementById('instance-options');
 const retained=new Set(list.map(i=>i.id));
 for(const [id,button] of instanceButtons){if(!retained.has(id)){button.parentElement.remove();instanceButtons.delete(id);}}
 list.forEach((item,index)=>{
  let button=instanceButtons.get(item.id);
  if(!button){
   const row=document.createElement('div');row.className='instance-option-row';
   button=document.createElement('button');button.type='button';button.className='instance-choice';
   const rename=document.createElement('button');rename.type='button';rename.className='instance-rename';
   rename.innerHTML='<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L9 17l-4 1 1-4Z"/></svg>';
   row.append(button,rename);instanceButtons.set(item.id,button);
  }
  // Reuse DOM nodes so polling and minute updates preserve keyboard focus.
  const row=button.parentElement;
  if(options.children[index]!==row)options.insertBefore(row,options.children[index]||null);
  const rename=row.querySelector('.instance-rename');rename.title='重命名实例';rename.setAttribute('aria-label','重命名实例：'+(item.human_name||item.name||'未命名实例'));rename.onclick=event=>{event.stopPropagation();onrename(item.id);};
  paintInstance(button,item,now);button.setAttribute('aria-current',String(item.id===selected));
  button.onclick=()=>{picker.open=false;document.getElementById('instance-selected').focus();if(item.id!==selected)onselect(item.id);};
 });
 const item=list.find(i=>i.id===selected),summary=document.getElementById('instance-selected');
 if(item)paintInstance(summary,item,now);
 else{summary.textContent='等待实例';summary.classList.remove('instance-live');summary.removeAttribute('title');summary.setAttribute('aria-label','选择实例');}
}
document.getElementById('instances').addEventListener('keydown',event=>{
 const picker=event.currentTarget,buttons=[...document.querySelectorAll('#instance-options .instance-choice')];
 if(event.key==='Escape'&&picker.open){event.preventDefault();picker.open=false;document.getElementById('instance-selected').focus();return;}
 if(!['ArrowDown','ArrowUp','Home','End'].includes(event.key)||!buttons.length)return;
 event.preventDefault();picker.open=true;
 const index=buttons.indexOf(document.activeElement);
 const next=event.key==='Home'?0:event.key==='End'?buttons.length-1:event.key==='ArrowDown'?Math.min(index+1,buttons.length-1):index<0?buttons.length-1:Math.max(0,index-1);
 buttons[next].focus();
});
document.addEventListener('click',event=>{const picker=document.getElementById('instances');if(!picker.contains(event.target))picker.open=false;});
