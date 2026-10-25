-- expire_sweep.lua - Periodic memory cleanup for expired temporary URLs
-- Scans su_exp shared dict (TTL=0, always reliable) for expired entries,
-- removes expired entries from both su_exp and su_url, and decrements temp_count.
-- LRU-evicted entries are restored from JSON cold storage; they are not treated
-- as deleted records because cold storage remains authoritative.
-- Triggered by ngx.timer.every in init.lua (worker 0 only)
--
-- Design note: su_exp entries are stored with TTL=0 (managed by sweep, not
-- native TTL). This ensures entries are always visible to sweep for accurate
-- count decrement. su_url still uses native TTL so redirects 404 immediately
-- on expiry, but su_url entries may disappear before sweep runs — that's fine.

local time_util = require "shorturl.util.time"
local url_util = require "shorturl.util.url"
local cjson = require "cjson.safe"

local M = {}

--- Unified expiry predicate: expired when exp_epoch <= now
local function is_expired(exp_epoch, now_epoch)
    return exp_epoch <= now_epoch
end

local function load_cold(path)
    if not path then return nil end
    local f = io.open(path, "r")
    if not f then return nil end
    local content = f:read("*a")
    f:close()
    if not content or content == "" then return nil end
    local data = cjson.decode(content)
    return data and type(data) == "table" and type(data.d) == "table" and data.d or nil
end

local function restore_entry(su_url, su_exp, code, entry, exp_str, now_epoch)
    if type(entry) ~= "table" or type(entry.url) ~= "string" then return false end
    local url = entry.url
    local url_ok, reason = url_util.check(url)
    if not url_ok then
        ngx.log(ngx.WARN, "ShortURL: restore_entry blocked invalid URL for code=", code,
                " reason=", tostring(reason))
        return false
    end
    local ttl = 0
    if exp_str ~= "0" then
        local exp_epoch = time_util.parse_iso8601(exp_str)
        if not exp_epoch or is_expired(exp_epoch, now_epoch) then return false end
        ttl = exp_epoch - now_epoch
    end
    local ok_url = su_url:set(code, url, ttl)
    if not ok_url then return false end
    local ok_exp = su_exp:set(code, exp_str, 0)
    if not ok_exp then
        su_url:delete(code)
        return false
    end
    return true
end

--- Run expire sweep (called by ngx.timer.every)
-- @param premature  boolean  true if timer is exiting prematurely
function M.run(premature)
    if premature then return end

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- Acquire sweep lock atomically using add() (returns nil if key already exists)
    -- TTL ensures automatic release if the lock holder crashes.
    local expire_interval = tonumber(su_meta:get("expire_interval")) or 3600
    if expire_interval <= 0 then expire_interval = 3600 end
    -- TTL = 1x interval: long enough to prevent overlap, short enough to recover from crashes quickly
    local lock_ttl = expire_interval
    local ok, err = su_meta:add("lock_sweep", 1, lock_ttl)
    if not ok then return end

    local expired_deleted = 0
    local counts_reconciled = false
    local pok, perr = pcall(function()
        local now_epoch = ngx.time()
        local perm_data = load_cold(su_meta:get("perm_path"))
        local temp_data = load_cold(su_meta:get("temp_path"))
        if not perm_data or not temp_data then
            error("cold storage unavailable; reconciliation aborted")
        end

        -- Iterate su_exp (TTL=0) instead of su_url. Native TTL may already have
        -- reclaimed a temporary URL, but its cold record is still authoritative.
        local keys = su_exp:get_keys(0)
        for _, code in ipairs(keys) do
            local exp_str = su_exp:get(code)
            if exp_str and not su_meta:get("mutation:" .. code) then
                if exp_str == "0" then
                    local cold_entry = perm_data and perm_data[code]
                    if not cold_entry then
                        su_url:delete(code)
                        su_exp:delete(code)
                    elseif not su_url:get(code) then
                        local restored = restore_entry(su_url, su_exp, code, cold_entry, "0", now_epoch)
                        if not restored then
                            ngx.log(ngx.ERR, "ShortURL: permanent entry missing from hot storage and cold restore failed: ", code)
                        end
                    end
                else
                    -- Use epoch comparison instead of string comparison
                    -- to handle mixed timezone formats correctly
                    local exp_epoch = time_util.parse_iso8601(exp_str)
                    local cold_entry = temp_data and temp_data[code]
                    if not cold_entry then
                        su_url:delete(code)
                        su_exp:delete(code)
                    elseif exp_epoch and is_expired(exp_epoch, now_epoch) then
                        -- Expired: delete from both dicts (su_url may already be gone)
                        su_url:delete(code)
                        su_exp:delete(code)
                        expired_deleted = expired_deleted + 1
                    elseif exp_epoch and not su_url:get(code) then
                        -- Not expired but missing from su_url: restore from cold storage.
                        local restored = restore_entry(su_url, su_exp, code, cold_entry, exp_str, now_epoch)
                        if not restored then
                            ngx.log(ngx.ERR, "ShortURL: temporary entry missing from hot storage and cold restore failed: ", code)
                        end
                    elseif not exp_epoch then
                        -- Unparseable exp_str: treat as expired to free the slot
                        su_url:delete(code)
                        su_exp:delete(code)
                        expired_deleted = expired_deleted + 1
                    end
                end
                -- else: not expired and present in su_url, skip
            end
        end

        -- 每轮都以冷存储为权威来源双向对账，也覆盖 PHP 在冷热提交之间异常退出的情况。
        local actual_perm = 0
        local actual_temp = 0
        if perm_data then
            for code, entry in pairs(perm_data) do
                if type(code) == "string" and type(entry) == "table" and type(entry.url) == "string" then
                    if not su_meta:get("mutation:" .. code)
                       and (not su_url:get(code) or not su_exp:get(code)) then
                        restore_entry(su_url, su_exp, code, entry, "0", now_epoch)
                    end
                    actual_perm = actual_perm + 1
                end
            end
        end
        if temp_data then
            for code, entry in pairs(temp_data) do
                if type(code) == "string" and type(entry) == "table" and type(entry.url) == "string" then
                    local exp_str = entry.t
                    if type(exp_str) == "string" and exp_str ~= "0" then
                        local exp_epoch = time_util.parse_iso8601(exp_str)
                        if exp_epoch and not is_expired(exp_epoch, now_epoch) then
                            if not su_meta:get("mutation:" .. code)
                               and (not su_url:get(code) or not su_exp:get(code)) then
                                restore_entry(su_url, su_exp, code, entry, exp_str, now_epoch)
                            end
                            actual_temp = actual_temp + 1
                        end
                    end
                end
            end
        end
        su_meta:set("perm_count", actual_perm)
        su_meta:set("temp_count", actual_temp)
        su_meta:delete("eviction_flag")
        counts_reconciled = true
    end)

    if not pok then
        ngx.log(ngx.ERR, "ShortURL: expire_sweep error: ", perr)
    end

    if expired_deleted > 0 and not counts_reconciled then
        -- Decrement temp_count by expired count
        local new_val, err = su_meta:incr("temp_count", -expired_deleted, 0)
        if not new_val then
            ngx.log(ngx.WARN, "ShortURL: expire_sweep incr failed (", tostring(err), ")")
        elseif new_val < 0 then
            su_meta:set("temp_count", 0)
        end
        ngx.log(ngx.NOTICE, "ShortURL: expire sweep removed ", expired_deleted, " expired entries")
    end

    -- Release sweep lock
    su_meta:delete("lock_sweep")
end

return M
