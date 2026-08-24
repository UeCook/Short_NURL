-- internal.lua - Internal API route dispatcher
-- Routes /internal/set, /internal/delete, /internal/stat to handlers
-- 应用层认证：校验 LPA-Key 请求头（token 来自 internal_token 文件，每次调用读取）
--
-- 注意：此前版本使用 _token_loaded 永久缓存，若首次读取时文件不存在则永久跳过
-- 认证。现改为每次调用从文件读取，确保 nurl -itk 轮换后无需 nginx reload 即可生效。

local M = {}

local bit = require "bit"

--- 每次调用从文件读取预期令牌（不缓存）
-- 内部 API 调用频率低，每次读一个小文件开销可接受。
local function get_expected_token()
    -- Reading an undeclared nginx variable raises instead of returning nil.
    local ok, configured_path = pcall(function()
        return ngx.var.su_internal_token_path
    end)
    local path = (ok and configured_path and configured_path ~= "")
        and configured_path
        or "/opt/shorturl/backend/data/internal_token"
    local f = io.open(path, "r")
    if not f then
        return ""
    end
    local tok = f:read("*l")
    f:close()
    return (tok and tok ~= "") and tok or ""
end

--- 常量时间字符串比较（防时序攻击，与 PHP 侧 hash_equals 对齐）
local function constant_time_equals(a, b)
    if type(a) ~= "string" or type(b) ~= "string" then return false end
    if #a ~= #b then return false end
    local diff = 0
    for i = 1, #a do
        diff = bit.bor(diff, bit.bxor(string.byte(a, i), string.byte(b, i)))
    end
    return diff == 0
end

function M.dispatch()
    local expected = get_expected_token()

    -- fail-close：token 缺失或不可读时直接拒绝，不跳过认证
    if expected == "" then
        ngx.status = 500
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"internal token missing"}')
        ngx.exit(500)
        return
    end

    local token = ngx.var.http_lpa_key
    if not token or not constant_time_equals(token, expected) then
        ngx.status = 403
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"Forbidden"}')
        ngx.exit(403)
        return
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
