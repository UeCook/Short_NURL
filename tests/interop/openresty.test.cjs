// Real HTTP/shared-dict/timer tests. No Go server, dependencies, or production data.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const http = require('node:http');
const { once } = require('node:events');
const { setTimeout: delay } = require('node:timers/promises');

const binary = process.env.OPENRESTY_BIN;
const root = path.resolve(__dirname, '../..');
const unix = value => value.replaceAll('\\', '/');
const iso = seconds => new Date(seconds * 1000).toISOString().replace(/\.\d{3}Z$/, '+00:00');
const envelope = (d, revision) => ({ v: 1, at: '2026-01-01T00:00:00+08:00', d, revision });
let peerSequence = 0;
const newPeer = () => `198.18.${(++peerSequence >> 8) % 256}.${peerSequence % 256}`;

async function port() {
  const server = net.createServer();
  await new Promise((resolve, reject) => server.once('error', reject).listen(0, '127.0.0.1', resolve));
  const value = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return value;
}

async function until(check, message, timeout = 7000) {
  const end = Date.now() + timeout;
  let last;
  while (Date.now() < end) {
    try { if (await check()) return; } catch (error) { last = error; }
    await delay(50);
  }
  assert.fail(`${message}${last ? `: ${last.message}` : ''}`);
}

