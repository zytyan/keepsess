/* Browser-only preferences. Never contains connection credentials or terminal data. */
'use strict';
const preferenceKey='keepsess.preferences.v1';
const preferenceDefaults={version:1,theme:'dark',fontSize:14,uiFontSize:13,scrollback:5000,sidebar:true,history:false,echo:true,filter:{sources:[],streams:['stdout','stderr','mixed','unknown'],include:'',exclude:'',human:true},appearance:null,search:{open:false,width:400,query:'',caseSensitive:false,only:false,before:0,after:0},gutterExpanded:false};
function validatePreferences(input){
 if(!input||input.version!==1)throw Error('不支持的设置版本');
 const d=structuredClone(preferenceDefaults),v=input;
 if(!['dark','light','system'].includes(v.theme)||!Number.isInteger(v.fontSize)||v.fontSize<10||v.fontSize>24||!Number.isInteger(v.scrollback)||v.scrollback<100||v.scrollback>50000)throw Error('主题、字号或滚动历史无效');
 for(const k of ['sidebar','history','echo'])if(typeof v[k]!=='boolean')throw Error('无效的界面设置');
 const uiFontSize=v.uiFontSize===undefined?d.uiFontSize:v.uiFontSize;if(!Number.isInteger(uiFontSize)||uiFontSize<11||uiFontSize>18)throw Error('界面字号须为 11–18px');
 const f=v.filter;if(!f||!Array.isArray(f.sources)||f.sources.length>128||f.sources.some(s=>typeof s!=='string'||s.length>128)||!Array.isArray(f.streams)||f.streams.some(s=>!d.filter.streams.includes(s))||typeof f.include!=='string'||typeof f.exclude!=='string'||f.include.length>512||f.exclude.length>512||typeof f.human!=='boolean')throw Error('过滤设置无效');
 const q=v.search===undefined?{...d.search,query:f.include,caseSensitive:!!(f.include||f.exclude),only:!!f.include||f.sources.length>0}:v.search;
 if(!q||typeof q.query!=='string'||q.query.length>512||!['open','caseSensitive','only'].every(k=>typeof q[k]==='boolean')||!Number.isInteger(q.width)||q.width<280||q.width>560||!['before','after'].every(k=>Number.isInteger(q[k])&&q[k]>=0&&q[k]<=50))throw Error('查找设置无效');
 if(v.gutterExpanded!==undefined&&typeof v.gutterExpanded!=='boolean')throw Error('来源栏设置无效');
 let a=null;
 if(v.appearance!==null){const x=v.appearance;if(!x||typeof x.enabled!=='boolean'||!Array.isArray(x.rules)||x.rules.length>32)throw Error('高亮设置无效');a={enabled:x.enabled,rules:x.rules.map(r=>{const out={};for(const k of ['name','pattern','foreground','background']){if(typeof r[k]!=='string'||new TextEncoder().encode(r[k]).length>(k==='pattern'?512:128))throw Error('无效高亮规则');out[k]=r[k];}if(!out.name||!out.pattern||(!out.foreground&&!out.background)||[out.foreground,out.background].some(c=>c&&!/^#[\da-f]{6}$/i.test(c)))throw Error('无效高亮颜色或表达式');for(const k of ['enabled','regex','case_sensitive','override_ansi']){if(typeof r[k]!=='boolean')throw Error('无效高亮选项');out[k]=r[k];}if(out.regex)new RegExp(out.pattern,'u');return out;})};}
 return {...d,search:{open:q.open,width:q.width,query:q.query,caseSensitive:q.caseSensitive,only:q.only,before:q.before,after:q.after},gutterExpanded:v.gutterExpanded??false,theme:v.theme,fontSize:v.fontSize,uiFontSize,scrollback:v.scrollback,sidebar:v.sidebar,history:v.history,echo:v.echo,filter:{sources:[...f.sources],streams:[...new Set(f.streams)],include:f.include,exclude:f.exclude,human:f.human},appearance:a};
}
let preferences=structuredClone(preferenceDefaults),preferenceError='';
try{
 const saved=localStorage.getItem(preferenceKey);
 if(saved)preferences=validatePreferences(JSON.parse(saved));
 else {const theme=localStorage.getItem('keepsess.theme'),n=Number(localStorage.getItem('keepsess.scrollback'));if(['dark','light','system'].includes(theme))preferences.theme=theme;if(Number.isInteger(n)&&n>=100&&n<=50000)preferences.scrollback=n;}
}catch(e){preferenceError='无法读取本地设置：'+e.message;}
function savePreferences(patch){
 const next=validatePreferences({...preferences,...patch});
 try{localStorage.setItem(preferenceKey,JSON.stringify(next));}catch(e){throw Error('浏览器无法保存设置：'+e.message);}
 preferences=next;window.dispatchEvent(new Event('preferenceschange'));
}
window.addEventListener('storage',event=>{if(event.key!==preferenceKey||!event.newValue)return;try{preferences=validatePreferences(JSON.parse(event.newValue));window.dispatchEvent(new Event('preferenceschange'));}catch(e){document.getElementById('status').textContent=e.message;}});
