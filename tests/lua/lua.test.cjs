const { test } = require('node:test');
const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const root = path.resolve(__dirname, '../..');
const binary = process.env.LUA_BIN;
for (const file of ['urlguard_test.lua', 'mutation_test.lua', 'manual_test.lua',
  'redirect_guard_test.lua', 'redirect_test.lua']) {
  test(file, { skip: !binary && 'Set LUA_BIN to LuaJIT (with lua-cjson installed)' }, () => {
    const tempRoot = fs.realpathSync(os.tmpdir());
    const temp = fs.mkdtempSync(path.join(tempRoot, 'short-nurl-lua-'));
    try {
      if (file === 'manual_test.lua') fs.mkdirSync(path.join(temp, 'hashv'));
      const result = spawnSync(binary, [path.join('tests/lua', file)], {
        cwd: root, encoding: 'utf8', timeout: 30000, windowsHide: true,
        env: { ...process.env, NURL_LUA_TEST_TMP: temp },
      });
      assert.equal(result.status, 0, `${result.error || ''}\n${result.stdout}\n${result.stderr}`);
      process.stdout.write(result.stdout);
    } finally {
      // Only remove the unique directory created by this test.
      assert.equal(path.dirname(fs.realpathSync(temp)), tempRoot);
      assert.ok(path.basename(temp).startsWith('short-nurl-lua-'));
      fs.rmSync(temp, { recursive: true });
    }
  });
}
