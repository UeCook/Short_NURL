-- internal_delete.lua - Delete from hot storage (su_url + su_exp shared dicts)
-- Called by PHP via POST /internal/delete
-- Also decrements perm_count / temp_count in su_meta for accurate tracking

local cjson = require "cjson.safe"

local M = {}

function M.handle()
    ngx.req.read_body()
    local body = ngx.req.get_body_data()

    if not body then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"empty body"}')
        ngx.exit(400)
        return
    end

    local params = cjson.decode(body)
    if not params or not params.code then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"missing code"}')
        ngx.exit(400)
        return
    end

    local code = params.code
    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- Check entry type before deletion to update the correct counter
    local exp_str = su_exp:get(code)
    if exp_str then
        -- Entry exists: decrement the appropriate counter
        local count_key
        if exp_str == "0" then
            count_key = "perm_count"
        else
            -- Any non-"0" value is treated as temporary (including corrupted data)
            count_key = "temp_count"
        end
        local new_val, err = su_meta:incr(count_key, -1)
        if not new_val then
            -- incr failed (key may not exist), initialize to 0 (entry already deleted)
            su_meta:set(count_key, 0)
        elseif new_val < 0 then
            -- Count went negative (drift), reset to 0
            su_meta:set(count_key, 0)
        end
    end

    su_url:delete(code)
    su_exp:delete(code)

    ngx.header["Content-Type"] = "application/json"
    ngx.say('{"ok":true}')
    ngx.exit(200)
end

return M
