-- 运行模式分派与托管模式的冷启动加载。
-- 手工模式由 shorturl.manual 发布只读快照。
-- 托管模式在初始化时将 JSON 数据装载到 lua_shared_dict。
-- 各工作进程在收到首个站点请求时，经 access_by_lua_block 触发初始化。
--
-- 在托管模式下，M.init_worker() 将 expire_interval 存入 su_meta，
-- 再调用 ngx.timer.every 注册过期清理定时器。timer_registered 防止同一工作进程重复注册。
-- 注册在收到请求时进行，可与 WAF 等第三方的 init_worker_by_lua_file 配置共存。
--
-- M.init() 读取 Nginx 的 $su_* 变量并调用对应的模式初始化逻辑。
--
-- 托管模式从 flush_all 清空共享字典到数据装载完成之间，
-- 并发请求可能短暂查不到短码，返回 404。

local cjson = require "cjson.safe"
local time_util = require "shorturl.util.time"
local url_util = require "shorturl.util.url"
local expire_sweep = require "shorturl.expire_sweep"
local su_config = require "shorturl.config"
local runtime = require "shorturl.runtime"
local cold_lock = require "shorturl.cold_lock"

local M = {}
-- 仅在当前工作进程有效。重载会重置此标志，共享字典里的 inited 则可保留。
local timer_registered = false
local managed_snapshot_version = 2

--- 读取 JSON 文件并检查根对象是否包含 d 表。
-- @param path string 文件路径。
-- @return table|nil 根对象。失败时返回 nil 和错误信息。
local function load_json(path)
    local f, open_err = io.open(path, "r")
    if not f then
        return nil, open_err or "open failed"
    end
    local content = f:read("*a")
    f:close()
    if not content or content == "" then
        return nil, "empty file"
    end
    local data, err = cjson.decode(content)
    if not data or type(data) ~= "table" or type(data.d) ~= "table" then
        return nil, err or "invalid structure"
    end
    return data
end

--- 文件不存在时，写入空的 JSON 数据结构。
-- @param path string 文件路径。
local function ensure_json(path, tz)
    local f = io.open(path, "r")
    if f then
        f:close()
        return
    end
    local empty = { v = 1, at = time_util.now_iso8601(tz), d = {} }
    local tmp = path .. ".lua.tmp"
    f = io.open(tmp, "w")
    if not f then
        ngx.log(ngx.ERR, "ShortURL: 无法创建数据文件: ", tmp)
        return
    end
    f:write(cjson.encode(empty))
    f:close()
    local ok, err = os.rename(tmp, path)
    if not ok then
        ngx.log(ngx.ERR, "ShortURL: 重命名失败: ", tmp, " -> ", path, " err: ", err)
        os.remove(tmp)
    end
end

--- 在当前工作进程注册周期性的过期清理定时器。
-- 托管模式由 M.init() 在请求中调用，并以 config.lua 作为周期的后备配置。
-- 定时器随工作进程退出。新进程即使看到共享字典里的 inited，也要重新注册。
function M.init_worker()
    if timer_registered then return true end

    local su_meta = ngx.shared.su_meta
    local expire_interval = tonumber(su_meta:get("expire_interval"))
        or tonumber(su_config.expire_interval) or 3600
    if expire_interval <= 0 then expire_interval = 3600 end

    su_meta:set("expire_interval", expire_interval)

    -- 任一收到请求的工作进程都可启动定时器，清理任务通过共享锁串行执行。
    local ok, err = ngx.timer.every(expire_interval, expire_sweep.run)
    if not ok then
        ngx.log(ngx.ERR, "ShortURL: timer registration failed: ", err)
        return false -- 下次请求会重试注册。
    end
    timer_registered = true

    ngx.log(ngx.NOTICE, "ShortURL: init_worker timer registered, interval=", expire_interval)
    return true
end