async function start(mode, missing = false, sweepInterval = 3600) {
  const tempRoot = fs.realpathSync(os.tmpdir());
  const temp = fs.mkdtempSync(path.join(tempRoot, 'short-nurl-openresty-'));
  for (const dir of ['conf', 'logs', 'data', 'lua/shorturl']) fs.mkdirSync(path.join(temp, dir), { recursive: true });
  const permPath = path.join(temp, 'data/perm.json');
  const tempPath = path.join(temp, 'data/temp.json');
  const write = (name, value) => {
    const target = name === 'perm' ? permPath : tempPath;
    // The publisher owns these files; atomically replacing them is the operator contract.
    fs.writeFileSync(`${target}.next`, typeof value === 'string' ? value : JSON.stringify(value));
    fs.renameSync(`${target}.next`, target);
  };
  let perm = envelope({ abc: { url: 'https://example.com/old' }, gone: { url: 'https://example.com/gone' } });
  let temporary = envelope({ live: { url: 'https://example.com/live', t: iso(Math.floor(Date.now() / 1000) + 60) } });
  if (!missing) write('perm', perm);
  write('temp', temporary);
  fs.writeFileSync(path.join(temp, 'data/token'), 'local-test-token');
  fs.writeFileSync(path.join(temp, 'lua/shorturl/config.lua'), `return {
    mode = "${mode}", expire_interval = 3600, manual_reload_interval = 0.1,
    manual_max_file_bytes = 4 * 1024 * 1024, manual_max_entries = 2000,
    redirect_guard = { enabled = true, global_requests_per_second = 50,
      miss_window_seconds = 2, max_misses = 3, ban_seconds = 3 }
  }`);
  const listen = await port();
  const prefix = `${unix(temp)}/`;
  const workers = process.platform === 'win32' ? 1 : 2;
  const threaded = mode === 'manual' && process.platform === 'linux';
  const config = `worker_processes ${workers};
${threaded ? 'thread_pool shorturl_manual threads=1 max_queue=16;' : ''}
error_log logs/error.log notice;
pid logs/nginx.pid;
events { worker_connections 1024; }
http {
  access_log off;
  lua_package_path "${unix(temp)}/lua/?.lua;${unix(root)}/backend/lua/?.lua;${unix(path.dirname(binary))}/lualib/?.lua;;";
  lua_package_cpath "${unix(path.dirname(binary))}/lualib/?.so;;";
  lua_shared_dict su_manual 128k;
  lua_shared_dict su_meta 1m;
  lua_shared_dict su_url 1m;
  lua_shared_dict su_exp 1m;
  lua_shared_dict su_blocklist 1m;
  ${mode === 'manual' ? `init_worker_by_lua_block {
    local options = require("shorturl.runtime").manual_options()
    options.perm_path = "${unix(permPath)}"
    options.temp_path = "${unix(tempPath)}"
    ${threaded ? 'options.thread_pool = "shorturl_manual"' : ''}
    require("shorturl.manual").start(options)
  }` : ''}
  limit_req_zone $binary_remote_addr zone=redirect_burst:1m rate=2r/s;
  server {
    listen 127.0.0.1:${listen};
    server_name localhost;
    # Test harness only: all connections originate on loopback.
    set_real_ip_from 127.0.0.1;
    real_ip_header X-Test-Peer;
    set $su_mode "${mode}";
    set $su_perm_path "${unix(permPath)}";
    set $su_temp_path "${unix(tempPath)}";
    set $su_internal_token_path "${unix(temp)}/data/token";
    set $su_tz_offset "+08:00";
    set $su_expire_interval ${sweepInterval};
    set $su_reload_interval 0.1;
    set $su_redirect_code 302;
    access_by_lua_block { require("shorturl.init").init() }
    location = /healthz {
      content_by_lua_block {
        ngx.status = ngx.ctx.su_ready and 200 or 503
        ngx.say(ngx.ctx.su_ready and "ready" or "not ready")
      }
    }
    location = /clock { content_by_lua_block { ngx.say(ngx.time()) } }
    location ^~ /api/ { return 404; }
    location ^~ /headless/ { return 404; }
    location ^~ /internal/ { ${mode === 'managed'
      ? 'content_by_lua_block { require("shorturl.internal").dispatch() }' : 'return 404;'} }
    location ~* "^/([0-9a-z-]{1,5})$" {
      limit_req zone=redirect_burst burst=10 nodelay;
      limit_req_status 429;
      limit_except GET { deny all; }
      content_by_lua_block { require("shorturl.redirect").go() }
    }
    location / { return 404; }
  }
}`;
  fs.writeFileSync(path.join(temp, 'conf/nginx.conf'), config);
  const run = args => spawnSync(binary, ['-p', prefix, '-c', 'conf/nginx.conf', ...args], {
    cwd: path.dirname(binary), encoding: 'utf8', timeout: 10000, windowsHide: true,
  });
  const checked = run(['-t']);
  assert.equal(checked.status, 0, checked.stderr || checked.error?.message);
  const processHandle = spawn(binary, ['-p', prefix, '-c', 'conf/nginx.conf', '-g', 'daemon off;'], {
    cwd: path.dirname(binary), stdio: 'ignore', windowsHide: true,
  });
  let launchError;
  processHandle.on('error', error => { launchError = error; });
  const log = () => fs.readFileSync(path.join(temp, 'logs/error.log'), 'utf8');
  const request = (url, { peer = newPeer(), method = 'GET', headers = {}, body } = {}) => new Promise((resolve, reject) => {
    const req = http.request({ hostname: '127.0.0.1', port: listen, path: url, method,
      headers: { 'X-Test-Peer': peer, ...headers }, agent: false }, res => {
      let data = '';
      res.setEncoding('utf8'); res.on('data', chunk => { data += chunk; });
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: data }));
    });
    req.on('error', reject);
    req.setTimeout(3000, () => req.destroy(new Error('HTTP timeout')));
    req.end(body === undefined ? undefined : JSON.stringify(body));
  });
  const close = async () => {
    run(['-s', 'quit']);
    try { await until(() => processHandle.exitCode !== null, 'nginx did not quit', 5000); }
    catch { run(['-s', 'stop']); await until(() => processHandle.exitCode !== null, 'nginx did not stop', 3000); }
    assert.equal(path.dirname(fs.realpathSync(temp)), tempRoot);
    assert.ok(path.basename(temp).startsWith('short-nurl-openresty-'));
    fs.rmSync(temp, { recursive: true });
  };
  try {
    await until(async () => {
      if (launchError) throw launchError;
      if (processHandle.exitCode !== null) throw new Error(log());
      return [200, 503].includes((await request('/healthz')).status);
    }, 'OpenResty did not start');
  } catch (error) { await close(); throw error; }
  return { temp, prefix, permPath, tempPath, perm, temporary, write, request, log, run, close, workers };
}

