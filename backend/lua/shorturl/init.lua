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
    -- Create empty structure
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
        -- 清理残留临时文件，避免磁盘满等场景下 .lua.tmp 永久泄漏
        os.remove(tmp)
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

    local now_epoch = ngx.time()
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
        if t and t ~= "0" then
            -- Use epoch comparison to handle mixed timezone formats correctly
            local exp_epoch = time_util.parse_iso8601(t)
            if exp_epoch then
                local remaining = exp_epoch - now_epoch
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
