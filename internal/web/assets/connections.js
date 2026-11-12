/* Private connection records never enter preferences, terminal buffers or history. */
'use strict';
(()=>{
 const dialog=$('connections-dialog'),list=$('connections-list'),detail=$('connection-detail'),message=$('connections-status'),more=$('connections-more');
 let revision=0,after='',request=null;
 const text=(tag,value,cls)=>{const e=document.createElement(tag);e.textContent=value;if(cls)e.className=cls;return e;};
 function invalidate(){revision++;request?.abort();request=null;detail.replaceChildren();}
 async function fetchConnection(endpoint,body,query={}){
  request?.abort();const controller=new AbortController();request=controller;
  const timer=setTimeout(()=>controller.abort(),10000),url=new URL('api/'+endpoint,root);
  for(const [k,v] of Object.entries(query))url.searchParams.set(k,v);
  try{const response=await fetch(url,{signal:controller.signal,...(body?{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}:{})});if(!response.ok)throw Error(await response.text());return await response.json();}
  finally{clearTimeout(timer);if(request===controller)request=null;}
 }
 const identity=r=>({instance:r.instance,session:r.session});
 function instanceLabel(id){const item=instanceItems.find(i=>i.id===id);return item?(item.human_name||item.name||'未命名实例')+' · '+(item.current?'活动'+(item.pid?' · PID '+item.pid:''):'归档')+' · '+id:id;}
 const date=value=>{const d=new Date(value);return Number.isNaN(+d)?'未知':d.toLocaleString();};
 function renderRecord(r){
  detail.replaceChildren();const version=revision;
  detail.append(text('h3',r.session+(r.name?' · '+r.name:'')));
  const fields=document.createElement('dl');
  function field(name,value){fields.append(text('dt',name),text('dd',value||'未记录'));}
  field('实例',instanceLabel(r.instance));field('协议 / 模式',r.protocol.toUpperCase()+' / '+r.mode);
  field('首次成功连接',date(r.first_connected));field('最近成功连接',date(r.last_connected));
  detail.append(fields);
  const passwords=[];
  function endpoint(title,endpoint){
   detail.append(text('h4',title));const info=document.createElement('dl');
   const add=(name,value)=>info.append(text('dt',name),text('dd',value||'未记录'));
   add('地址',endpoint.host+':'+endpoint.port);add('用户名',endpoint.username);
   add('认证方式',[endpoint.has_password?'密码':'',endpoint.keyfile?'密钥文件':''].filter(Boolean).join(' / ')||'未记录');
   if(endpoint.keyfile)add('密钥文件',endpoint.keyfile);
   if(r.protocol==='ssh'||title.startsWith('跳板'))add('主机校验',endpoint.insecure?'跳过（insecure=true）':endpoint.host_key?'指定主机密钥：'+endpoint.host_key:endpoint.known_hosts?'known_hosts：'+endpoint.known_hosts:'默认跳过');
   const password=text('dd',endpoint.has_password?'••••••••':'未记录');password.className='connection-password';
   info.append(text('dt','密码'),password);passwords.push({element:password,present:endpoint.has_password});detail.append(info);
  }
  endpoint('目标',r.target);(r.ssh_jumps||[]).forEach((j,i)=>endpoint('跳板 '+(i+1),j));
  const label=document.createElement('label'),show=document.createElement('input');show.type='checkbox';show.id='connection-show-passwords';label.className='connection-show';label.append(show,document.createTextNode('显示密码'));detail.append(label);
  const mask=()=>{for(const p of passwords)p.element.textContent=p.present?'••••••••':'未记录';};
  show.onchange=async()=>{
   mask();request?.abort();
   if(!show.checked)return;
   const attempt=++showAttempt;
   try{const secrets=await fetchConnection('connection-secrets',identity(r));if(version!==revision||attempt!==showAttempt||!show.checked||!dialog.open)return;
    const values=[secrets.password,...secrets.ssh_jumps];passwords.forEach((p,i)=>p.element.textContent=p.present?values[i]:'未记录');
   }catch(e){if(version===revision&&attempt===showAttempt&&show.checked){show.checked=false;mask();if(e.name!=='AbortError')message.textContent=e.message;}}
  };
  let showAttempt=0;
  if(r.protocol==='telnet'){
   detail.append(text('p',r.manual?'人工填写，未验证；仅保存资料，不发送至终端。':'可手动补录登录资料；不会从终端输入推断密码。','muted'));
   const form=document.createElement('form');form.id='connection-credentials-form';form.autocomplete='off';
   const user=document.createElement('input');user.id='connection-username';user.value=r.target.username||'';user.maxLength=4096;user.autocomplete='off';
   const userLabel=document.createElement('label');userLabel.className='field';userLabel.append(document.createTextNode('用户名'),user);
   const pass=document.createElement('input');pass.id='connection-password-edit';pass.type='password';pass.autocomplete='new-password';pass.maxLength=65536;
   const passLabel=document.createElement('label');passLabel.className='field';passLabel.append(document.createTextNode('新密码'),pass);
   const clear=document.createElement('input');clear.id='connection-password-clear';clear.type='checkbox';const clearLabel=document.createElement('label');clearLabel.append(clear,document.createTextNode('清空已保存密码'));clear.onchange=()=>{pass.disabled=clear.checked;if(clear.checked)pass.value='';};
   form.append(userLabel,passLabel,text('p','密码留空保留原值；勾选“清空已保存密码”可明确清空。','muted'),clearLabel);
   const save=text('button','保存资料');save.type='submit';form.append(save);detail.append(form);
   form.onsubmit=async event=>{
    event.preventDefault();show.checked=false;mask();showAttempt++;save.disabled=true;message.textContent='保存中…';
    try{
     const credentials={...identity(r),username:user.value};
     if(clear.checked||pass.value)credentials.password=clear.checked?'':pass.value;
     await fetchConnection('connection-credentials',credentials);
     pass.value='';delete credentials.password;
     if(version!==revision||!dialog.open)return;
     message.textContent='资料已保存，未发送至终端。';await select(r);
    }catch(e){if(version===revision&&e.name!=='AbortError')message.textContent=e.message;}
    finally{if(version===revision)save.disabled=false;}
   };
  }
 }
 async function select(r){
  invalidate();const version=revision;
  for(const b of list.children)b.classList.toggle('active',b.dataset.key===r.instance+'/'+r.session);
  detail.append(text('p','读取中…'));
  try{const record=await fetchConnection('connection',null,identity(r));if(version===revision&&dialog.open)renderRecord(record);}
  catch(e){if(version===revision&&e.name!=='AbortError'){detail.replaceChildren(text('p',e.message));}}
 }
 async function load(){
  invalidate();const version=revision;more.hidden=true;list.replaceChildren();message.textContent='读取中…';
  try{const page=await fetchConnection('connections',null,{after,limit:50});if(version!==revision||!dialog.open)return;
   after=page.next_cursor||'';more.hidden=!after;message.textContent=(page.warnings||[]).join('\n')||(!page.items.length?'尚无记录。旧实例的连接信息未记录；新连接成功后会保存。':'');
   for(const r of page.items){const button=document.createElement('button');button.type='button';button.className='connection-item';button.dataset.key=r.instance+'/'+r.session;
    button.append(text('strong',r.session+(r.name?' · '+r.name:'')),text('span',r.protocol.toUpperCase()+' · '+r.target.host+':'+r.target.port),text('small',instanceLabel(r.instance)),text('small','最近成功：'+date(r.last_connected)));button.onclick=()=>select(r);list.append(button);}
   if(page.items.length)await select(page.items[0]);
  }catch(e){if(version===revision&&e.name!=='AbortError')message.textContent=e.message;}
 }
 $('connections-open').onclick=()=>{after='';dialog.showModal();load();};
 $('connections-close').onclick=()=>{invalidate();dialog.close();};more.onclick=load;
 dialog.addEventListener('close',()=>{invalidate();list.replaceChildren();message.textContent='';after='';});
 dialog.addEventListener('cancel',()=>invalidate());
})();
