'use strict';
const assert=require('node:assert/strict');

module.exports=async function assertVisibleQualityEntry(button){
 const status=await button.evaluate(element=>{
  const style=getComputedStyle(element),box=element.getBoundingClientRect();
  const readableText=parseFloat(style.fontSize)>=10&&element.textContent.trim().length>0;
  const visibleGraphic=[...element.querySelectorAll('svg,img')].some(graphic=>{
   const r=graphic.getBoundingClientRect(),s=getComputedStyle(graphic);
   return r.width>=8&&r.height>=8&&s.display!=='none'&&s.visibility!=='hidden'&&parseFloat(s.opacity)>0;
  });
  return {width:box.width,height:box.height,font_size:style.fontSize,readable_text:readableText,visible_graphic:visibleGraphic};
 });
 assert(status.width>0&&status.height>0&&(status.readable_text||status.visible_graphic),'Network quality entry has no visible label or icon');
 return status;
};
