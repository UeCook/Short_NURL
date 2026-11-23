-- 定期清理过期的临时短链，并依据 JSON 冷存储核对共享字典和计数。
-- 冷存储仍有效的条目即使被 LRU 淘汰，也会重新装载到共享字典。
-- 各工作进程收到请求后注册定时器，清理任务通过共享锁协调执行。
--
-- su_exp 条目的 TTL 为 0，使清理任务仍可读取过期时间。若条目被 LRU 淘汰，
-- 清理任务会扫描冷存储来补齐。su_url 使用共享字典的 TTL，使过期链接及时停止跳转。

local time_util = require "shorturl.util.time"
local url_util = require "shorturl.util.url"
local cjson = require "cjson.safe"
local cold_lock = require "shorturl.cold_lock"

local M = {}

local function load_cold(path)
    if not path then return nil end
    local f = io.open(path, "r")
    if not f then return nil end
    local content = f:read("*a")
    f:close()
    if not content or content == "" then return nil end
    local data = cjson.decode(content)
    return data and type(data) == "table" and type(data.d) == "table" and data.d or nil
end

local function restore_entry(su_url, su_exp, code, url, exp, ttl)
    local valid, reason = url_util.check(url)
    if not valid then return nil, reason end
    local ok, err = su_url:set(code, url, ttl)
    if not ok then return nil, err end
    return su_exp:set(code, exp, 0)
end

--- 执行过期清理，由 ngx.timer.every 调用。
-- @param premature boolean 定时器提前结束时为 true。
function M.run(premature)
    if premature then return end
    -- 切换模式并重载时，旧工作进程的定时器可能仍在运行。
    if ngx.shared.su_meta:get("runtime_mode") == "manual" then return end

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- 用 add() 原子获取清理锁，锁已存在时返回 nil。
    -- 持锁进程异常退出后，锁会在 TTL 到期时自动释放。
    local expire_interval = tonumber(su_meta:get("expire_interval")) or 3600
    if expire_interval <= 0 then expire_interval = 3600 end
    -- 共享锁的有效期设为一个清理周期。
    local lock_ttl = expire_interval
    local ok, err = su_meta:add("lock_sweep", 1, lock_ttl)
    if not ok then return end

    local perm_path, temp_path = su_meta:get("perm_path"), su_meta:get("temp_path")
    local pok, perr = cold_lock.with(perm_path, temp_path, false, function()
        local now_epoch = ngx.time()
        local perm_data = load_cold(perm_path)
        local temp_data = load_cold(temp_path)
        if not perm_data or not temp_data then
            error("cold storage unavailable; reconciliation aborted")
        end

        -- 先建立有效冷记录的索引，再释放两个字典各自留下的孤儿。
        local expected, temp_expiry = {}, {}
        local actual_perm = 0
        local actual_temp = 0
        for code, entry in pairs(perm_data) do
            if type(code) == "string" and type(entry) == "table" and type(entry.url) == "string" then
                expected[code] = entry
                actual_perm = actual_perm + 1
            end
        end
        for code, entry in pairs(temp_data) do
            if type(code) == "string" and type(entry) == "table" and type(entry.url) == "string" then
                local exp_epoch = time_util.parse_iso8601(entry.t)
                if exp_epoch and exp_epoch > now_epoch then
                    expected[code] = entry
                    temp_expiry[code] = exp_epoch
                    actual_temp = actual_temp + 1
                end
            end
        end

        local function remove_orphans(dict)
            for _, code in ipairs(dict:get_keys(0)) do
                if not expected[code] and not su_meta:get("mutation:" .. code) then
                    su_url:delete(code)
                    su_exp:delete(code)
                end
            end
        end
        remove_orphans(su_url)
        remove_orphans(su_exp)

        for code, entry in pairs(expected) do
            local expires = temp_expiry[code]
            local exp = expires and entry.t or "0"
            if not su_meta:get("mutation:" .. code)
               and (su_url:get(code) ~= entry.url or su_exp:get(code) ~= exp) then
                local ttl = expires and expires - now_epoch or 0
                local restored, reason = restore_entry(su_url, su_exp, code, entry.url, exp, ttl)
                if not restored then
                    su_url:delete(code)
                    su_exp:delete(code)
                    ngx.log(ngx.ERR, "ShortURL: cold restore failed for code=", code, " reason=", tostring(reason))
                end
            end
        end
        su_meta:set("perm_count", actual_perm)
        su_meta:set("temp_count", actual_temp)
    end)

    su_meta:delete("lock_sweep")
    if not pok and perr == "busy" then
        if su_meta:safe_add("sweep_retry", true, 5) then
            local scheduled = ngx.timer.at(1, function(stopping)
                su_meta:delete("sweep_retry")
                M.run(stopping)
            end)
            if not scheduled then su_meta:delete("sweep_retry") end
        end
    elseif not pok then
        ngx.log(ngx.ERR, "ShortURL: expire_sweep error: ", perr)
    end
end

return M
