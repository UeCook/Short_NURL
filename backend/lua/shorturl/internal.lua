-- internal.lua - Internal API route dispatcher
-- Routes /internal/set, /internal/delete, /internal/stat to handlers
-- Only accessible from allowed IPs (enforced by Nginx config)

local cjson = require "cjson.safe"
local M = {}

function M.dispatch()
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
