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
 const name=item.name||'未命名实例',time=instanceTime(item.started,now);
 node.classList.toggle('instance-live',!!item.current);
 if(!node.firstElementChild){node.replaceChildren(document.createElement('strong'),document.createElement('small'));}
 node.firstChild.textContent=name;
 node.lastChild.textContent=(item.current&&item.pid?`PID ${item.pid} · `:'')+time;
 node.title=instanceTime(item.started,now,true);
 node.setAttribute('aria-label',`${name}，${item.current?'活动实例':'归档，只读'}${item.current&&item.pid?'，PID '+item.pid:''}，${time}，启动于 ${node.title}`);
}
function renderInstancePicker(list, selected, onselect, now=new Date()){
 const picker=document.getElementById('instances'),options=document.getElementById('instance-options');
 const retained=new Set(list.map(i=>i.id));
 for(const [id,button] of instanceButtons){if(!retained.has(id)){button.remove();instanceButtons.delete(id);}}
 list.forEach((item,index)=>{
  let button=instanceButtons.get(item.id);
  if(!button){button=document.createElement('button');button.type='button';instanceButtons.set(item.id,button);}
  // Reuse DOM nodes so polling and minute updates preserve keyboard focus.
  if(options.children[index]!==button)options.insertBefore(button,options.children[index]||null);
  paintInstance(button,item,now);button.setAttribute('aria-current',String(item.id===selected));
  button.onclick=()=>{picker.open=false;document.getElementById('instance-selected').focus();if(item.id!==selected)onselect(item.id);};
 });
 const item=list.find(i=>i.id===selected),summary=document.getElementById('instance-selected');
 if(item)paintInstance(summary,item,now);
 else{summary.textContent='等待实例';summary.classList.remove('instance-live');summary.removeAttribute('title');summary.setAttribute('aria-label','选择实例');}
}
document.getElementById('instances').addEventListener('keydown',event=>{
 const picker=event.currentTarget,buttons=[...document.getElementById('instance-options').children];
 if(event.key==='Escape'&&picker.open){event.preventDefault();picker.open=false;document.getElementById('instance-selected').focus();return;}
 if(!['ArrowDown','ArrowUp','Home','End'].includes(event.key)||!buttons.length)return;
 event.preventDefault();picker.open=true;
 const index=buttons.indexOf(document.activeElement);
 const next=event.key==='Home'?0:event.key==='End'?buttons.length-1:event.key==='ArrowDown'?Math.min(index+1,buttons.length-1):index<0?buttons.length-1:Math.max(0,index-1);
 buttons[next].focus();
});
document.addEventListener('click',event=>{const picker=document.getElementById('instances');if(!picker.contains(event.target))picker.open=false;});
