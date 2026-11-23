const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const test = require("node:test");

function loadCopy(execCommand) {
  const toasts = [];
  const attached = new Set();
  const source = fs.readFileSync(path.join(__dirname, "../../frontend/app.js"), "utf8");
  const instrumented = source.replace(/\}\)\(\);\s*$/, [
    "showToast = function(message, type) { toasts.push({ message, type }); };",
    "globalThis.copy = copyText;",
    "})();",
  ].join("\n"));
  const context = {
    document: {
      readyState: "loading",
      addEventListener() {},
      createElement() { return { style: {}, select() {} }; },
      execCommand,
      body: {
        appendChild(element) { attached.add(element); },
        removeChild(element) { attached.delete(element); },
      },
    },
    navigator: {},
    toasts,
  };
  vm.runInNewContext(instrumented, context);
  return { copy: context.copy, toasts, attached };
}

test("clipboard fallback confirms a successful copy and removes its temporary element", () => {
  const fixture = loadCopy(command => {
    assert.equal(command, "copy");
    return true;
  });
  fixture.copy("http://127.0.0.1:38127/abcd");
  assert.equal(fixture.toasts.length, 1);
  assert.equal(fixture.toasts[0].message, "已复制到剪贴板");
  assert.equal(fixture.attached.size, 0);
});

for (const failure of ["false result", "exception"]) {
  test("clipboard fallback reports failure for " + failure + " and removes its temporary element", () => {
    const fixture = loadCopy(() => {
      if (failure === "exception") throw new Error("copy unavailable");
      return false;
    });
    fixture.copy("http://127.0.0.1:38127/abcd");
    assert.equal(fixture.toasts.length, 1);
    assert.equal(fixture.toasts[0].message, "复制失败，请手动复制");
    assert.equal(fixture.toasts[0].type, "error");
    assert.equal(fixture.attached.size, 0);
  });
}
