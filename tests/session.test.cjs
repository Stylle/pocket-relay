const {test}=require('node:test');
const assert=require('node:assert/strict');
const Session=require('../web/session.js');
function storage(){const m=new Map();return{getItem:k=>m.get(k)||null,setItem:(k,v)=>m.set(k,v),removeItem:k=>m.delete(k)}}
test('refresh restores only the tab token, not a new or restored navigation',()=>{
 const store=storage();const s=new Session(store,'navigate');s.accept('test-token',{idle_minutes:15});
 assert.equal(new Session(store,'reload').restore(),'test-token');
 assert.equal(new Session(store,'back_forward').restore(),null);
 assert.equal(store.getItem('pr-session'),null);
 s.accept('another-token',{idle_minutes:15});
 assert.equal(new Session(store,'navigate').restore(),null);
});
test('server clock and activity determine idle, background reads cannot extend it',()=>{
 let now=1000000;const s=new Session(storage(),'reload',()=>now);
 s.accept('token',{idle_minutes:15,last_active:0,server_time:14*60000});
 assert.equal(s.expired(),false);now+=60000;assert.equal(s.expired(),true);
 assert.equal(s.input(),false); // expired sessions cannot be revived by input
 s.accept('fresh',{idle_minutes:1});now+=10000;assert.equal(s.input(),true);now+=59999;assert.equal(s.expired(),false);now++;assert.equal(s.expired(),true);
 s.accept('unlimited',{idle_minutes:0});now+=365*86400000;assert.equal(s.expired(),false);
 s.clear();assert.equal(s.token,'');
});
test('blocked or malformed storage fails closed without breaking login',()=>{
 const blocked={getItem(){throw Error('blocked')},setItem(){throw Error('blocked')},removeItem(){throw Error('blocked')}};
 const s=new Session(blocked,'reload');assert.equal(s.restore(),null);s.accept('token',{idle_minutes:15});assert.equal(s.persist(),false);
 const store=storage();store.setItem('pr-session','invalid');assert.equal(new Session(store,'reload').restore(),null);
});
test('remembered login ignores idle but has a fixed absolute deadline',()=>{
 let now=100000;const s=new Session(storage(),'navigate',()=>now);
 s.accept('memory-token',{remembered:true,idle_minutes:0,expires_at:now+7*86400000,server_time:now});
 now+=6*86400000;assert.equal(s.expired(),false);
 now+=86400000;assert.equal(s.expired(),true);assert.equal(s.input(),false);
 s.clear();assert.equal(s.remembered,false);assert.equal(s.expiresAt,0);
});
