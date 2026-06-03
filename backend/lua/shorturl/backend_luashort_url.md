expire_sweep.lua
```
-- expire_sweep.lua - Periodic memory cleanup for expired temporary URLs
-- Scans su_exp shared dict (TTL=0, always reliable) for expired entries,
-- removes them from both su_exp and su_url, and decrements temp_count.
-- Does NOT touch JSON files (cold storage is maintained exclusively by PHP)
-- Triggered by ngx.timer.every in init.lua (worker 0 only)
--
-- Design note: su_exp entries are stored with TTL=0 (managed by sweep, not
-- native TTL). This ensures entries are always visible to sweep for accurate
-- count decrement. su_url still uses native TTL so redirects 404 immediately
-- on expiry, but su_url entries may disappear before sweep runs — that's fine.

local time_util = require "shorturl.util.time"

local M = {}

--- Run expire sweep (called by ngx.timer.every)
-- @param premature  boolean  true if timer is exiting prematurely
function M.run(premature)
    if premature then return end

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- Acquire sweep lock with TTL protection
    -- If the lock holder crashes, the TTL ensures automatic release after expire_interval seconds,
    -- preventing permanent deadlock. Without TTL, a worker panic would lock sweep until next restart.
    local lock = su_meta:get("lock_sweep")
    if tonumber(lock) == 1 then return end

    local expire_interval = tonumber(ngx.var.su_expire_interval) or 3600
    su_meta:set("lock_sweep", 1, expire_interval)

    local deleted = 0
    local ok, err = pcall(function()
        local tz = ngx.var.su_tz_offset or "+08:00"
        local now_str = time_util.now_iso8601(tz)

        -- Iterate su_exp (TTL=0, always reliable) instead of su_url
        -- su_url may have already been reclaimed by native TTL auto-expiry,
        -- but we still need to clean up su_exp and decrement temp_count.
        local keys = su_exp:get_keys(0)
        for _, code in ipairs(keys) do
            local exp_str = su_exp:get(code)
            if exp_str then
                if exp_str == "0" then
                    -- Permanent link, skip
                elseif exp_str < now_str then
                    -- Expired: delete from both dicts (su_url may already be gone)
                    su_url:delete(code)
                    su_exp:delete(code)
                    deleted = deleted + 1
                end
                -- else: not expired, skip
            end
        end
    end)

    if not ok then
        ngx.log(ngx.ERR, "ShortURL: expire_sweep error: ", err)
    end

    if deleted > 0 then
        -- Decrement temp_count
        local new_val, err = su_meta:incr("temp_count", -deleted)
        if not new_val then
            -- incr failed (key may not exist), initialize to 0
            su_meta:set("temp_count", 0)
        elseif new_val < 0 then
            -- Count went negative (drift), reset to 0
            su_meta:set("temp_count", 0)
        end
        ngx.log(ngx.NOTICE, "ShortURL: expire sweep removed ", deleted, " expired entries")
    end

    -- Release sweep lock
    su_meta:set("lock_sweep", 0)
end

return M

```