-- 调用期间持有冷库排他锁，首次启动可能创建数据文件。
local function load_managed_snapshot(perm_path, temp_path, tz)
    local su_meta = ngx.shared.su_meta

    ensure_json(perm_path, tz)
    ensure_json(temp_path, tz)

    su_meta:set("perm_path", perm_path)
    su_meta:set("temp_path", temp_path)

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp

    local now_epoch = ngx.time()
    local perm_count = 0
    local temp_count = 0

    local perm_data, perm_err = load_json(perm_path)
    local temp_data, temp_err = load_json(temp_path)
    if not perm_data or not temp_data then
        ngx.log(ngx.ERR, "ShortURL: cold start aborted: ", perm_err or "", " ", temp_err or "")
        return false
    end

    su_url:flush_all()
    su_exp:flush_all()

    local function store_entry(code, url, ttl, exp_str)
        if type(code) ~= "string" or type(url) ~= "string" or url == "" then
            return false
        end
        local url_ok, reason = url_util.check(url)
        if not url_ok then
            ngx.log(ngx.WARN, "ShortURL: skipping invalid URL for code=", code,
                    " reason=", tostring(reason))
            return false
        end
        local ok_url, err_url = su_url:set(code, url, ttl)
        if not ok_url then
            ngx.log(ngx.ERR, "ShortURL: su_url:set failed code=", code, " err=", tostring(err_url))
            return false
        end

        local ok_exp, err_exp = su_exp:set(code, exp_str, 0)
        if not ok_exp then
            su_url:delete(code)
            ngx.log(ngx.ERR, "ShortURL: su_exp:set failed code=", code, " err=", tostring(err_exp))
            return false
        end
        return true
    end

    for code, entry in pairs(perm_data.d) do
        if type(code) == "string" and type(entry) == "table" and type(entry.url) == "string" then
            if store_entry(code, entry.url, 0, "0") then
                perm_count = perm_count + 1
            end
        else
            ngx.log(ngx.WARN, "ShortURL: skipping malformed perm entry: ", tostring(code))
        end
    end

    for code, entry in pairs(temp_data.d) do
        if type(code) == "string" and type(entry) == "table" and type(entry.url) == "string" then
            local t = entry.t
            if type(t) == "string" and t ~= "0" then
                local exp_epoch = time_util.parse_iso8601(t)
                if exp_epoch then
                    local remaining = exp_epoch - now_epoch
                    if remaining > 0 then
                        if store_entry(code, entry.url, remaining, t) then
                            temp_count = temp_count + 1
                        end
                    end
                end
            end
        else
            ngx.log(ngx.WARN, "ShortURL: skipping malformed temp entry: ", tostring(code))
        end
    end

    su_meta:set("perm_count", perm_count)
    su_meta:set("temp_count", temp_count)

    -- 版本变化时从冷库重建，淘汰旧版本可能留下的仅热存储记录。
    su_meta:set("inited", managed_snapshot_version)

    ngx.log(ngx.NOTICE, "ShortURL: cold start complete, perm=", perm_count, " temp=", temp_count)
    return true
end

local function init_managed()
    local su_meta = ngx.shared.su_meta
    local perm_path = ngx.var.su_perm_path or "/opt/shorturl/backend/data/perm.json"
    local temp_path = ngx.var.su_temp_path or "/opt/shorturl/backend/data/temp.json"
    local tz = ngx.var.su_tz_offset or "+08:00"
    local expire_interval = tonumber(ngx.var.su_expire_interval) or 3600
    if expire_interval <= 0 then expire_interval = 3600 end
    su_meta:set("expire_interval", expire_interval)

    if su_meta:get("inited") == managed_snapshot_version
       and su_meta:get("perm_path") == perm_path
       and su_meta:get("temp_path") == temp_path then
        M.init_worker()
        return true
    end

    local ok, ready = cold_lock.with(perm_path, temp_path, true, function()
        if su_meta:get("inited") == managed_snapshot_version
           and su_meta:get("perm_path") == perm_path
           and su_meta:get("temp_path") == temp_path then
            return true
        end
        su_meta:delete("inited")
        return load_managed_snapshot(perm_path, temp_path, tz)
    end)
    if not ok then
        if ready ~= "busy" then ngx.log(ngx.ERR, "ShortURL: cold start lock failed: ", ready) end
        return false
    end
    if ready then M.init_worker() end
    return ready
end

-- 手工模式只读取现有文件并发布快照。冷热对账和数据文件初始化由托管模式负责。
function M.init()
    local mode = runtime.mode()
    local ready = false
    if mode == "manual" then
        ngx.shared.su_meta:set("runtime_mode", "manual")
        ready = require("shorturl.manual").init()
    elseif mode == "managed" then
        ngx.shared.su_meta:set("runtime_mode", "managed")
        ready = init_managed()
    else
        ngx.log(ngx.ERR, "ShortURL: invalid su_mode: ", tostring(mode))
    end
    ngx.ctx.su_mode = mode
    ngx.ctx.su_ready = ready
    return ready
end

return M
