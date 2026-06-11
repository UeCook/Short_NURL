-- expire_sweep.lua - Periodic memory cleanup for expired temporary URLs
-- Scans su_exp shared dict (TTL=0, always reliable) for expired entries,
-- removes them from both su_exp and su_url, and decrements temp_count.
-- Also reconciles entries that were forcibly evicted from su_url (LRU eviction)
-- but still have orphaned records in su_exp, preventing perm_count/temp_count drift.
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
    -- TTL = 2x interval: prevents the next scheduled sweep from being skipped due to
    -- lock TTL expiring at almost the same instant the next timer fires
    su_meta:set("lock_sweep", 1, expire_interval * 2)

    local expired_deleted = 0
    local evicted_deleted = 0
    local ok, err = pcall(function()
        local now_epoch = ngx.time()

        -- Iterate su_exp (TTL=0, always reliable) instead of su_url
        -- su_url may have already been reclaimed by native TTL auto-expiry,
        -- but we still need to clean up su_exp and decrement temp_count.
        local keys = su_exp:get_keys(0)
        for _, code in ipairs(keys) do
            local exp_str = su_exp:get(code)
            if exp_str then
                if exp_str == "0" then
                    -- Permanent entry: should always exist in su_url
                    -- If missing, it was forcibly evicted (LRU) — reconcile
                    if not su_url:get(code) then
                        su_exp:delete(code)
                        evicted_deleted = evicted_deleted + 1
                    end
                else
                    -- Use epoch comparison instead of string comparison
                    -- to handle mixed timezone formats correctly
                    local exp_epoch = time_util.parse_iso8601(exp_str)
                    if exp_epoch and exp_epoch < now_epoch then
                        -- Expired: delete from both dicts (su_url may already be gone)
                        su_url:delete(code)
                        su_exp:delete(code)
                        expired_deleted = expired_deleted + 1
                    elseif not su_url:get(code) then
                        -- Not expired but missing from su_url → forcibly evicted (LRU)
                        su_exp:delete(code)
                        evicted_deleted = evicted_deleted + 1
                    end
                end
                -- else: not expired and present in su_url, skip
            end
        end
    end)

    if not ok then
        ngx.log(ngx.ERR, "ShortURL: expire_sweep error: ", err)
    end

    local total_temp_removed = expired_deleted + evicted_deleted

    if expired_deleted > 0 then
        -- Decrement temp_count by expired count
        local new_val, err = su_meta:incr("temp_count", -expired_deleted)
        if not new_val then
            ngx.log(ngx.WARN, "ShortURL: expire_sweep incr failed (", tostring(err), "), resetting temp_count to 0")
            su_meta:set("temp_count", 0)
        elseif new_val < 0 then
            su_meta:set("temp_count", 0)
        end
        ngx.log(ngx.NOTICE, "ShortURL: expire sweep removed ", expired_deleted, " expired entries")
    end

    if evicted_deleted > 0 then
        -- Evicted entries could be perm or temp; we don't know the breakdown.
        -- The safest fix: reconcile by re-counting from actual data.
        -- This is lightweight (max ~9999 entries) and only runs when evictions are detected.
        local actual_perm = 0
        local actual_temp = 0
        local all_keys = su_exp:get_keys(0)
        for _, code in ipairs(all_keys) do
            local exp_str = su_exp:get(code)
            if exp_str then
                if exp_str == "0" then
                    actual_perm = actual_perm + 1
                else
                    local exp_epoch = time_util.parse_iso8601(exp_str)
                    if exp_epoch then
                        local now_epoch = ngx.time()
                        if exp_epoch >= now_epoch then
                            actual_temp = actual_temp + 1
                        end
                    end
                end
            end
        end
        su_meta:set("perm_count", actual_perm)
        su_meta:set("temp_count", actual_temp)
        ngx.log(ngx.WARN, "ShortURL: expire sweep reconciled ", evicted_deleted,
                " LRU-evicted entries, counts corrected: perm=", actual_perm, " temp=", actual_temp)
    end

    -- Release sweep lock
    su_meta:set("lock_sweep", 0)
end

return M