init.lua
```
-- init.lua - Cold start loading module
-- Loads JSON data files into lua_shared_dict on startup
-- Should only run in worker 0 via init_worker_by_lua_block
--
-- NOTE: There is a brief window between flush_all and data loading completion
-- where incoming requests may get cache misses (404). This is an inherent
-- limitation of shared dict cold start — acceptable for a personal service.

local cjson = require "cjson.safe"
local time_util = require "shorturl.util.time"
local expire_sweep = require "shorturl.expire_sweep"

local M = {}

--- Load a JSON file and return its d field (or empty table)
-- @param path  string  file path
-- @return table  the data object
local function load_json(path)
    local f = io.open(path, "r")
    if not f then
        -- File doesn't exist, create empty structure
        return { v = 1, at = "0", d = {} }
    end
    local content = f:read("*a")
    f:close()
    local data = cjson.decode(content)
    if not data or type(data.d) ~= "table" then
        return { v = 1, at = "0", d = {} }
    end
    return data
end

--- Create empty JSON structure file if it doesn't exist
-- @param path  string  file path
local function ensure_json(path, tz)
    local f = io.open(path, "r")
    if f then
        f:close()
        return
    end
    -- Create empty structure
    local empty = { v = 1, at = time_util.now_iso8601(tz), d = {} }
    local tmp = path .. ".lua.tmp"
    f = io.open(tmp, "w")
    if f then
        f:write(cjson.encode(empty))
        f:close()
        os.rename(tmp, path)
    end
end

--- Main cold start function
-- Reads perm.json and temp.json, loads entries into shared dicts
function M.init()
    -- Only worker 0 executes cold start
    if ngx.worker.id() ~= 0 then return end

    -- Load config from nginx variables
    local perm_path = ngx.var.su_perm_path or "/opt/shorturl/backend/data/perm.json"
    local temp_path = ngx.var.su_temp_path or "/opt/shorturl/backend/data/temp.json"
    local tz = ngx.var.su_tz_offset or "+08:00"
    local expire_interval = tonumber(ngx.var.su_expire_interval) or 3600

    -- Ensure JSON files exist
    ensure_json(perm_path, tz)
    ensure_json(temp_path, tz)

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- Clear all shared dicts
    -- NOTE: flush_all() marks all keys as expired; new writes will reuse
    -- the memory naturally, so flush_expired() is not needed here.
    -- su_meta MUST be flushed to clear stale lock flags (e.g. lock_sweep=1
    -- left over from a crashed worker), otherwise sweep could be permanently locked.
    su_url:flush_all()
    su_exp:flush_all()
    su_meta:flush_all()

    local now_str = time_util.now_iso8601(tz)
    local perm_count = 0
    local temp_count = 0

    -- Load permanent URLs
    local perm_data = load_json(perm_path)
    for code, entry in pairs(perm_data.d) do
        su_url:set(code, entry.url, 0)
        su_exp:set(code, "0", 0)
        perm_count = perm_count + 1
    end

    -- Load temporary URLs
    local temp_data = load_json(temp_path)
    for code, entry in pairs(temp_data.d) do
        local t = entry.t  -- ISO 8601 expiration string
        if t and t ~= "0" and t > now_str then
            -- Not expired: calculate remaining TTL in seconds
            local exp_epoch = time_util.parse_iso8601(t)
            if exp_epoch then
                local remaining = exp_epoch - ngx.time()
                if remaining > 0 then
                    -- su_url uses native TTL for auto-expiry
                    -- su_exp uses TTL=0 so expire_sweep can reliably find entries
                    su_url:set(code, entry.url, remaining)
                    su_exp:set(code, t, 0)
                    temp_count = temp_count + 1
                end
            end
        end
        -- Expired entries are discarded
    end

    -- Write counts to su_meta
    su_meta:set("perm_count", perm_count)
    su_meta:set("temp_count", temp_count)

    -- Initialize locks
    su_meta:set("lock_sweep", 0)
    su_meta:set("lock_perm", 0)
    su_meta:set("lock_temp", 0)

    -- Register periodic expire sweep
    ngx.timer.every(expire_interval, expire_sweep.run)

    ngx.log(ngx.NOTICE, "ShortURL: cold start complete, perm=", perm_count, " temp=", temp_count)
end

return M

```

internal.lua
```
-- internal.lua - Internal API route dispatcher
-- Routes /internal/set, /internal/delete, /internal/stat to handlers
-- Only accessible from allowed IPs (enforced by Nginx config)

local cjson = require "cjson.safe"
local M = {}

function M.dispatch()
    ngx.req.read_body()
    local uri = ngx.var.uri

    if uri:match("/internal/set$") then
        local internal_set = require "shorturl.internal_set"
        internal_set.handle()

    elseif uri:match("/internal/delete$") then
        local internal_delete = require "shorturl.internal_delete"
        internal_delete.handle()

    elseif uri:match("/internal/stat$") then
        local internal_stat = require "shorturl.internal_stat"
        internal_stat.handle()

    else
        ngx.status = 404
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"unknown internal endpoint"}')
        ngx.exit(404)
    end
end

return M

```

