-- Nginx variables are unavailable in timers and undeclared variables raise.
-- Capture request configuration before scheduling any background work.
local config = require "shorturl.config"
local M = {}

function M.variable(name, fallback)
    local ok, value = pcall(function() return ngx.var[name] end)
    if ok and value ~= nil and value ~= "" then return value end
    return fallback
end

function M.mode()
    return M.variable("su_mode", config.mode or "managed")
end

function M.manual_options()
    return {
        perm_path = M.variable("su_perm_path", "/opt/shorturl/backend/data/perm.json"),
        temp_path = M.variable("su_temp_path", "/opt/shorturl/backend/data/temp.json"),
        interval = tonumber(M.variable("su_reload_interval", config.manual_reload_interval)),
        max_file_bytes = config.manual_max_file_bytes,
        max_entries = config.manual_max_entries,
    }
end

return M
