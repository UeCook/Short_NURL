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

--- 到期时间小于或等于当前时间时，判定为过期。
local function is_expired(exp_epoch, now_epoch)
    return exp_epoch <= now_epoch
end

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

local function restore_entry(su_url, su_exp, code, entry, exp_str, now_epoch)
    if type(entry) ~= "table" or type(entry.url) ~= "string" then return false end
    local url = entry.url
    local url_ok, reason = url_util.check(url)
    if not url_ok then
        ngx.log(ngx.WARN, "ShortURL: restore_entry blocked invalid URL for code=", code,
                " reason=", tostring(reason))
        return false
    end
    local ttl = 0
    if exp_str ~= "0" then
        local exp_epoch = time_util.parse_iso8601(exp_str)
        if not exp_epoch or is_expired(exp_epoch, now_epoch) then return false end
        ttl = exp_epoch - now_epoch
    end
    local ok_url = su_url:set(code, url, ttl)
    if not ok_url then return false end
    local ok_exp = su_exp:set(code, exp_str, 0)
    if not ok_exp then
        su_url:delete(code)
        return false
    end
    return true
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

        -- 遍历 TTL 为 0 的 su_exp。su_url 可能已删除过期条目，冷存储仍是对账依据。
        local keys = su_exp:get_keys(0)
        for _, code in ipairs(keys) do
            local exp_str = su_exp:get(code)
            if exp_str and not su_meta:get("mutation:" .. code) then
                if exp_str == "0" then
                    local cold_entry = perm_data and perm_data[code]
                    if not cold_entry then
                        su_url:delete(code)
                        su_exp:delete(code)
                    elseif not su_url:get(code) then
                        local restored = restore_entry(su_url, su_exp, code, cold_entry, "0", now_epoch)
                        if not restored then
                            ngx.log(ngx.ERR, "ShortURL: permanent entry missing from hot storage and cold restore failed: ", code)
                        end
                    end
                else
                    -- 比较 Unix 时间戳，避免不同时区偏移量影响排序结果。
                    local exp_epoch = time_util.parse_iso8601(exp_str)
                    local cold_entry = temp_data and temp_data[code]
                    if not cold_entry then
                        su_url:delete(code)
                        su_exp:delete(code)
                    elseif exp_epoch and is_expired(exp_epoch, now_epoch) then
                        -- 已过期，从两个字典删除。su_url 中的条目可能已自动过期。
                        su_url:delete(code)
                        su_exp:delete(code)
                    elseif exp_epoch and not su_url:get(code) then
                        -- 条目仍有效，但 su_url 缺失时从冷存储恢复。
                        local restored = restore_entry(su_url, su_exp, code, cold_entry, exp_str, now_epoch)
                        if not restored then
                            ngx.log(ngx.ERR, "ShortURL: temporary entry missing from hot storage and cold restore failed: ", code)
                        end
                    elseif not exp_epoch then
                        -- 到期时间无法解析，按过期处理以释放空间。
                        su_url:delete(code)
                        su_exp:delete(code)
                    end
                end
                -- 有效且仍在 su_url 中的条目无需更新。
            end
        end

        -- 每轮都以冷存储为权威来源双向对账，也覆盖 Go 在冷热提交之间异常退出的情况。
        local actual_perm = 0
        local actual_temp = 0
        if perm_data then
            for code, entry in pairs(perm_data) do
                if type(code) == "string" and type(entry) == "table" and type(entry.url) == "string" then
                    if not su_meta:get("mutation:" .. code)
                       and (not su_url:get(code) or not su_exp:get(code)) then
                        restore_entry(su_url, su_exp, code, entry, "0", now_epoch)
                    end
                    actual_perm = actual_perm + 1
                end
            end
        end
        if temp_data then
            for code, entry in pairs(temp_data) do
                if type(code) == "string" and type(entry) == "table" and type(entry.url) == "string" then
                    local exp_str = entry.t
                    if type(exp_str) == "string" and exp_str ~= "0" then
                        local exp_epoch = time_util.parse_iso8601(exp_str)
                        if exp_epoch and not is_expired(exp_epoch, now_epoch) then
                            if not su_meta:get("mutation:" .. code)
                               and (not su_url:get(code) or not su_exp:get(code)) then
                                restore_entry(su_url, su_exp, code, entry, exp_str, now_epoch)
                            end
                            actual_temp = actual_temp + 1
                        end
                    end
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