internal_delete.lua
```
-- internal_delete.lua - Delete from hot storage (su_url + su_exp shared dicts)
-- Called by PHP via POST /internal/delete
-- Also decrements perm_count / temp_count in su_meta for accurate tracking

local cjson = require "cjson.safe"

local M = {}

function M.handle()
    ngx.req.read_body()
    local body = ngx.req.get_body_data()

    if not body then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"empty body"}')
        ngx.exit(400)
        return
    end

    local params = cjson.decode(body)
    if not params or not params.code then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"missing code"}')
        ngx.exit(400)
        return
    end

    local code = params.code
    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- Check entry type before deletion to update the correct counter
    local exp_str = su_exp:get(code)
    if exp_str then
        -- Entry exists: decrement the appropriate counter
        local count_key
        if exp_str == "0" then
            count_key = "perm_count"
        else
            -- Any non-"0" value is treated as temporary (including corrupted data)
            count_key = "temp_count"
        end
        local new_val, err = su_meta:incr(count_key, -1)
        if not new_val then
            -- incr failed (key may not exist), initialize to 0 (entry already deleted)
            su_meta:set(count_key, 0)
        elseif new_val < 0 then
            -- Count went negative (drift), reset to 0
            su_meta:set(count_key, 0)
        end
    end

    su_url:delete(code)
    su_exp:delete(code)

    ngx.header["Content-Type"] = "application/json"
    ngx.say('{"ok":true}')
    ngx.exit(200)
end

return M

```

internal_set.lua
```
-- internal_set.lua - Write to hot storage (su_url + su_exp shared dicts)
-- Called by PHP via POST /internal/set
-- No business logic, pure cache write
-- Also maintains perm_count / temp_count in su_meta for new entries

local cjson = require "cjson.safe"

local M = {}

function M.handle()
    ngx.req.read_body()
    local body = ngx.req.get_body_data()

    if not body then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"empty body"}')
        ngx.exit(400)
        return
    end

    local params = cjson.decode(body)
    if not params or not params.code or not params.url then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"missing code or url"}')
        ngx.exit(400)
        return
    end

    local code = params.code
    local url = params.url
    local ttl = tonumber(params.ttl) or 0
    local exp_str = params.exp_str or "0"

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- Check if this is a new entry (not an overwrite) to update counts accurately
    local is_new = (su_url:get(code) == nil)

    -- Write to shared dicts
    -- su_url uses native TTL for auto-expiry (redirect 404 on expired links)
    -- su_exp uses TTL=0 (never auto-expire) so expire_sweep can always
    -- find the entry and decrement temp_count reliably. Without this,
    -- native TTL reclaims su_exp before sweep runs → count drifts.
    su_url:set(code, url, ttl)
    su_exp:set(code, exp_str, 0)

    -- Update counts for new entries only (overwrites don't change total count)
    if is_new then
        local count_key = (ttl == 0) and "perm_count" or "temp_count"
        local new_val, err = su_meta:incr(count_key, 1)
        if not new_val then
            -- incr failed (key may not exist after cold start), initialize it
            su_meta:set(count_key, 1)
        end
    end

    ngx.header["Content-Type"] = "application/json"
    ngx.say('{"ok":true}')
    ngx.exit(200)
end

return M

```

internal_stat.lua
```
-- internal_stat.lua - Read counts from su_meta shared dict
-- Called by PHP via GET /internal/stat
--
-- Count accuracy: counts are maintained by internal_set (incr on new), internal_delete (decr),
-- and expire_sweep (batch decr). The only source of drift is the window between native TTL
-- auto-expiry and the next expire_sweep run — at most expire_interval seconds.
-- Cold start always rebuilds exact counts from JSON files.

local cjson = require "cjson.safe"

local M = {}

function M.handle()
    local su_meta = ngx.shared.su_meta

    local perm_count = tonumber(su_meta:get("perm_count")) or 0
    local temp_count = tonumber(su_meta:get("temp_count")) or 0

    ngx.header["Content-Type"] = "application/json"
    ngx.say(cjson.encode({
        perm_count = perm_count,
        temp_count = temp_count
    }))
    ngx.exit(200)
end

return M
```

