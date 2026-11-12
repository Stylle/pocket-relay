const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
test('policy controls use explicit units and trusted interaction, not polling',()=>{
 const html=fs.readFileSync('web/index.html','utf8'),app=fs.readFileSync('web/app.js','utf8');
 assert.match(html,/id="idle-unit"/);assert.match(html,/value="days"/);assert.match(html,/value="never"/);
 const compact=app.replace(/\s+/g,'').replaceAll("'",'"');
 assert.ok(compact.includes('if(!event.isTrusted||document.hidden||!state.token)return'));
 assert.ok(compact.includes('session_idle_minutes:$("idle-unit").value==="never"?0:'));
});
