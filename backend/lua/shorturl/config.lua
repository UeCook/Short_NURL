-- config.lua - Runtime configuration for Lua modules
--
-- init.lua registers each worker's sweep timer lazily from access_by_lua_block.
-- Nginx $su_* variables are authoritative and are stored in su_meta before registration.
-- This interval is the fallback when su_meta does not contain an interval.
--
-- Configure the deployment interval with $su_expire_interval in the site config.

local M = {
    -- Nginx $su_mode overrides this: "managed" (Go) or "manual" (read-only JSON).
    mode = "managed",
    expire_interval = 3600,
    manual_reload_interval = 5,
    manual_max_file_bytes = 16 * 1024 * 1024,
    manual_max_entries = 30000,

    -- Shared by every worker, in su_blocklist. The global budget is checked
    -- BEFORE lookup and includes valid redirects, so rotating IPs cannot bypass it.
    redirect_guard = {
        enabled = true,
        global_requests_per_second = 100,
        miss_window_seconds = 60,
        max_misses = 20,
        ban_seconds = 900,
    },
}

return M
