-- internal_mutation.lua - Mark a code while Go commits hot and cold storage.

local cjson = require "cjson.safe"

local M = {}

function M.handle()
    ngx.req.read_body()
    local body_data = ngx.req.get_body_data()
    if not body_data then
        local file = ngx.req.get_body_file()
        if file then
            local fh = io.open(file, "r")
            if fh then
                body_data = fh:read("*a")
                fh:close()
            end
        end
    end
    local params = cjson.decode(body_data or "")
    if type(params) ~= "table" then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid body"}')
        return ngx.exit(400)
    end
    local code = params.code
    local action = params.action

    if type(code) ~= "string" or #code < 1 or #code > 5 or not code:match("^[0-9a-z%-]+$") then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid code"}')
        return ngx.exit(400)
    end
    if action ~= "begin" and action ~= "end" then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid action"}')
        return ngx.exit(400)
    end

    local key = "mutation:" .. code
    -- End keeps a grace period for a sweep that captured the previous cold snapshot.
    local ttl = (action == "begin") and 120 or 60
    -- Do not evict other in-flight mutation markers to make room for this one.
    local ok, err = ngx.shared.su_meta:safe_set(key, 1, ttl)
    if not ok then
        ngx.log(ngx.ERR, "ShortURL: mutation marker failed code=", code, " err=", tostring(err))
        ngx.status = 503
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"mutation marker unavailable"}')
        return ngx.exit(503)
    end

    ngx.header["Content-Type"] = "application/json"
    ngx.say('{"ok":true}')
    return ngx.exit(200)
end

return M