redirect.lua
```
-- redirect.lua - Short URL redirect handler
-- Looks up short code in su_url shared dict and performs 302 redirect
-- Read path: no disk I/O, pure memory lookup

local M = {}

function M.go()
    -- Get code from URI capture group
    local code = ngx.var[1]
    if not code or code == "" then
        ngx.exit(404)
        return
    end

    -- Convert to lowercase
    code = code:lower()

    local su_url = ngx.shared.su_url
    local target = su_url:get(code)

    if not target then
        ngx.redirect("/404.html")
        return
    end

    -- Perform 302 redirect
    ngx.redirect(target, 302)
end

return M
```

---

util\time.lua
```
-- time.lua - ISO 8601 formatting and comparison utilities
-- All timestamps use ISO 8601 extended format with timezone offset

local M = {}

--- Get the local system timezone offset in seconds (cached)
-- Uses os.date("!*t") vs os.date("*t") to compute the difference.
-- Cached after first call since timezone doesn't change during process lifetime.
-- @return number  offset in seconds (e.g. 28800 for UTC+8)
local function get_local_tz_offset()
    if M._tz_offset then return M._tz_offset end
    local now = os.time()
    local utc_t = os.date("!*t", now)
    local local_t = os.date("*t", now)
    utc_t.isdst = false
    local_t.isdst = false
    M._tz_offset = os.difftime(os.time(local_t), os.time(utc_t))
    return M._tz_offset
end

--- Format a unix timestamp to ISO 8601 string with timezone offset
-- @param ts   number  unix timestamp (seconds)
-- @param tz   string  timezone offset e.g. "+08:00"
-- @return     string  ISO 8601 string e.g. "2025-06-17T08:00:00+08:00"
function M.format_iso8601(ts, tz)
    tz = tz or "+08:00"

    -- Parse timezone offset to seconds
    local sign, hh, mm = tz:match("([+-])(%d%d):(%d%d)")
    if not sign then return "0" end
    local offset_sec = tonumber(hh) * 3600 + tonumber(mm) * 60
    if sign == "-" then offset_sec = -offset_sec end

    -- Apply offset to get local time
    local local_ts = ts + offset_sec
    local d = os.date("!%Y-%m-%dT%H:%M:%S", local_ts)

    return d .. tz
end

--- Get current time as ISO 8601 string
-- @param tz   string  timezone offset e.g. "+08:00"
-- @return     string  ISO 8601 string
function M.now_iso8601(tz)
    tz = tz or "+08:00"
    return M.format_iso8601(ngx.time(), tz)
end

--- Parse ISO 8601 string to unix timestamp (UTC epoch)
-- @param s    string  ISO 8601 string e.g. "2025-06-17T08:00:00+08:00"
-- @return     number  unix timestamp (UTC), or nil on failure
--
-- Note: os.time() interprets its argument as local time, not UTC.
-- We compensate by computing the local timezone offset and adjusting.
function M.parse_iso8601(s)
    if not s or s == "0" then return nil end

    local year, month, day, hour, min, sec, sign, oh, om =
        s:match("^(%d%d%d%d)%-(%d%d)%-(%d%d)T(%d%d):(%d%d):(%d%d)([+-])(%d%d):(%d%d)$")
    if not year then return nil end

    -- Build time table from the ISO string components
    local time_table = {
        year = tonumber(year), month = tonumber(month), day = tonumber(day),
        hour = tonumber(hour), min = tonumber(min), sec = tonumber(sec),
        isdst = false
    }
    -- os.time() interprets this as local time; compensate to get UTC epoch
    local epoch = os.time(time_table) + get_local_tz_offset()

    -- Apply the timezone offset from the ISO string to convert to UTC
    local offset = tonumber(oh) * 3600 + tonumber(om) * 60
    if sign == "+" then epoch = epoch - offset
    else epoch = epoch + offset end

    return epoch
end

return M

```