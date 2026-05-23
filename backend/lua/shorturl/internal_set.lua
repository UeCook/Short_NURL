-- internal_set.lua - Write to hot storage (su_url + su_exp shared dicts)
-- Called by PHP via POST /internal/set
-- No business logic, pure cache write
-- Also maintains perm_count / temp_count in su_meta for new entries

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
    if not params or not params.code or not params.url then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"missing code or url"}')
        ngx.exit(400)
        return
    end

    local code = params.code
    local url = params.url
    local ttl = tonumber(params.ttl) or 0
    local exp_str = params.exp_str or "0"

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- Check if this is a new entry (not an overwrite) to update counts accurately
    local is_new = (su_url:get(code) == nil)

    -- Write to shared dicts
    -- su_url uses native TTL for auto-expiry (redirect 404 on expired links)
    -- su_exp uses TTL=0 (never auto-expire) so expire_sweep can always
    -- find the entry and decrement temp_count reliably. Without this,
    -- native TTL reclaims su_exp before sweep runs → count drifts.
    su_url:set(code, url, ttl)
    su_exp:set(code, exp_str, 0)

    -- Update counts for new entries only (overwrites don't change total count)
    if is_new then
        local count_key = (ttl == 0) and "perm_count" or "temp_count"
        local new_val, err = su_meta:incr(count_key, 1)
        if not new_val then
            -- incr failed (key may not exist after cold start), initialize it
            su_meta:set(count_key, 1)
        end
    end

    ngx.header["Content-Type"] = "application/json"
    ngx.say('{"ok":true}')
    ngx.exit(200)
end

return M
