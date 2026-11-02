-- init.lua - Cold start loading module
-- Loads JSON data files into lua_shared_dict on startup
-- Triggered by access_by_lua_block in site nginx config (first request per worker)
--
-- Architecture:
--   1. M.init_worker() — registers the expire sweep timer (called from M.init())
--      Stores expire_interval in su_meta, registers ngx.timer.every (guarded by timer_registered).
--      Timer registration is lazy (on first request per worker) to avoid conflicting with
--      third-party init_worker_by_lua_file directives (e.g. WAF modules).
--
--   2. M.init() — called from access_by_lua_block (lazy, first request per worker)
--      Reads nginx $su_* variables, loads JSON files into shared dicts.
--
-- NOTE: There is a brief window between flush_all and data loading completion
-- where incoming requests may get cache misses (404). This is an inherent
-- limitation of shared dict cold start — acceptable for a personal service.

local cjson = require "cjson.safe"
local time_util = require "shorturl.util.time"
local url_util = require "shorturl.util.url"
local expire_sweep = require "shorturl.expire_sweep"
local su_config = require "shorturl.config"

local M = {}
-- Worker-local: reset on reload, unlike shared dict's persistent inited flag.
local timer_registered = false

--- Load a JSON file and return its d field (or empty table)
-- @param path  string  file path
-- @return table, string  the data object, or nil and error message
local function load_json(path)
    local f, open_err = io.open(path, "r")
    if not f then
        return nil, open_err or "open failed"
    end
    local content = f:read("*a")
    f:close()
    if not content or content == "" then
        return nil, "empty file"
    end
    local data, err = cjson.decode(content)
    if not data or type(data) ~= "table" or type(data.d) ~= "table" then
        return nil, err or "invalid structure"
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
    local empty = { v = 1, at = time_util.now_iso8601(tz), d = {} }
    local tmp = path .. ".lua.tmp"
    f = io.open(tmp, "w")
    if not f then
        ngx.log(ngx.ERR, "ShortURL: 无法创建数据文件: ", tmp)
        return
    end
    f:write(cjson.encode(empty))
    f:close()
    local ok, err = os.rename(tmp, path)
    if not ok then
        ngx.log(ngx.ERR, "ShortURL: 重命名失败: ", tmp, " -> ", path, " err: ", err)
        os.remove(tmp)
    end
end

--- Worker initialization — registers the expire sweep timer
-- Registers the periodic expire sweep timer. Called from M.init() on first request per worker.
-- Reads config.lua (ngx.var may be unavailable in some contexts).
-- Timer registration is NOT gated by inited_N — it must run on every worker
-- (timers do not survive reloads; timer_registered resets per worker).
function M.init_worker()
    if timer_registered then return true end

    local su_meta = ngx.shared.su_meta
    local expire_interval = tonumber(su_meta:get("expire_interval"))
        or tonumber(su_config.expire_interval) or 3600
    if expire_interval <= 0 then expire_interval = 3600 end

    su_meta:set("expire_interval", expire_interval)

    -- Any worker receiving traffic can start maintenance; sweep has a shared lock.
    -- No dependency on worker 0 receiving a request after reload.
    local ok, err = ngx.timer.every(expire_interval, expire_sweep.run)
    if not ok then
        ngx.log(ngx.ERR, "ShortURL: timer registration failed: ", err)
        return false -- next request retries
    end
    timer_registered = true

    ngx.log(ngx.NOTICE, "ShortURL: init_worker timer registered, interval=", expire_interval)
    return true
end

--- Data loading — called from access_by_lua_block (first request per worker)
-- Reads nginx $su_* variables, loads JSON files into shared dicts.
-- Uses shared lock to ensure only one worker executes data loading.
function M.init()
    local su_meta = ngx.shared.su_meta
    local perm_path = ngx.var.su_perm_path or "/opt/shorturl/backend/data/perm.json"
    local temp_path = ngx.var.su_temp_path or "/opt/shorturl/backend/data/temp.json"
    local tz = ngx.var.su_tz_offset or "+08:00"
    local expire_interval = tonumber(ngx.var.su_expire_interval) or 3600
    if expire_interval <= 0 then expire_interval = 3600 end

    -- Must run before the shared inited shortcut: timers disappear on reload.
    su_meta:set("expire_interval", expire_interval)
    M.init_worker()

    if su_meta:get("inited")
       and su_meta:get("perm_path") == perm_path
       and su_meta:get("temp_path") == temp_path then
        su_meta:set("expire_interval", expire_interval)
        return true
    end

    local locked = su_meta:add("init_lock", 1, 30)
    if not locked then
        return false
    end

    ensure_json(perm_path, tz)
    ensure_json(temp_path, tz)

    su_meta:set("perm_path", perm_path)
    su_meta:set("temp_path", temp_path)

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    local now_epoch = ngx.time()
    local perm_count = 0
    local temp_count = 0

    local perm_data, perm_err = load_json(perm_path)
    local temp_data, temp_err = load_json(temp_path)
    if not perm_data or not temp_data then
        ngx.log(ngx.ERR, "ShortURL: cold start aborted: ", perm_err or "", " ", temp_err or "")
        su_meta:delete("init_lock")
        return false
    end

    su_url:flush_all()
    su_exp:flush_all()

    local function store_entry(code, url, ttl, exp_str)
        if type(code) ~= "string" or type(url) ~= "string" or url == "" then
            return false
        end
        local url_ok, reason = url_util.check(url)
        if not url_ok then
            ngx.log(ngx.WARN, "ShortURL: skipping invalid URL for code=", code,
                    " reason=", tostring(reason))
            return false
        end
        local ok_url, err_url = su_url:set(code, url, ttl)
        if not ok_url then
            ngx.log(ngx.ERR, "ShortURL: su_url:set failed code=", code, " err=", tostring(err_url))
            return false
        end

        local ok_exp, err_exp = su_exp:set(code, exp_str, 0)
        if not ok_exp then
            su_url:delete(code)
            ngx.log(ngx.ERR, "ShortURL: su_exp:set failed code=", code, " err=", tostring(err_exp))
            return false
        end
        return true
    end

    for code, entry in pairs(perm_data.d) do
        if type(code) == "string" and type(entry) == "table" and type(entry.url) == "string" then
            if store_entry(code, entry.url, 0, "0") then
                perm_count = perm_count + 1
            end
        else
            ngx.log(ngx.WARN, "ShortURL: skipping malformed perm entry: ", tostring(code))
        end
    end

    for code, entry in pairs(temp_data.d) do
        if type(code) == "string" and type(entry) == "table" and type(entry.url) == "string" then
            local t = entry.t
            if type(t) == "string" and t ~= "0" then
                local exp_epoch = time_util.parse_iso8601(t)
                if exp_epoch then
                    local remaining = exp_epoch - now_epoch
                    if remaining > 0 then
                        if store_entry(code, entry.url, remaining, t) then
                            temp_count = temp_count + 1
                        end
                    end
                end
            end
        else
            ngx.log(ngx.WARN, "ShortURL: skipping malformed temp entry: ", tostring(code))
        end
    end

    su_meta:set("perm_count", perm_count)
    su_meta:set("temp_count", temp_count)

    -- Overwrite expire_interval from nginx config (authoritative source)
    su_meta:set("expire_interval", expire_interval)

    su_meta:set("inited", 1)
    su_meta:delete("init_lock")

    ngx.log(ngx.NOTICE, "ShortURL: cold start complete, perm=", perm_count, " temp=", temp_count)
    return true
end

return M
