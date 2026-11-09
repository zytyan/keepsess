const assert=require('node:assert/strict');
exports.install=page=>page.evaluate(()=>{
 window.keywordPaintReady=term=>{
  const row=term.element.querySelector('.xterm-rows')?.firstElementChild;if(!row?.textContent.startsWith('ERROR'))return false;
  const walker=document.createTreeWalker(row,NodeFilter.SHOW_TEXT),colors=[];let node;
  while((node=walker.nextNode())&&colors.length<5)colors.push(...[...node.textContent].map(()=>getComputedStyle(node.parentElement).color));
  return colors.slice(0,5).length===5&&colors.slice(0,5).every(c=>c==='rgb(255, 133, 133)');
 };
});
// Verify actual painted pixels as well as render-time DOM styles.
exports.check=async(page,expression='terminal',{x=0,y=0,width=5,color=[255,133,133]}={})=>{
 const clip=await page.evaluate(({expression,x,y,width})=>{
  const term=eval(expression),b=term.element.querySelector('.xterm-screen').getBoundingClientRect();
  return {x:Math.floor(b.x+b.width/term.cols*x),y:Math.floor(b.y+b.height/term.rows*y),width:Math.ceil(b.width/term.cols*width),height:Math.ceil(b.height/term.rows)};
 },{expression,x,y,width});
 const png=(await page.screenshot({clip})).toString('base64');
 const count=await page.evaluate(async({png,color})=>{
  const bytes=Uint8Array.from(atob(png),ch=>ch.charCodeAt(0));
  const img=await createImageBitmap(new Blob([bytes],{type:'image/png'}));
  const canvas=document.createElement('canvas');canvas.width=img.width;canvas.height=img.height;
  const context=canvas.getContext('2d',{willReadFrequently:true});context.drawImage(img,0,0);
  const data=context.getImageData(0,0,img.width,img.height).data;img.close();let red=0;
  for(let i=0;i<data.length;i+=4)if(color.every((v,c)=>Math.abs(data[i+c]-v)<20))red++;
  return red;
 },{png,color});
 assert(count>3,`foreground ${color} was not painted: ${count} matching pixels`);
};
