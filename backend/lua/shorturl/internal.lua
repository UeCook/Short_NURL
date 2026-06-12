-- internal.lua - Internal API route dispatcher
-- Routes /internal/set, /internal/delete, /internal/stat to handlers
-- 应用层认证：校验 LPA-Key 请求头（token 来自 internal_token 文件，懒加载缓存）

local M = {}

local _token_loaded = false
local _token_cache = ""

local function get_expected_token()
    if _token_loaded then
        return _token_cache
    end
    local path = ngx.var.su_internal_token_path
        or "/opt/shorturl/backend/data/internal_token"
    local f = io.open(path, "r")
    if f then
        local tok = f:read("*l")
        f:close()
        _token_cache = (tok and tok ~= "") and tok or ""
    else
        _token_cache = ""
    end
    _token_loaded = true
    return _token_cache
end

function M.dispatch()
    local expected = get_expected_token()
    if expected ~= "" then
        local token = ngx.var.http_lpa_key
        if not token or token ~= expected then
            ngx.status = 403
            ngx.header["Content-Type"] = "application/json"
            ngx.say('{"error":"Forbidden"}')
            ngx.exit(403)
            return
        end
    end

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
