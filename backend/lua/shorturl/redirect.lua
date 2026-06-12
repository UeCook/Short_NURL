-- redirect.lua - Short URL redirect handler
-- Looks up short code in su_url shared dict and performs redirect
-- Read path: no disk I/O, pure memory lookup
-- Redirect code (301/302) configurable via nginx $su_redirect_code (default 302)

local M = {}

function M.go()
    local code = ngx.var[1]
    if not code or code == "" then
        ngx.exit(404)
        return
    end

    code = code:lower()

    local su_url = ngx.shared.su_url
    local target = su_url:get(code)

    if type(target) ~= "string" or target == "" then
        ngx.exit(404)
        return
    end

    local redirect_code = tonumber(ngx.var.su_redirect_code) or 302
    ngx.redirect(target, redirect_code)
end

return M
