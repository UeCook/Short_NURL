-- internal.lua - Internal API route dispatcher
-- Routes /internal/set, /internal/delete, /internal/stat to handlers
-- Only accessible from 127.0.0.1 (enforced by Nginx config)
-- Defense in depth: verifies X-Internal-Token header if $su_internal_token is configured

local cjson = require "cjson.safe"
local M = {}

--- Verify internal token (defense in depth)
-- Reads expected token from nginx variable $su_internal_token.
-- If configured (non-empty), requires matching X-Internal-Token header.
-- If empty or unset, skips verification (backward compatible).
-- @return boolean  true if verification passes, false if rejected (403 already sent)
local function verify_token()
    local expected = ngx.var.su_internal_token or ""
    -- No token configured → skip verification
    if expected == "" then return true end

    local token = ngx.req.get_headers()["X-Internal-Token"] or ""
    if token ~= expected then
        ngx.log(ngx.WARN, "internal token mismatch: expected=", expected, " got=", token)
        ngx.status = 403
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"forbidden"}')
        ngx.exit(403)
        return false
    end
    return true
end

function M.dispatch()
    -- Token verification (defense in depth, skips if not configured)
    if not verify_token() then return end

    ngx.req.read_body()
    local uri = ngx.var.uri

    if uri:match("/internal/set$") then
        local internal_set = require "shorturl.internal_set"
        internal_set.handle()

    elseif uri:match("/internal/delete$") then
        local internal_delete = require "shorturl.internal_delete"
        internal_delete.handle()

    elseif uri:match("/internal/stat$") then
        local internal_stat = require "shorturl.internal_stat"
        internal_stat.handle()

    else
        ngx.status = 404
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"unknown internal endpoint"}')
        ngx.exit(404)
    end
end

return M