test('shipped manual configuration and managed Nginx examples pass nginx -t', {
  skip: !binary && 'Set OPENRESTY_BIN to an OpenResty nginx executable',
}, async () => {
  const tempRoot = fs.realpathSync(os.tmpdir());
  const temp = fs.mkdtempSync(path.join(tempRoot, 'short-nurl-openresty-'));
  fs.mkdirSync(path.join(temp, 'logs'));
  try {
    const replace = source => source.replaceAll('{部署目录}', unix(root)).replaceAll('{你的域名}', 'short.example.com');
    const listen = 'listen 127.0.0.1:' + await port() + ';';
    const manualTemplate = replace(fs.readFileSync(path.join(root, 'deploy/nginx/manual.conf.example'), 'utf8'))
      .replace('listen 80;', listen);
    const manual = process.platform === 'linux' ? manualTemplate : manualTemplate
      .replace(/^thread_pool .*\r?\n/m, '').replace(/^\s*options\.thread_pool = .*\r?\n/m, '\n');
    const snippets = [...fs.readFileSync(path.join(root, 'deploy/nginx/nginx.md'), 'utf8')
      .matchAll(/```nginx[^\n]*\n([\s\S]*?)```/g)].map(match => replace(match[1]));
    assert.equal(snippets.length, 2);
    const managed = `events {}\nhttp {\n${snippets[0]}\nserver { ${listen}\n${snippets[1]}\n}\n}`;
    for (const [name, config] of [['manual', manual], ['managed', managed]]) {
      const file = path.join(temp, `${name}.conf`);
      fs.writeFileSync(file, config);
      const checked = spawnSync(binary, ['-p', `${unix(temp)}/`, '-c', unix(file), '-t'], {
        cwd: path.dirname(binary), encoding: 'utf8', timeout: 10000, windowsHide: true,
      });
      assert.equal(checked.status, 0, `${name}: ${checked.error || ''}\n${checked.stderr}`);
    }
  } finally {
    assert.equal(path.dirname(fs.realpathSync(temp)), tempRoot);
    assert.ok(path.basename(temp).startsWith('short-nurl-openresty-'));
    fs.rmSync(temp, { recursive: true });
  }
});

