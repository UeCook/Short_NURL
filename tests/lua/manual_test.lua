package.path = "backend/lua/?.lua;tests/lua/?.lua;" .. package.path
local cjson = require "cjson.safe"
local helper = require "helper"
local time_util = require "shorturl.util.time"
local env = helper.new()
local dir = assert(os.getenv("NURL_LUA_TEST_TMP"), "NURL_LUA_TEST_TMP must name an empty test directory")
local perm_path, temp_path = dir .. "/perm.json", dir .. "/temp.json"
local options = { perm_path = perm_path, temp_path = temp_path,
    interval = 1, max_file_bytes = 32768, max_entries = 100 }
local manual = helper.module("shorturl.manual")

local function write_raw(path, raw)
    local f = assert(io.open(path, "wb")); assert(f:write(raw)); assert(f:close())
end
local function envelope(data, revision)
    return { v = 1, at = "2026-01-01T00:00:00+08:00", d = data, revision = revision }
end
local function write(path, root) write_raw(path, assert(cjson.encode(root))) end
local function entry(url, expires)
    return { url = "https://example.com/" .. url,
        t = expires and assert(time_util.format_iso8601(expires, "+08:00")) or nil }
end
local function reload()
    env:advance(2)
    return manual.reload(false)
end

-- Missing files never become empty databases; retrying is throttled.
assert(not manual.init(options))
assert(io.open(perm_path, "rb") == nil and io.open(temp_path, "rb") == nil)
local _, reason = manual.lookup("abc")
assert(reason == "unavailable")
assert(#env.timers == 1)
manual.init(options); assert(#env.timers == 1)

local perm = envelope({ abc = entry("old"), gone = entry("gone") })
local temp = envelope({ live = entry("live", env.now + 100) })
write(perm_path, perm); write(temp_path, temp)
assert(reload())
assert(manual.lookup("abc") == "https://example.com/old")
assert(manual.lookup("live") == "https://example.com/live")
assert(type(ngx.shared.su_manual:get("active")) == "number", "commit selector must be fixed-sized")

-- Same timestamp and byte length, changed URL, added code and removed code.
perm.d.abc = entry("new"); perm.d.gone = nil; perm.d.plus = entry("plus")
write(perm_path, perm); assert(reload())
assert(manual.lookup("abc") == "https://example.com/new")
assert(manual.lookup("gone") == nil and manual.lookup("plus") == "https://example.com/plus")

-- Expiry changes replace old metadata; expiration needs no timer or file writes.
temp.d.live = entry("live", env.now + 5)
write(temp_path, temp); assert(reload())
env:advance(3); assert(manual.lookup("live") == nil)
temp.d.live = entry("back", env.now + 50)
write(temp_path, temp); assert(reload())
assert(manual.lookup("live") == "https://example.com/back")

-- Every invalid candidate keeps the entire working snapshot.
local generation = ngx.shared.su_manual:get("active")
for _, bad in ipairs({
    "", "{broken", '{"v":2,"d":{}}', '{"v":1,"d":null}',
    '{"v":1,"d":[{"url":"https://example.com"}]}',
    '{"v":1,"d":{"UPPER":{"url":"https://example.com"}}}',
    '{"v":1,"d":{"api":{"url":"https://example.com"}}}',
    '{"v":1,"d":{"abc":{"url":"http://127.0.0.1"}}}',
    '{"v":1,"d":{"abc":{"id":"wrong","url":"https://example.com"}}}',
    '{"v":1,"d":{"abc":{"url":"https://example.com","t":"invalid"}}}',
}) do
    write_raw(perm_path, bad); assert(reload())
    assert(ngx.shared.su_manual:get("active") == generation)
    assert(manual.lookup("abc") == "https://example.com/new")
end
write(perm_path, perm)
local old_live = temp.d.live
temp.d.live = entry("broken"); write(temp_path, temp); assert(reload())
assert(ngx.shared.su_manual:get("active") == generation)
temp.d.live = old_live
temp.d.abc = entry("duplicate", env.now + 200)
write(temp_path, temp); assert(reload())
assert(ngx.shared.su_manual:get("active") == generation)
temp.d.abc = nil; write(temp_path, temp)
assert(os.remove(perm_path)); assert(reload())
assert(manual.lookup("abc") == "https://example.com/new")
write(perm_path, perm)

-- An optional shared revision makes a two-file publication all-or-nothing.
perm.revision, perm.d.abc = "release-2", entry("rev")
write(perm_path, perm); assert(reload())
assert(manual.lookup("abc") == "https://example.com/new")
temp.revision = "release-2"; write(temp_path, temp); assert(reload())
assert(manual.lookup("abc") == "https://example.com/rev")

-- Staging and metadata allocation failures cannot evict the active snapshot.
generation = ngx.shared.su_manual:get("active")
perm.d.abc = entry("oom"); write(perm_path, perm)
for _, prefix in ipairs({ "g:", "meta:" }) do
    ngx.shared.su_manual.fail = function(key) return key:sub(1, #prefix) == prefix end
    assert(reload())
    assert(ngx.shared.su_manual:get("active") == generation)
    assert(manual.lookup("abc") == "https://example.com/rev")
end
ngx.shared.su_manual.fail = nil
assert(reload()); assert(manual.lookup("abc") == "https://example.com/oom")

-- Recover a worker interrupted during staging, including after successful commit.
ngx.shared.su_manual:safe_set("index:999", '["lost"]', 0)
ngx.shared.su_manual:safe_set("g:999:lost", "0\nhttps://example.com/lost", 0)
ngx.shared.su_manual:safe_set("pending", "999", 0)
assert(reload()); assert(ngx.shared.su_manual:get("g:999:lost") == nil)
generation = ngx.shared.su_manual:get("active")
ngx.shared.su_manual:safe_set("pending", tostring(generation), 0)
assert(reload()); assert(manual.lookup("abc") == "https://example.com/oom")

-- Reloading Lua code re-registers its timer without clearing retained shared data.
manual = helper.module("shorturl.manual")
assert(manual.init(options)); assert(#env.timers == 2)
assert(manual.lookup("abc") == "https://example.com/oom")
assert(ngx.shared.su_manual:get("active") == generation)

-- A reader stalled across publication/retirement retries the new generation.
local dict = ngx.shared.su_manual
dict:safe_set("meta:1001", assert(cjson.encode({ generation = "1001",
    perm_path = perm_path, temp_path = temp_path })), 0)
dict:safe_set("g:1001:abc", "0\nhttps://example.com/race", 0)
dict.get_hook = function(key)
    if key == "g:" .. generation .. ":abc" then
        dict.get_hook = nil
        dict:delete(key)
        dict:safe_set("active", 1001, 0)
    end
end
assert(manual.lookup("abc") == "https://example.com/race")
assert(dict.unsafe_writes == 0)

-- Both the internal API and leftover managed timers are inert in manual mode.
ngx.var.su_mode = "manual"
ngx.shared.su_meta:safe_set("runtime_mode", "manual", 0)
local original_open = io.open
io.open = function() error("manual mode must not read internal_token or run managed I/O") end
local internal = helper.module("shorturl.internal")
internal.dispatch(); assert(env.response.status == 403)
require("shorturl.expire_sweep").run(false)
io.open = original_open

assert(os.remove(perm_path)); assert(os.remove(temp_path))
print("PASS: manual CRUD/TTL, strict validation, revisions, failed publication, restart recovery and read-only isolation")
