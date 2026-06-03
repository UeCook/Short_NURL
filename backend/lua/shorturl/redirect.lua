-- redirect.lua - Short URL redirect handler
-- Looks up short code in su_url shared dict and performs 302 redirect
-- Read path: no disk I/O, pure memory lookup

local M = {}

function M.go()
    -- Get code from URI capture group
    local code = ngx.var[1]
    if not code or code == "" then
        ngx.exit(404)
        return
    end

    -- Convert to lowercase
    code = code:lower()

    local su_url = ngx.shared.su_url
    local target = su_url:get(code)

    if not target then
        ngx.redirect("/404.html")
        return
    end

    -- Perform 302 redirect
    ngx.redirect(target, 302)
end

return M
