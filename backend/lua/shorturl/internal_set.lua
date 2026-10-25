-- internal_set.lua - Write to hot storage (su_url + su_exp shared dicts)
-- Called by PHP via POST /internal/set
-- No business logic, pure cache write
-- Also maintains perm_count / temp_count in su_meta for new entries

local cjson = require "cjson.safe"
local time_util = require "shorturl.util.time"
local url_util = require "shorturl.util.url"

local M = {}

function M.handle()
    ngx.req.read_body()
    local body = ngx.req.get_body_data()
    if not body then
        -- 请求体超过 client_body_buffer_size 时内容落入临时文件，
        -- get_body_data() 返回 nil，需回退 get_body_file() 读取，否则误报 "empty body"
        local fname = ngx.req.get_body_file()
        if fname then
            local f = io.open(fname, "rb")
            if f then
                body = f:read("*a")
                f:close()
            end
        end
    end

    if not body then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"empty body"}')
        ngx.exit(400)
        return
    end

    local params = cjson.decode(body)
    if type(params) ~= "table" or not params.code or not params.url then
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

    -- ── 输入验证（信任边界：PHP 是低特权调用方，Lua 侧必须独立防守）──
    local MAX_TTL = 365 * 24 * 3600  -- 与 PHP config ttl_max 保持一致

    -- code：与 PHP create.php 完全对齐 ^[0-9a-z-]{1,5}$（Lua 模式无 {n,m}，展开写法）
    if type(code) ~= "string"
       or not code:match("^[0-9a-z%-][0-9a-z%-]?[0-9a-z%-]?[0-9a-z%-]?[0-9a-z%-]?$") then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid code"}')
        ngx.exit(400)
        return
    end

    -- url：与 Go 创建路径共用校验（方案/长度/字符/字面量私网地址）。
    local url_ok = url_util.check(url)
    if type(url) ~= "string" or not url_ok then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid url"}')
        ngx.exit(400)
        return
    end

    -- ttl：非负整数，不超过上限
    if type(ttl) ~= "number" or ttl < 0 or ttl > MAX_TTL
       or math.floor(ttl) ~= ttl then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid ttl"}')
        ngx.exit(400)
        return
    end

    -- exp_str：仅允许 "0"（永久）或标准 ISO 8601（临时，语义校验由 time_util 把关）
    if type(exp_str) ~= "string" or
       (exp_str ~= "0" and not exp_str:match(
           "^%d%d%d%d%-%d%d%-%d%dT%d%d:%d%d:%d%d[%+%-]%d%d:%d%d$")) then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid exp_str"}')
        ngx.exit(400)
        return
    end

    -- exp_str 语义校验：必须可被 time_util 解析（拒绝 2026-99-45 等）
    if exp_str ~= "0" and not time_util.parse_iso8601(exp_str) then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid exp_str"}')
        ngx.exit(400)
        return
    end

    -- ttl/exp_str 一致性：永久链接 ttl==0 ⟺ exp_str=="0"
    if (ttl == 0) ~= (exp_str == "0") then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"ttl/exp_str mismatch"}')
        ngx.exit(400)
        return
    end
    -- ── 验证结束 ──────────────────────────────────────────────

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- Check if this is a new entry (not an overwrite) to update counts accurately
    -- Note: is_new check and su_url:set below are NOT atomic. In theory, two
    -- concurrent requests carrying the same new code could both read nil and both
    -- call incr, leading to a double-increment. In practice this does not happen
    -- because PHP's file lock (lockBegin/lockEnd) in create.php ensures the same
    -- code is never written concurrently from two requests — by the time the
    -- second request reaches internalSet, the first has already written to cold
    -- storage, so the second request would either dedup-hit (permanent) or hit a
    -- 409 conflict (custom code). This is a known, accepted design trade-off for
    -- simplicity, not an oversight.
    local is_new = (su_url:get(code) == nil)

    -- Write to shared dicts
    -- su_url uses native TTL for auto-expiry (redirect 404 on expired links)
    -- su_exp uses TTL=0 (never auto-expire) so expire_sweep can always
    -- find the entry and decrement temp_count reliably. Without this,
    -- native TTL reclaims su_exp before sweep runs → count drifts.
    -- 保存旧值用于回滚（覆盖写入时 su_exp:set 失败不能删除原有有效条目）
    local old_url = su_url:get(code)
    local old_exp = su_exp:get(code)

    local ok, err, forcible = su_url:set(code, url, ttl)
    if not ok then
        ngx.log(ngx.ERR, "ShortURL: su_url:set failed for code=", code, " err=", tostring(err))
        ngx.status = 500
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"shared dict write failed"}')
        ngx.exit(500)
        return
    end
    if forcible then
        ngx.log(ngx.ERR, "ShortURL: su_url:set forcible eviction for code=", code,
                " — shared dict 可能空间不足，计数可能漂移，等待 sweep 修正")
        su_meta:set("eviction_flag", 1)
    end

    local ok2, err2, exp_forcible = su_exp:set(code, exp_str, 0)
    if not ok2 then
        -- 回滚：恢复旧值（如果是新条目则删除，如果是覆盖写入则恢复原值和旧 TTL）
        if old_url ~= nil then
            -- 计算旧条目的剩余 TTL（而非使用本次写入的 ttl）
            local old_ttl = 0
            if old_exp and old_exp ~= "0" then
                local old_epoch = time_util.parse_iso8601(old_exp)
                if old_epoch then
                    old_ttl = math.max(1, old_epoch - ngx.time())
                end
            end
            su_url:set(code, old_url, old_ttl)
        else
            su_url:delete(code)
        end
        if old_exp ~= nil then
            su_exp:set(code, old_exp, 0)
        end
        ngx.log(ngx.ERR, "ShortURL: su_exp:set failed for code=", code, " err=", tostring(err2))
        ngx.status = 500
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"su_exp write failed"}')
        return ngx.exit(500)
    end
    if exp_forcible then
        ngx.log(ngx.ERR, "ShortURL: su_exp:set forcible eviction for code=", code,
                " — eviction recovery will rebuild entries from cold storage")
        su_meta:set("eviction_flag", 1)
    end

    -- Update counts for new entries only (overwrites don't change total count)
    if is_new then
        local count_key = (ttl == 0) and "perm_count" or "temp_count"
        local new_val, err = su_meta:incr(count_key, 1, 0)
        if not new_val then
            ngx.log(ngx.WARN, "ShortURL: create incr failed for ", count_key, ": ", tostring(err))
        end
    end

    ngx.header["Content-Type"] = "application/json"
    ngx.say('{"ok":true}')
    ngx.exit(200)
end

return M
