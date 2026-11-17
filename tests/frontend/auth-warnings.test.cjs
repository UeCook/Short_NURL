const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const test = require("node:test");

function loadRefresh(fetch) {
  const calls = [];
  const renders = [];
  const input = { value: "su_test" };
  const source = fs.readFileSync(path.join(__dirname, "../../frontend/app.js"), "utf8");
  // 仅在测试上下文中导出管理刷新入口，页面初始化和渲染无需真实浏览器。
  const instrumented = source.replace(/\}\)\(\);\s*$/, `
    renderStat = function(data) { renders.push(["stat", data]); };
    renderList = function(perm, temp) { renders.push(["list", perm, temp]); };
    globalThis.refresh = refreshManageData;
  })();`);
  const context = {
    document: { readyState: "loading", addEventListener() {}, querySelector() { return input; } },
    fetch(url, options) { calls.push(url); return fetch(url, options); },
    renders,
  };
  vm.runInNewContext(instrumented, context);
  return { refresh: context.refresh, calls, renders, input };
}

function response(status, body) {
  return Promise.resolve({ ok: status >= 200 && status < 300, status, text: () => Promise.resolve(JSON.stringify(body)) });
}

test("each wrong confirmation makes one request and preserves progressive warnings", async () => {
  let failures = 0;
  const fixture = loadRefresh(() => {
    failures++;
    return response(failures < 5 ? 401 : 429, {
      error: failures < 5 ? `第 ${failures} 次失败，还可尝试 ${5 - failures} 次` : "请求来源已封禁 15 分钟",
      code: failures < 5 ? "invalid_key" : "auth_blocked",
    });
  });
  for (let attempt = 1; attempt <= 5; attempt++) {
    await assert.rejects(fixture.refresh(), error => {
      assert.equal(error.status, attempt < 5 ? 401 : 429);
      assert.ok(error.message.includes(attempt < 5 ? `还可尝试 ${5 - attempt} 次` : "封禁 15 分钟"));
      return true;
    });
  }
  assert.equal(fixture.calls.length, 5);
  assert.ok(fixture.calls.every(url => url === "./api/list"));
});

test("statistics are requested only after successful list authentication", async () => {
  let finishList;
  const listResponse = new Promise(resolve => { finishList = resolve; });
  const fixture = loadRefresh(url => url.endsWith("/list") ? listResponse : response(200, { perm_count: 0 }));
  const pending = fixture.refresh();
  assert.deepEqual(fixture.calls, ["./api/list"]);
  finishList(await response(200, { permanent: [], temporary: [] }));
  await pending;
  assert.deepEqual(fixture.calls, ["./api/list", "./api/stat"]);
  assert.equal(fixture.renders[0][0], "list");
  assert.equal(fixture.renders[1][0], "stat");
});

test("a statistics failure remains an error and preserves the loaded list", async () => {
  const fixture = loadRefresh(url => url.endsWith("/list")
    ? response(200, { permanent: [{ id: "abcd" }], temporary: [] })
    : response(503, { error: "热存储暂不可用" }));
  await assert.rejects(fixture.refresh(), error => error.message === "热存储暂不可用");
  assert.deepEqual(fixture.calls, ["./api/list", "./api/stat"]);
  assert.equal(fixture.renders.filter(render => render[0] === "list").length, 1);
  assert.equal(fixture.renders.at(-1)[0], "stat");
  assert.equal(fixture.renders.at(-1)[1], null);
});

test("clearing the key invalidates a pending list request without requesting statistics", async () => {
  let finishList;
  const listResponse = new Promise(resolve => { finishList = resolve; });
  const fixture = loadRefresh(() => listResponse);
  const pending = fixture.refresh();

  fixture.input.value = "";
  await fixture.refresh();
  finishList(await response(200, { permanent: [{ id: "abcd" }], temporary: [] }));
  await pending;

  assert.deepEqual(fixture.calls, ["./api/list"]);
  assert.equal(fixture.renders.length, 2);
  assert.equal(fixture.renders[0][0], "stat");
  assert.equal(fixture.renders[0][1], null);
  assert.equal(fixture.renders[1][0], "list");
  assert.equal(fixture.renders[1][1], null);
  assert.equal(fixture.renders[1][2], null);
});

test("clearing the key invalidates a pending statistics response", async () => {
  let finishStat, startStat;
  const statisticsResponse = new Promise(resolve => { finishStat = resolve; });
  const statisticsStarted = new Promise(resolve => { startStat = resolve; });
  const fixture = loadRefresh(url => {
    if (url.endsWith("/list")) return response(200, { permanent: [{ id: "abcd" }], temporary: [] });
    startStat();
    return statisticsResponse;
  });
  const pending = fixture.refresh();
  await statisticsStarted;

  fixture.input.value = "";
  await fixture.refresh();
  const clearedRenderCount = fixture.renders.length;
  finishStat(await response(200, { perm_count: 1 }));
  await pending;

  assert.equal(fixture.renders.length, clearedRenderCount);
  assert.equal(fixture.renders.at(-1)[0], "list");
  assert.equal(fixture.renders.at(-1)[1], null);
});
