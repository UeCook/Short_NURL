-- config.lua - Runtime configuration for Lua modules
--
-- init.lua registers each worker's sweep timer lazily from access_by_lua_block.
-- Nginx $su_* variables are authoritative and are stored in su_meta before registration.
-- This interval is the fallback when su_meta does not contain an interval.
--
-- Configure the deployment interval with $su_expire_interval in the site config.

local M = {
    expire_interval = 3600,
}

return M
