-- init.lua - Cold start loading module
-- Loads JSON data files into lua_shared_dict on startup
-- Triggered by access_by_lua_block in site nginx config (first request per worker)
--
-- Architecture:
--   1. M.init_worker() — called from init_worker_by_lua_block (runs on every worker start/reload)
--      Reads config.lua, stores expire_interval in su_meta, registers ngx.timer.every.
--      This ensures the sweep timer survives nginx reloads (unlike access_by_lua which
--      is gated by inited_N and skipped on reload since su_meta persists).
--
--   2. M.init() — called from access_by_lua_block (lazy, first request per worker)
--      Reads nginx $su_* variables, loads JSON files into shared dicts.
--
-- NOTE: There is a brief window between flush_all and data loading completion
-- where incoming requests may get cache misses (404). This is an inherent
-- limitation of shared dict cold start — acceptable for a personal service.

local cjson = require "cjson.safe"
local time_util = require "shorturl.util.time"
local expire_sweep = require "shorturl.expire_sweep"
local su_config = require "shorturl.config"

local M = {}

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
    if not data or type(data.d) ~= "table" then
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

--- Worker initialization — called from init_worker_by_lua_block
-- Registers the periodic expire sweep timer. Runs on every worker start/reload.
-- Reads config.lua (ngx.var unavailable in init_worker context).
-- Timer registration is NOT gated by inited_N — it must run unconditionally
-- to survive nginx reloads (su_meta persists across reload, but timers do not).
function M.init_worker()
    local su_meta = ngx.shared.su_meta
    local expire_interval = tonumber(su_config.expire_interval) or 3600

    su_meta:set("expire_interval", expire_interval)

    ngx.timer.every(expire_interval, expire_sweep.run)

    ngx.log(ngx.NOTICE, "ShortURL: init_worker timer registered, interval=", expire_interval)
end

--- Data loading — called from access_by_lua_block (first request per worker)
-- Reads nginx $su_* variables, loads JSON files into shared dicts.
-- Uses shared lock to ensure only one worker executes data loading.
function M.init()
    local su_meta = ngx.shared.su_meta

    if su_meta:get("inited") then
        return true
    end

    local locked = su_meta:add("init_lock", 1, 30)
    if not locked then
        return false
    end

    local perm_path = ngx.var.su_perm_path or "/opt/shorturl/backend/data/perm.json"
    local temp_path = ngx.var.su_temp_path or "/opt/shorturl/backend/data/temp.json"
    local tz = ngx.var.su_tz_offset or "+08:00"
    local expire_interval = tonumber(ngx.var.su_expire_interval) or 3600

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
        if store_entry(code, entry.url, 0, "0") then
            perm_count = perm_count + 1
        end
    end

    for code, entry in pairs(temp_data.d) do
        local t = entry.t
        if t and t ~= "0" then
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
    end

    su_meta:set("perm_count", perm_count)
    su_meta:set("temp_count", temp_count)

    -- Overwrite expire_interval from nginx config (authoritative source)
    su_meta:set("expire_interval", expire_interval)

    su_meta:delete("lock_sweep")
    su_meta:delete("lock_perm")
    su_meta:delete("lock_temp")

    su_meta:set("inited", 1)
    su_meta:delete("init_lock")

    ngx.log(ngx.NOTICE, "ShortURL: cold start complete, perm=", perm_count, " temp=", temp_count)
    return true
end

return M
