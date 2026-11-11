'use strict';
let previousFilter=JSON.stringify(preferences.filter);
function renderSourceOptions(){
 const select=$('filter-sources'),value=preferences.filter.sources;select.replaceChildren();
 for(const t of sessionTasks())select.add(new Option(t.handle+' · '+(t.origin==='human'?'人类终端':t.command||t.name||'命令'),t.handle,false,value.includes(t.handle)));
 // Preserve portable filters for sources outside the current session.
 for(const id of value)if(![...select.options].some(o=>o.value===id))select.add(new Option(id+' · 当前未加载',id,false,true));
}
function syncWorkspace(){
 document.body.classList.toggle('sidebar-collapsed',!preferences.sidebar);
 $('history-drawer').hidden=humanPage||!preferences.history;
 $('sidebar-toggle').setAttribute('aria-expanded',String(preferences.sidebar));
 $('history-toggle').setAttribute('aria-expanded',String(preferences.history));
 $('scrollback').value=preferences.scrollback;scrollback=preferences.scrollback;
 $('font-size').value=preferences.fontSize;$('font-size-value').value=preferences.fontSize+'px';
 $('echo-preference').checked=$('echo').checked=preferences.echo;if(!preferences.echo)localEcho?.clear();
 for(const h of terminalHighlights){if(h.term.options.scrollback!==preferences.scrollback)h.term.options.scrollback=preferences.scrollback;if(h.term.options.fontSize!==preferences.fontSize){h.term.options.fontSize=preferences.fontSize;remeasureTerminalFonts(h.term);}}
 fit();themeMode=preferences.theme;applyTheme();
 if(preferences.appearance&&JSON.stringify(appearance)!==JSON.stringify(preferences.appearance)){applyAppearance(preferences.appearance);if($('appearance-dialog').open){appearanceOriginal=cloneAppearance(appearance);renderAppearanceForm(appearance);}}
 renderSourceOptions();for(const el of $('filter-streams').querySelectorAll('input'))el.checked=preferences.filter.streams.includes(el.value);
 $('filter-include').value=preferences.filter.include;$('filter-exclude').value=preferences.filter.exclude;$('filter-human').checked=preferences.filter.human;
 $('filter-toggle').classList.toggle('filter-active',JSON.stringify(preferences.filter)!==JSON.stringify(preferenceDefaults.filter));
 const next=JSON.stringify(preferences.filter);if(previousFilter!==next){previousFilter=next;if(continuous()&&selected)selectSession(selected).catch(e=>warn(e.message));}
}
function persist(patch){try{savePreferences(patch);}catch(e){status(e.message);$('highlight-status').textContent=e.message;}}
window.addEventListener('preferenceschange',syncWorkspace);
syncWorkspace();if(preferenceError)status(preferenceError);
$('sidebar-toggle').onclick=()=>persist({sidebar:!preferences.sidebar});
$('history-toggle').onclick=()=>persist({history:!preferences.history});$('history-close').onclick=()=>persist({history:false});
$('search-toggle').onclick=()=>{$('terminal-search').hidden=!$('terminal-search').hidden;if(!$('terminal-search').hidden)$('search-text').focus();};
$('filter-toggle').onclick=()=>{$('filter-panel').hidden=!$('filter-panel').hidden;};
$('filter-apply').onclick=()=>persist({filter:{sources:[...$('filter-sources').selectedOptions].map(o=>o.value),streams:[...$('filter-streams').querySelectorAll('input:checked')].map(o=>o.value),include:$('filter-include').value,exclude:$('filter-exclude').value,human:$('filter-human').checked}});
$('filter-reset').onclick=()=>persist({filter:structuredClone(preferenceDefaults.filter)});
$('font-size').oninput=()=>persist({fontSize:Number($('font-size').value)});
$('echo-preference').onchange=()=>persist({echo:$('echo-preference').checked});
$('follow-tail').onclick=()=>terminal?.scrollToBottom();
for(const tab of document.querySelectorAll('[data-settings]'))tab.onclick=()=>{for(const t of document.querySelectorAll('[data-settings]')){const active=t===tab;t.classList.toggle('active',active);t.setAttribute('aria-current',String(active));}for(const section of document.querySelectorAll('[data-settings-page]'))section.hidden=section.dataset.settingsPage!==tab.dataset.settings;};
$('preferences-export').onclick=()=>{
 try{const blob=new Blob([JSON.stringify(preferences,null,2)+'\n'],{type:'application/json'}),u=URL.createObjectURL(blob),a=document.createElement('a');a.href=u;a.download='keepsess-preferences.json';a.click();setTimeout(()=>URL.revokeObjectURL(u),1000);$('highlight-status').textContent='设置已导出。';}catch(e){$('highlight-status').textContent=e.message;}
};
$('preferences-import').onchange=async event=>{try{const file=event.target.files[0];if(!file)return;if(file.size>65536)throw Error('设置文件不能超过 64KiB');const next=validatePreferences(JSON.parse(await file.text()));if(next.appearance)await validateHighlightRules(next.appearance.rules);savePreferences(next);$('highlight-status').textContent='设置已导入并应用。';}catch(e){$('highlight-status').textContent='导入失败，原设置保留：'+e.message;}finally{event.target.value='';}};
$('preferences-reset').onclick=()=>{try{savePreferences({...structuredClone(preferenceDefaults),appearance:cloneAppearance(appearanceDefaults)});$('highlight-status').textContent='已恢复默认设置。';}catch(e){$('highlight-status').textContent=e.message;}};
$('open-terminal').onclick=()=>{if(!liveSession())return;const u=new URL(root);u.searchParams.set('view','terminal');u.searchParams.set('instance',instance);u.searchParams.set('session',selected);const candidate=sessionTasks().find(t=>t.origin==='human'&&t.status.state==='running');if(candidate)u.searchParams.set('handle',candidate.handle);window.open(u.href,'_blank','noopener');};
$('shell-start').onclick=()=>send({type:'web_shell'});
$('shell-existing').onchange=()=>{const target=$('shell-existing').value;if(target)selectSession(selected,target).catch(e=>warn(e.message));};
$('shell-end').onclick=()=>send({type:'web_end',handle});
function sendResize(){if(humanPage&&unlocked&&handle&&terminal&&webCaps.includes('web_resize.v1')){if(operationPending){clearTimeout(resizeTimer);resizeTimer=setTimeout(sendResize,150);return;}send({type:'web_resize',handle,columns:terminal.cols,rows:terminal.rows});}}
