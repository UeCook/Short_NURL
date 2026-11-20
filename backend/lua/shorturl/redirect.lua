-- 短链跳转处理器，从托管模式的热存储或手工模式的快照查找短码。
-- 查找只访问内存。Nginx 的 $su_redirect_code 支持 301、302、303、307、308，默认 302。

local url_util = require "shorturl.util.url"
local runtime = require "shorturl.runtime"
local guard = require "shorturl.redirect_guard"

local M = {}

function M.go()
    ngx.header["Cache-Control"] = "no-store"
    local method = ngx.req.get_method()
    if method ~= "GET" and method ~= "HEAD" then
        ngx.header["Allow"] = "GET, HEAD"
        return ngx.exit(405)
    end
    local allowed, status, retry = guard.before()
    if not allowed then return guard.reject(status, retry) end
    if ngx.ctx.su_ready == false then return guard.reject(503, 1) end
    local code = ngx.var[1]
    if not code or code == "" then
        ngx.exit(404)
        return
    end

    code = code:lower()

    local mode = ngx.ctx.su_mode or runtime.mode()
    local target, lookup_err
    if mode == "manual" then
        target, lookup_err = require("shorturl.manual").lookup(code)
    elseif mode == "managed" then
        target = ngx.shared.su_url:get(code)
    else
        return guard.reject(503, 1)
    end
    if lookup_err then return guard.reject(503, 1) end

    if type(target) ~= "string" or target == "" then
        allowed, status, retry = guard.miss()
        if not allowed then return guard.reject(status, retry) end
        ngx.exit(404)
        return
    end

    -- 跳转前复核：不信任热存储中的任意字符串（内部写入/冷恢复/人为导入）。
    local ok, reason = url_util.check(target)
    if not ok then
        ngx.log(ngx.ERR, "ShortURL: blocked invalid redirect target for code=", code,
                " reason=", tostring(reason))
        -- 热存储中的无效条目属于数据异常，应与访问者的扫描行为区分。
        return guard.reject(503, 1)
    end

    local rc = tonumber(ngx.var.su_redirect_code) or 302
    local allowed_rc = {[301]=true,[302]=true,[303]=true,[307]=true,[308]=true}
    if not allowed_rc[rc] then rc = 302 end
    ngx.redirect(target, rc)
end

return M
