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
