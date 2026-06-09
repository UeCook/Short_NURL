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
        local now_epoch = ngx.time()

        -- Iterate su_exp (TTL=0, always reliable) instead of su_url
        -- su_url may have already been reclaimed by native TTL auto-expiry,
        -- but we still need to clean up su_exp and decrement temp_count.
        local keys = su_exp:get_keys(0)
        for _, code in ipairs(keys) do
            local exp_str = su_exp:get(code)
            if exp_str then
                if exp_str == "0" then
                    -- Permanent link, skip
                else
                    -- Use epoch comparison instead of string comparison
                    -- to handle mixed timezone formats correctly
                    local exp_epoch = time_util.parse_iso8601(exp_str)
                    if exp_epoch and exp_epoch < now_epoch then
                        -- Expired: delete from both dicts (su_url may already be gone)
                        su_url:delete(code)
                        su_exp:delete(code)
                        deleted = deleted + 1
                    end
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
            -- incr 失败：key 不存在或值非数值（被意外写入了非数值）。
            -- 统一用 set 重置为 0，并记录警告。
            -- 注意：旧实现用 add，但 add 在 key 已存在（值损坏）时会静默失败，计数不被修复。
            ngx.log(ngx.WARN, "ShortURL: expire_sweep incr failed (", tostring(err), "), resetting temp_count to 0")
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
