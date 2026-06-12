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
-- @return table  the data object
local function load_json(path)
    local f = io.open(path, "r")
    if not f then
        return { v = 1, at = "0", d = {} }
    end
    local content = f:read("*a")
    f:close()
    if not content or content == "" then
        return { v = 1, at = "0", d = {} }
    end
    local data, err = cjson.decode(content)
    if not data or type(data.d) ~= "table" then
        ngx.log(ngx.WARN, "ShortURL: JSON decode failed for ", path, ": ", err or "unknown")
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
-- Only worker 0 executes data loading; other workers skip.
function M.init()
    if ngx.worker.id() ~= 0 then return end

    local perm_path = ngx.var.su_perm_path or "/opt/shorturl/backend/data/perm.json"
    local temp_path = ngx.var.su_temp_path or "/opt/shorturl/backend/data/temp.json"
    local tz = ngx.var.su_tz_offset or "+08:00"
    local expire_interval = tonumber(ngx.var.su_expire_interval) or 3600

    ensure_json(perm_path, tz)
    ensure_json(temp_path, tz)

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    su_url:flush_all()
    su_exp:flush_all()
    su_meta:flush_all()

    local now_epoch = ngx.time()
    local perm_count = 0
    local temp_count = 0

    local perm_ok, perm_data = pcall(load_json, perm_path)
    if not perm_ok then
        ngx.log(ngx.ERR, "ShortURL: 加载 perm.json 失败: ", tostring(perm_data), "，perm_count 将为 0")
        perm_data = { v = 1, at = "0", d = {} }
    end
    for code, entry in pairs(perm_data.d) do
        su_url:set(code, entry.url, 0)
        su_exp:set(code, "0", 0)
        perm_count = perm_count + 1
    end

    local temp_ok, temp_data = pcall(load_json, temp_path)
    if not temp_ok then
        ngx.log(ngx.ERR, "ShortURL: 加载 temp.json 失败: ", tostring(temp_data), "，temp_count 将为 0")
        temp_data = { v = 1, at = "0", d = {} }
    end
    for code, entry in pairs(temp_data.d) do
        local t = entry.t
        if t and t ~= "0" then
            local exp_epoch = time_util.parse_iso8601(t)
            if exp_epoch then
                local remaining = exp_epoch - now_epoch
                if remaining > 0 then
                    su_url:set(code, entry.url, remaining)
                    su_exp:set(code, t, 0)
                    temp_count = temp_count + 1
                end
            end
        end
    end

    su_meta:set("perm_count", perm_count)
    su_meta:set("temp_count", temp_count)

    -- Overwrite expire_interval from nginx config (authoritative source)
    su_meta:set("expire_interval", expire_interval)

    su_meta:set("lock_sweep", 0)
    su_meta:set("lock_perm", 0)
    su_meta:set("lock_temp", 0)

    ngx.log(ngx.NOTICE, "ShortURL: cold start complete, perm=", perm_count, " temp=", temp_count)
end

return M