test('real OpenResty: manual publication, failure recovery, reload and scan protection', {
  skip: !binary && 'Set OPENRESTY_BIN to an OpenResty nginx executable', timeout: 90000,
}, async t => {
  const app = await start('manual', true);
  try {
    const { write, request } = app;
    assert.equal((await request('/abc')).status, 503);
    assert.equal(fs.existsSync(app.permPath), false, 'Lua must not create a missing JSON file');
    write('perm', app.perm);
    await until(async () => (await request('/abc')).headers.location === 'https://example.com/old', 'cold-load valid files');

    app.perm.d.abc.url = 'https://example.com/new'; // same byte length and unchanged at
    delete app.perm.d.gone;
    app.perm.d.plus = { url: 'https://example.com/plus' };
    write('perm', app.perm);
    await until(async () => (await request('/abc')).headers.location === 'https://example.com/new', 'apply equal-length URL edit');
    assert.equal((await request('/gone')).status, 404);
    let result = await request('/plus');
    assert.equal(result.headers.location, 'https://example.com/plus');
    assert.equal(result.headers['cache-control'], 'no-store');

    app.temporary.d.live = { url: 'https://example.com/timer', t: iso(Math.floor(Date.now() / 1000) + 4) };
    write('temp', app.temporary);
    await until(async () => (await request('/live')).headers.location === 'https://example.com/timer', 'apply changed expiry');
    await until(async () => (await request('/live')).status === 404, 'temporary URL expires without another publication');
    app.temporary.d.live = { url: 'https://example.com/back', t: iso(Math.floor(Date.now() / 1000) + 60) };
    write('temp', app.temporary);
    await until(async () => (await request('/live')).headers.location === 'https://example.com/back', 'extend expired temporary URL');

    // Two-file migration must never publish only half the release.
    app.perm.revision = 'release-2'; delete app.perm.d.abc;
    write('perm', app.perm);
    await until(() => app.log().includes('revision mismatch'), 'revision mismatch should be reported');
    assert.equal((await request('/abc')).headers.location, 'https://example.com/new');
    app.temporary.revision = 'release-2';
    app.temporary.d.abc = { url: 'https://example.com/moved', t: iso(Math.floor(Date.now() / 1000) + 60) };
    write('temp', app.temporary);
    await until(async () => (await request('/abc')).headers.location === 'https://example.com/moved', 'publish matching revisions');

    write('perm', '{broken');
    await until(() => app.log().includes('expected v=1'), 'corrupt JSON should be rejected');
    assert.equal((await request('/abc')).headers.location, 'https://example.com/moved');
    assert.equal((await request('/healthz')).status, 200);
    write('perm', app.perm);

    // Real shared-memory exhaustion while staging must retain the prior pointer/data.
    const large = {};
    for (let i = 0; i < 1000; i++) {
      large[`x${i.toString(36).padStart(3, '0')}`] = { url: `https://example.com/${'a'.repeat(1800)}/${i}` };
    }
    write('perm', envelope(large, 'release-2'));
    await until(() => app.log().includes('manual shared memory exhausted'), 'candidate should exceed real shared memory');
    assert.equal((await request('/abc')).headers.location, 'https://example.com/moved');
    write('perm', app.perm);

    // Observe publication through logs, without requests that could initialize new workers.
    const reloadLog = app.log().length;
    const reloaded = app.run(['-s', 'reload']);
    assert.equal(reloaded.status, 0, reloaded.stderr);
    if (process.platform === 'linux') {
      await until(() => (app.log().slice(reloadLog).match(/worker process \d+ exited with code 0/g) || []).length >= app.workers,
        'old workers exit after reload');
    }
    const publicationLog = app.log().length;
    app.temporary.d.abc.url = 'https://example.com/reloaded';
    write('temp', app.temporary);
    await until(() => app.log().slice(publicationLog).includes('manual snapshot published'), 'new timer publishes while idle');
    assert.equal((await request('/abc')).headers.location, 'https://example.com/reloaded');

    const scanningPeer = newPeer();
    for (const [i, method] of ['GET', 'HEAD', 'GET'].entries()) {
      result = await request(`/bad${i}`, { peer: scanningPeer, method });
      assert.equal(result.status, i === 2 ? 429 : 404);
    }
    assert.ok(Number(result.headers['retry-after']) > 0);
    const firstRetry = Number(result.headers['retry-after']);
    await delay(1100);
    result = await request('/abc', { peer: scanningPeer, headers: {
      'X-Real-IP': '203.0.113.1', 'X-Forwarded-For': '203.0.113.1',
    } });
    assert.equal(result.status, 429);
    assert.ok(Number(result.headers['retry-after']) < firstRetry, 'ban should not be extended by retries');
    await delay(2100);
    assert.equal((await request('/abc', { peer: scanningPeer })).status, 302);
    assert.equal((await request('/abc', { method: 'HEAD' })).status, 302);

    // Align to a fresh server second, then probe a valid URL from many IPs.
    const second = (await request('/clock')).body.trim();
    await until(async () => (await request('/clock')).body.trim() !== second, 'advance server second', 2500);
    const distributed = await Promise.all(Array.from({ length: 70 }, () => request('/abc')));
    const hits = distributed.filter(item => item.status === 302).length;
    assert.ok(hits > 0 && hits <= 50, `global budget admitted ${hits} hits`);
    assert.ok(distributed.some(item => item.status === 429), 'rotating sources must hit the global budget');
    assert.ok(distributed.every(item => [302, 429].includes(item.status)));
    await delay(1100);
    assert.equal((await request('/abc')).status, 302);

    // Native per-IP request limiting still covers repeated valid-code traffic.
    const burstPeer = newPeer();
    const burst = await Promise.all(Array.from({ length: 25 }, () => request('/abc', { peer: burstPeer })));
    assert.ok(burst.some(item => item.status === 302) && burst.some(item => item.status === 429));
    for (const url of ['/api/ping', '/headless/api/list', '/internal/stat', '/backend/data/perm.json']) {
      assert.equal((await request(url)).status, 404);
    }
    t.diagnostic('Validated real snapshots, expiry, allocation failure, HUP, GET/HEAD bans and cross-source limits without Go.');
  } catch (error) {
    t.diagnostic(app.log().slice(-14000));
    throw error;
  } finally { await app.close(); }
});

