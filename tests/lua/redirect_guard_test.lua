package.path = "backend/lua/?.lua;tests/lua/?.lua;" .. package.path
local helper = require "helper"
local config = require "shorturl.config"

local function setup(overrides)
    local env = helper.new()
    config.redirect_guard = {
        enabled = true, global_requests_per_second = 100,
        miss_window_seconds = 5, max_misses = 3, ban_seconds = 10,
    }
    for key, value in pairs(overrides or {}) do config.redirect_guard[key] = value end
    return env, helper.module("shorturl.redirect_guard")
end

-- Misses are shared across workers, count HEAD too, and successes do not reset them.
local env, guard = setup()
assert(guard.before()); assert(guard.miss())
local second_worker = helper.module("shorturl.redirect_guard")
assert(second_worker.before()); assert(second_worker.miss())
assert(guard.before()) -- a valid hit; no miss() call
local ok, status, retry = guard.miss()
assert(not ok and status == 429 and retry == 10)
local until_time = ngx.shared.su_blocklist:get("scan:b:198.51.100.1")
env:advance(2)
ok, status, retry = second_worker.before()
assert(not ok and status == 429 and retry == 8)
guard.miss() -- simulate a request already in flight when the ban was created
assert(ngx.shared.su_blocklist:get("scan:b:198.51.100.1") == until_time)
env:advance(8)
assert(guard.before(), "ban must expire without retries extending it")
assert(ngx.shared.su_blocklist.unsafe_writes == 0)

-- A global pre-lookup budget blocks even valid links from a fresh source.
env, guard = setup({ global_requests_per_second = 3 })
for i = 1, 3 do
    ngx.var.remote_addr = "198.51.100." .. i
    assert(guard.before())
end
ngx.var.remote_addr = "203.0.113.1"
ok, status, retry = guard.before()
assert(not ok and status == 429 and retry == 1)
env:advance(1)
assert(guard.before())

-- Forwarded headers do not change identity; an unrelated peer remains usable.
env, guard = setup({ max_misses = 1 })
assert(guard.before()); assert(not guard.miss())
ngx.var.http_x_forwarded_for = "203.0.113.99"
ngx.var.http_x_real_ip = "203.0.113.99"
assert(not guard.before())
ngx.var.remote_addr = "203.0.113.2"
assert(guard.before())

-- A successful lookup never erases the fixed miss window, but its expiry does.
env, guard = setup()
assert(guard.miss())
env:advance(4)
assert(guard.miss())
env:advance(1)
assert(guard.miss())
assert(ngx.shared.su_blocklist:get("scan:m:198.51.100.1") == 1)

-- No LRU eviction of active bans to admit new sources. Failure stays bounded.
env, guard = setup()
ngx.shared.su_blocklist.fail = function() return true end
ok, status = guard.before()
assert(not ok and status == 503)
guard.before(); guard.before()
assert(#env.logs == 1, "do not flood disk logs on every protection failure")
assert(ngx.shared.su_blocklist.unsafe_writes == 0)

-- Disabled protection needs no dictionary; invalid configuration fails closed.
env, guard = setup({ enabled = false })
ngx.shared.su_blocklist = nil
assert(guard.before()); assert(guard.miss())
env, guard = setup({ global_requests_per_second = 0 })
ok, status = guard.before()
assert(not ok and status == 503)

print("PASS: redirect guard sharing, distributed admission, bans, expiry, identity, capacity and configuration")
