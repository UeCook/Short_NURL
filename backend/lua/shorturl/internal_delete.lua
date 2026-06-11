-- internal_delete.lua - Delete from hot storage (su_url + su_exp shared dicts)
-- Called by PHP via POST /internal/delete
-- Also decrements perm_count / temp_count in su_meta for accurate tracking
--
-- Type resolution priority for counter decrement:
--   1. params.type from PHP (authoritative — determined from cold storage lookup)
--   2. su_exp lookup (fallback — may be missing due to shared dict restart/eviction)
--   3. Skip decrement with warning log (both sources unavailable)

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
    local forced_type = params.type  -- "perm" or "temp" from PHP (authoritative)

    -- ── 输入验证（信任边界：PHP 是低特权调用方，Lua 侧必须独立防守）──
    -- code：与 PHP create.php 完全对齐 ^[0-9a-z-]{1,4}$（Lua 模式无 {n,m}，展开写法）
    if type(code) ~= "string"
       or not code:match("^[0-9a-z%-][0-9a-z%-]?[0-9a-z%-]?[0-9a-z%-]?$") then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid code"}')
        ngx.exit(400)
        return
    end

    -- type：只能是 nil（降级回退）或 "perm" 或 "temp"，杜绝非对称分支被滥用
    if forced_type ~= nil and forced_type ~= "perm" and forced_type ~= "temp" then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid type"}')
        ngx.exit(400)
        return
    end
    -- ── 验证结束 ──────────────────────────────────────────────

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- Determine which counter to decrement using priority:
    --   1. params.type from PHP (authoritative — determined from cold storage)
    --   2. su_exp lookup (fallback — may be missing due to shared dict restart/eviction)
    --   3. Skip decrement with warning log
    local count_key = nil
    local source = nil

    if forced_type then
        -- PHP provided the type from cold storage lookup — use it directly
        count_key = (forced_type == "perm") and "perm_count" or "temp_count"
        source = "php:" .. forced_type
    else
        local exp_str = su_exp:get(code)
        if exp_str then
            if exp_str == "0" then
                count_key = "perm_count"
            else
                count_key = "temp_count"
            end
            source = "su_exp"
        end
    end

    if count_key then
        local new_val, err = su_meta:incr(count_key, -1)
        if not new_val then
            -- incr failed (key missing or value corrupted), force reset to 0
            ngx.log(ngx.WARN, "ShortURL: delete incr failed for ", count_key, ": ", tostring(err))
            su_meta:set(count_key, 0)
        elseif new_val < 0 then
            -- Count went negative (drift), reset to 0
            su_meta:set(count_key, 0)
        end
    else
        -- Both sources unavailable — cannot determine type, skip decrement
        ngx.log(ngx.WARN, "ShortURL: delete - cannot determine type for code=", code,
            ", skipping count decrement (su_exp missing, no PHP type hint)")
    end

    su_url:delete(code)
    su_exp:delete(code)

    ngx.header["Content-Type"] = "application/json"
    ngx.say('{"ok":true}')
    ngx.exit(200)
end

return M
