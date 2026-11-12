'use strict';
// Per-tab, reload-only continuity. Never put credentials in localStorage.
(function(root){
  class RelaySession {
    constructor(storage, navigation, clock=()=>Date.now()) {
      this.storage=storage; this.navigation=navigation; this.clock=clock;
      this.token=''; this.idleMinutes=15; this.lastActive=0;
      this.remembered=false; this.expiresAt=0;
    }
    restore() {
      let record;
      try { record=JSON.parse(this.storage.getItem('pr-session')||'null'); } catch {}
      this.clear();
      // Fresh navigation, reopening a tab and browser session restoration are
      // deliberately not accepted as a refresh. The server validates the token.
      if(this.navigation!=='reload'||!record||typeof record.token!=='string'||!record.token||record.token.length>256)return null;
      this.token=record.token;
      this.persist();
      return this.token;
    }
    accept(token, info) {
      this.token=token;
      this.remembered=info.remembered===true;
      this.expiresAt=Number.isFinite(info.expires_at)?this.clock()+info.expires_at-(Number.isFinite(info.server_time)?info.server_time:this.clock()):0;
      this.idleMinutes=Number.isInteger(info.idle_minutes)?info.idle_minutes:15;
      const elapsed=Number.isFinite(info.server_time)&&Number.isFinite(info.last_active)?Math.max(0,info.server_time-info.last_active):0;
      this.lastActive=this.clock()-elapsed;
      this.persist();
    }
    persist() {
      try { this.storage.setItem('pr-session',JSON.stringify({token:this.token})); return true; } catch { return false; }
    }
    expired() { return !!this.token&&((this.expiresAt>0&&this.clock()>=this.expiresAt)||(!this.remembered&&this.idleMinutes>0&&this.clock()-this.lastActive>=this.idleMinutes*60000)); }
    input() { if(!this.token||this.expired())return false; this.lastActive=this.clock(); return true; }
    clear() { this.token='';this.lastActive=0;this.remembered=false;this.expiresAt=0;try { this.storage.removeItem('pr-session'); } catch {} }
  }
  root.RelaySession=RelaySession;
  if(typeof module!=='undefined')module.exports=RelaySession;
})(typeof window==='undefined'?globalThis:window);
