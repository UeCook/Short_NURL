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

    -- ── 输入验证（信任边界：PHP 是低特权调用方，Lua 侧必须独立防守）──
    local MAX_URL_LEN = 2048
    local MAX_TTL = 365 * 24 * 3600  -- 与 PHP config ttl_max 保持一致

    -- code：与 PHP create.php 完全对齐 ^[0-9a-z-]{1,4}$（Lua 模式无 {n,m}，展开写法）
    if type(code) ~= "string"
       or not code:match("^[0-9a-z%-][0-9a-z%-]?[0-9a-z%-]?[0-9a-z%-]?$") then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid code"}')
        ngx.exit(400)
        return
    end

    -- url：必须以 http:// 或 https:// 开头，限制长度
    if type(url) ~= "string" or #url > MAX_URL_LEN
       or not url:match("^https?://") then
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

    -- exp_str：仅允许 "0"（永久）或标准 ISO 8601（临时，解析由 time_util 把关）
    if type(exp_str) ~= "string" or
       (exp_str ~= "0" and not exp_str:match(
           "^%d%d%d%d%-%d%d%-%d%dT%d%d:%d%d:%d%d[%+%-]%d%d:%d%d$")) then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid exp_str"}')
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
    -- 检查 shared dict 写入返回值（防止 OOM 静默失败和强制驱逐）
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
        -- 发生了强制驱逐，说明 shared dict 空间紧张，记录告警
        ngx.log(ngx.WARN, "ShortURL: su_url:set forcible eviction for code=", code)
    end

    local ok2, err2 = su_exp:set(code, exp_str, 0)
    if not ok2 then
        -- su_url 已写入，但 su_exp 失败 → 记录告警，不回滚（PHP 侧 synced=false 会告警）
        ngx.log(ngx.ERR, "ShortURL: su_exp:set failed for code=", code, " err=", tostring(err2))
    end

    -- Update counts for new entries only (overwrites don't change total count)
    if is_new then
        local count_key = (ttl == 0) and "perm_count" or "temp_count"
        local new_val, err = su_meta:incr(count_key, 1)
        if not new_val then
            -- incr failed (key missing or value corrupted), force set to 1
            ngx.log(ngx.WARN, "ShortURL: create incr failed for ", count_key, ": ", tostring(err))
            su_meta:set(count_key, 1)
        end
    end

    ngx.header["Content-Type"] = "application/json"
    ngx.say('{"ok":true}')
    ngx.exit(200)
end

return M
