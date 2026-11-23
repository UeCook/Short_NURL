package.path = "backend/lua/?.lua;tests/lua/?.lua;" .. package.path

local cjson = require "cjson.safe"
local clock = require "shorturl.util.time"
local helper = require "helper"
local env = helper.new()
local sweep = helper.module("shorturl.expire_sweep")
local urls, expiry, meta = ngx.shared.su_url, ngx.shared.su_exp, ngx.shared.su_meta
local dir = assert(os.getenv("NURL_LUA_TEST_TMP"))
local perm, temp = dir .. "/perm.json", dir .. "/temp.json"
local future = clock.format_iso8601(env.now + 300, "+00:00")
local past = clock.format_iso8601(env.now - 1, "+00:00")

local function write(path, data)
    local file = assert(io.open(path, "wb"))
    assert(file:write(assert(cjson.encode({ d = data }))))
    assert(file:close())
end

meta:set("perm_path", perm)
meta:set("temp_path", temp)
write(perm, {
    drift = { url = "https://example.com/new" },
    same = { url = "https://example.com/same" },
    fresh = { url = "https://example.com/fresh" },
})
write(temp, {
    exp = { url = "https://example.com/expired", t = past },
    keep = { url = "https://example.com/keep", t = future },
})

urls:set("url", "https://example.com/orphan", 0)
expiry:set("exp-a", "0")
urls:set("drift", "https://example.com/old", 0)
expiry:set("drift", "0")
urls:set("exp", "https://example.com/expired", 0)
urls:set("keep", "https://example.com/keep", 0)
expiry:set("keep", "0")
urls:set("same", "https://example.com/same", 0)
expiry:set("same", "0")
urls:set("mut", "https://example.com/in-progress", 0)
meta:set("mutation:mut", 1, 120)

local before = urls.unsafe_writes
sweep.run(false)
assert(urls:get("url") == nil and expiry:get("exp-a") == nil)
assert(urls:get("exp") == nil)
assert(urls:get("drift") == "https://example.com/new")
assert(urls:get("fresh") == "https://example.com/fresh")
assert(expiry:get("keep") == future and urls:ttl("keep") == 300)
assert(urls:get("mut") == "https://example.com/in-progress")
assert(urls.unsafe_writes == before + 3, "avoid rewriting an unchanged entry")
assert(meta:get("perm_count") == 3 and meta:get("temp_count") == 1)

write(temp, false)
sweep.run(false)
assert(urls:get("keep") == "https://example.com/keep", "keep last hot data when cold snapshot is invalid")
print("PASS: managed reconciliation removes orphans and repairs drift, respecting active mutations")
