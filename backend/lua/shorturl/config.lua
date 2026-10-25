-- config.lua - Runtime configuration for Lua modules
--
-- This file is read by init_worker (init_worker_by_lua_block) to register
-- the periodic expire sweep timer and cache configuration in su_meta.
-- Data loading (init.lua M.init) still reads nginx $su_* variables via ngx.var
-- in the access_by_lua_block context, and overwrites su_meta values for consistency.
--
-- IMPORTANT: Values here MUST match the nginx set directives ($su_*) in your site config.
-- Edit this file to match your deployment, then reload nginx.

local M = {
    expire_interval = 3600,
}

return M