test('real OpenResty: managed cold load and authenticated hot updates stay compatible', {
  skip: !binary && 'Set OPENRESTY_BIN to an OpenResty nginx executable', timeout: 30000,
}, async t => {
  const app = await start('managed');
  try {
    assert.equal((await app.request('/abc')).headers.location, 'https://example.com/old');
    let result = await app.request('/internal/set', { method: 'POST', headers: {
      'Content-Type': 'application/json', 'LPA-Key': 'local-test-token',
    }, body: { code: 'abc', url: 'https://example.com/hot', ttl: 0, exp_str: '0' } });
    assert.equal(result.status, 200, result.body);
    await delay(300);
    assert.equal((await app.request('/abc')).headers.location, 'https://example.com/hot');
    assert.equal(JSON.parse(fs.readFileSync(app.permPath, 'utf8')).d.abc.url, 'https://example.com/old');
    result = await app.request('/internal/set', { method: 'POST', body: {} });
    assert.equal(result.status, 403);
    t.diagnostic('Managed mode keeps its mutation/hot-sync path; manual polling does not overwrite hot updates.');
  } catch (error) { t.diagnostic(app.log().slice(-14000)); throw error; }
  finally { await app.close(); }
});

test('real OpenResty: managed sweep skips a cold transaction still holding its write lock', {
  skip: (!binary || process.platform !== 'linux') && 'Requires OpenResty and Linux flock',
  timeout: 25000,
}, async t => {
  const app = await start('managed', false, 1);
  try {
    assert.equal((await app.request('/healthz')).status, 200);
    const lock = spawn('flock', ['-x', app.permPath + '.lock', 'sh', '-c',
      'echo ready; read -r release'], { stdio: ['pipe', 'pipe', 'inherit'] });
    const exited = once(lock, 'exit');
    try {
      const [ready] = await Promise.race([
        once(lock.stdout, 'data'),
        exited.then(([code]) => { throw new Error(`flock exited before locking: ${code}`); }),
      ]);
      assert.equal(ready.toString().trim(), 'ready');
      app.write('perm', envelope({
        ...app.perm.d,
        abc: { url: 'https://example.com/updated' },
        next: { url: 'https://example.com/committed' },
      }));
      await delay(2300); // At least one sweep has run while the writer still holds the lock.
      assert.equal((await app.request('/next')).status, 404);
      assert.equal((await app.request('/abc')).headers.location, 'https://example.com/old');
    } finally {
      lock.stdin.end('release\n');
      await exited;
    }
    await until(async () => {
      const result = await app.request('/next');
      if (result.status !== 302 || result.headers.location !== 'https://example.com/committed') return false;
      return (await app.request('/abc')).headers.location === 'https://example.com/updated';
    }, 'the committed redirects did not become visible');
  } catch (error) { t.diagnostic(app.log().slice(-14000)); throw error; }
  finally { await app.close(); }
});
