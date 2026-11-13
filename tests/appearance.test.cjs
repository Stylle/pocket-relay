const {test}=require("node:test");
const assert=require("node:assert/strict");
const fs=require("node:fs");
const vm=require("node:vm");
const path=require("node:path");
const root=path.resolve(__dirname,"..");
const base=fs.existsSync(path.join(root,"frontend/public/appearance.js"))?path.join(root,"frontend/public"):fs.existsSync(path.join(root,"web/public/appearance.js"))?path.join(root,"web/public"):path.join(root,"web");
const script=fs.readFileSync(base+"/appearance.js","utf8");
function fixture({saved=null,blocked=false,dark=false,id="movie"}={}){
 const listeners={},winListeners={},mediaListeners={}; let value=saved, writes=0;
 const root={dataset:{appId:id,defaultTheme:"green"}};
 const selects=[{value:""}],meta={content:""};
 const document={documentElement:root,readyState:"complete",
  querySelector:s=>s.startsWith("meta")?meta:null,
  querySelectorAll:s=>s==="[data-theme-select]"?selects:[],
  addEventListener:(name,fn)=>{listeners[name]=fn;}};
 const media={matches:dark,addEventListener:(name,fn)=>{mediaListeners[name]=fn;}};
 const window={matchMedia:query=>query.includes('prefers-color-scheme')?media:{matches:false,addEventListener:()=>{}},addEventListener:(name,fn)=>{winListeners[name]=fn;},dispatchEvent:()=>true};
 const localStorage={getItem:()=>{if(blocked)throw Error("blocked");return value;},setItem:(k,v)=>{if(blocked)throw Error("blocked");value=v;writes++;}};
 vm.runInNewContext(script,{document,window,localStorage,CustomEvent:function(name,options){this.type=name;this.detail=options.detail;}});
 return {root,selects,meta,media,listeners,winListeners,mediaListeners,get stored(){return value;},get writes(){return writes;}};
}
test("existing theme is restored without business requests or storage mutation",()=>{
 const f=fixture({saved:"dark"}); assert.equal(f.root.dataset.theme,"dark");assert.equal(f.selects[0].value,"dark");assert.equal(f.writes,0);
 assert.doesNotMatch(script,/fetch\s*\(|XMLHttpRequest|sendBeacon|innerHTML\s*=/);
});
test("all three themes persist and synchronize without remounting",()=>{
 const f=fixture(); for(const value of ["light","dark","green"]){f.listeners.change({target:{matches:()=>true,value}});assert.equal(f.stored,value);assert.equal(f.root.dataset.theme,value);}
 f.winListeners.storage({key:"trm_theme",newValue:"light"});assert.equal(f.root.dataset.theme,"light");assert.equal(f.writes,3);
});
test("system mode follows OS changes and invalid preferences safely fall back",()=>{
 const f=fixture({saved:"system",dark:true});assert.equal(f.root.dataset.theme,"dark");f.media.matches=false;f.mediaListeners.change();assert.equal(f.root.dataset.theme,"light");
 assert.equal(fixture({saved:"invalid"}).root.dataset.theme,"green");
});
test("blocked storage does not break rendering or theme selection",()=>{
 const f=fixture({blocked:true});f.listeners.change({target:{matches:()=>true,value:"dark"}});assert.equal(f.root.dataset.theme,"dark");
});
test("all theme palettes and copyright/about pages are shipped locally",()=>{
 const css=fs.readFileSync(base+"/design-system.css","utf8");
 assert.match(css,/data-theme=dark/);assert.match(css,/data-theme=green/);assert.match(css,/--theme-radius:0px/);
 const page=fs.readFileSync(base+"/about.html","utf8");assert.match(page,/MIT License/);assert.match(page,/viewport/);
 assert.doesNotMatch(page,/https?:\/\/[^" ]+\.js/);
});

