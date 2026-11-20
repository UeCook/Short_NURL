-- 在共享字典中控制短链访问，各工作进程共用额度。
-- 查短码前先检查全局额度，所有跳转请求均计入限额，以约束分布式扫描。
local config = require "shorturl.config"
local M = {}
local last_error = -math.huge

local function settings()
    local cfg = config.redirect_guard
    if type(cfg) ~= "table" or type(cfg.enabled) ~= "boolean" then return nil end
    if not cfg.enabled then return cfg end
    for _, field in ipairs({ "global_requests_per_second", "miss_window_seconds", "max_misses", "ban_seconds" }) do
        local value = cfg[field]
        if type(value) ~= "number" or value < 1 or value > 1000000
            or value ~= math.floor(value) then return nil end
    end
    if cfg.ban_seconds < cfg.miss_window_seconds then return nil end
    return cfg
end

local function unavailable(reason)
    if ngx.time() - last_error >= 60 then
        last_error = ngx.time()
        ngx.log(ngx.ERR, "ShortURL: redirect protection unavailable: ", tostring(reason))
    end
    return false, 503, 1
end

local function increment(dict, key, ttl)
    local added, err = dict:safe_add(key, 0, ttl)
    if not added and err ~= "exists" then return nil, err end
    -- 不传 incr 的初始值参数，改用 safe_add 创建计数，避免触发 LRU 淘汰。
    return dict:incr(key, 1)
end

local function retry_after(dict, key)
    local ttl = dict:ttl(key)
    return math.max(1, math.ceil(ttl or 1))
end

function M.before()
    local cfg = settings()
    if not cfg then return unavailable("invalid redirect_guard configuration") end
    if not cfg.enabled then return true end
    local dict = ngx.shared.su_blocklist
    if not dict then return unavailable("lua_shared_dict su_blocklist is missing") end
    -- 以 Nginx 解析出的客户端地址为准，调用方传入的请求头不直接参与判断。
    local ip = ngx.var.remote_addr
    if type(ip) ~= "string" or ip == "" then return unavailable("remote address missing") end
    local ban = dict:get("scan:b:" .. ip)
    if ban and ban > ngx.time() then return false, 429, ban - ngx.time() end
    local misses = dict:get("scan:m:" .. ip)
    if misses and misses >= cfg.max_misses then
        return false, 429, retry_after(dict, "scan:m:" .. ip)
    end
    local count, err = increment(dict, "scan:g:" .. ngx.time(), 2)
    if not count then return unavailable(err) end
    if count > cfg.global_requests_per_second then return false, 429, 1 end
    return true
end

function M.miss()
    local cfg = settings()
    if not cfg then return unavailable("invalid redirect_guard configuration") end
    if not cfg.enabled then return true end
    local dict = ngx.shared.su_blocklist
    if not dict then return unavailable("lua_shared_dict su_blocklist is missing") end
    local ip = ngx.var.remote_addr
    if type(ip) ~= "string" or ip == "" then return unavailable("remote address missing") end
    local count, err = increment(dict, "scan:m:" .. ip, cfg.miss_window_seconds)
    if not count then return unavailable(err) end
    if count >= cfg.max_misses then
        -- 并发未命中或客户端重试时，保留既有封禁的到期时间。
        local added, ban_err = dict:safe_add("scan:b:" .. ip,
            ngx.time() + cfg.ban_seconds, cfg.ban_seconds)
        if not added and ban_err ~= "exists" then return unavailable(ban_err) end
        local until_time = dict:get("scan:b:" .. ip)
        return false, 429, math.max(1, (until_time or ngx.time() + 1) - ngx.time())
    end
    return true
end

function M.reject(status, retry)
    ngx.header["Cache-Control"] = "no-store"
    ngx.header["Retry-After"] = tostring(math.max(1, math.ceil(retry or 1)))
    return ngx.exit(status)
end

return M
