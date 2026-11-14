(function(root){
  'use strict';
  // Credentials live only in the current call's memory, never in browser storage.
  root.IntegrationDraft = Object.freeze({
    async run(read, send) {
      const before = JSON.stringify(read());
      const result = await send(JSON.parse(before));
      if (JSON.stringify(read()) !== before) throw new Error('配置已修改，请重新测试');
      return result;
    }
  });
})(globalThis);
