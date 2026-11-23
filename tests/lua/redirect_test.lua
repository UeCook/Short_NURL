package.path = "backend/lua/?.lua;tests/lua/?.lua;" .. package.path
local helper = require "helper"
local config = require "shorturl.config"
local env = helper.new()
config.redirect_guard = {
    enabled = true, global_requests_per_second = 3,
    miss_window_seconds = 60, max_misses = 2, ban_seconds = 900,
}
helper.module("shorturl.redirect_guard")
local redirect = helper.module("shorturl.redirect")
ngx.shared.su_url:safe_set("abc", "https://example.com/valid", 0)

for i = 1, 3 do
    env:request("198.51.100." .. i, "ABC", i == 2 and "HEAD" or "GET")
    redirect.go()
    assert(env.response.status == 302 and env.response.location == "https://example.com/valid")
    assert(ngx.header["Cache-Control"] == "no-store")
end
local lookups = 0
ngx.shared.su_url.get_hook = function() lookups = lookups + 1 end
env:request("203.0.113.1", "abc")
redirect.go()
assert(env.response.status == 429 and ngx.header["Retry-After"] == "1")
assert(lookups == 0, "distributed admission must happen before knowing whether a code exists")
ngx.shared.su_url.get_hook = nil
env:advance(1)

env:request("203.0.113.2", "nope", "HEAD"); redirect.go()
assert(env.response.status == 404)
env:request("203.0.113.2", "gone"); redirect.go()
assert(env.response.status == 429)
env:request("203.0.113.2", "abc"); redirect.go()
assert(env.response.status == 429, "known codes cannot bypass a scan ban")

env:advance(1)
ngx.ctx.su_ready = false
env:request("203.0.113.3", "abc"); redirect.go()
assert(env.response.status == 503)
assert(not ngx.shared.su_blocklist:get("scan:m:203.0.113.3"))
ngx.ctx.su_ready = true
env:request("203.0.113.3", "abc", "POST"); redirect.go()
assert(env.response.status == 405 and ngx.header.Allow == "GET, HEAD")

print("PASS: redirect GET/HEAD handling, pre-lookup global limits, miss bans, readiness and cache headers")
