-- 手工模式从 JSON 只读加载数据，可独立于 Go、API 密钥和内部 HTTP 接口运行。
-- 已发布的快照保持不变。共享字典中的 active 指向当前代，
-- 候选代通过 safe_set 写入，避免挤掉正在服务的快照。
local cjson = require "cjson.safe"
local config = require "shorturl.config"
local time_util = require "shorturl.util.time"
local url_util = require "shorturl.util.url"
local M = {}

local options, timer_registered
local cached_generation, cached_active
-- 线程池中的 Lua VM 使用系统秒钟，多保留一秒以覆盖时间取整。
local publish_now = ngx.now or os.time
local publish_time = ngx.time or os.time
local RETIRE_GRACE = ngx.now and 1 or 2
local reserved = {
    api = true, stat = true, admin = true, data = true, lua = true,
    headless = true, internal = true, nurl = true,
}

local function active(dict)
    local generation = dict:get("active")
    if not generation then return nil end
    if generation ~= cached_generation then
        local raw = dict:get("meta:" .. tostring(generation))
        if not raw then
            generation = dict:get("active")
            raw = generation and dict:get("meta:" .. tostring(generation))
        end
        local decoded = raw and cjson.decode(raw)
        if type(decoded) ~= "table" then return nil end
        cached_generation, cached_active = generation, decoded
    end
    return cached_active
end

local function ready(dict)
    local current = active(dict)
    return current ~= nil and options ~= nil
        and current.perm_path == options.perm_path
        and current.temp_path == options.temp_path
end

local function read_file(path, limit)
    local f, err = io.open(path, "rb")
    if not f then return nil, "cannot read " .. path .. ": " .. tostring(err) end
    local raw, read_err = f:read(limit + 1)
    f:close()
    if not raw or raw == "" then return nil, "empty/unreadable file: " .. path .. ": " .. tostring(read_err) end
    if #raw > limit then return nil, "file exceeds byte limit: " .. path end
    return raw
end

local function decode(raw, kind)
    -- 解析时兼容 Windows 编辑器写入的 UTF-8 BOM。摘要仍按原始字节计算。
    if raw:sub(1, 3) == "\239\187\191" then raw = raw:sub(4) end
    local root, err = cjson.decode(raw)
    if type(root) ~= "table" or root.v ~= 1 or type(root.d) ~= "table" then
        return nil, kind .. ": expected v=1 and a d object: " .. tostring(err)
    end
    if root.revision ~= nil and (type(root.revision) ~= "string"
        or root.revision == "" or #root.revision > 128) then
        return nil, kind .. ": revision must be a nonempty string (max 128 bytes)"
    end
    return root
end

local function validate(perm, temp, max_entries)
    if perm.revision ~= temp.revision then
        return nil, "perm/temp revision mismatch; waiting for a complete publication"
    end
    local entries, codes, seen = {}, {}, {}
    local counts = { perm = 0, temp = 0 }
    local total, now = 0, publish_time()
    for _, source in ipairs({ { "perm", perm.d }, { "temp", temp.d } }) do
        local kind, data = source[1], source[2]
        for code, entry in pairs(data) do
            if type(code) ~= "string" or #code < 1 or #code > 5
                or not code:match("^[0-9a-z%-]+$") or reserved[code] then
                return nil, kind .. ": invalid/reserved short code: " .. tostring(code)
            end
            if seen[code] then return nil, "duplicate short code across files: " .. code end
            seen[code] = true
            total = total + 1
            if total > max_entries then return nil, "entry limit exceeded" end
            if type(entry) ~= "table" then return nil, kind .. ": invalid entry: " .. code end
            if entry.id ~= nil and entry.id ~= code then return nil, "id/code mismatch: " .. code end
            if entry.lurl ~= nil and type(entry.lurl) ~= "string" then
                return nil, "lurl must be a string: " .. code
            end
            local ok, reason = url_util.check(entry.url)
            if not ok then return nil, "invalid URL for " .. code .. ": " .. tostring(reason) end
            local expires = 0
            if kind == "temp" then
                expires = time_util.parse_iso8601(entry.t)
                if not expires then return nil, "invalid temporary expiry: " .. code end
            elseif entry.t ~= nil and entry.t ~= "0" then
                return nil, "permanent entry has a temporary expiry: " .. code
            end
            if expires == 0 or expires > now then
                -- 将 URL 与绝对到期时间打包在同一个值中。查找时直接读共享字典即可。
                entries[code] = string.format("%.0f\n%s", expires, entry.url)
                codes[#codes + 1] = code
                counts[kind] = counts[kind] + 1
            end
        end
    end
    return { entries = entries, codes = codes, counts = counts, revision = perm.revision }
end

local function data_key(generation, code)
    return "g:" .. generation .. ":" .. code
end

local function cleanup(dict, generation)
    local index_key = "index:" .. generation
    local raw = dict:get(index_key)
    if raw then
        local codes = cjson.decode(raw)
        if type(codes) == "table" then
            for _, code in ipairs(codes) do dict:delete(data_key(generation, code)) end
        end
        dict:delete(index_key)
    end
    dict:delete("meta:" .. generation)
end

local function write(dict, key, value)
    local ok, err = dict:safe_set(key, value, 0)
    if not ok then error("manual shared memory exhausted: " .. tostring(err)) end
end

local function publish(dict, opts, owner)
    if dict:get("reload_lock") ~= owner then return end
    local current = active(dict)
    local pending = dict:get("pending")
    if pending then
        -- 清理工作进程中断后遗留的候选代。若进程在原子切换后才退出，则保留已经生效的那一代。
        if not current or pending ~= current.generation then cleanup(dict, pending) end
        dict:delete("pending")
    end
    if current and current.previous then
        -- 发布期间最多需要容纳两代快照。等旧代的读取宽限期结束并回收后，再装载候选代。
        if publish_now() < current.retire_after then return end
        cleanup(dict, current.previous)
    end

    -- 读取数据前先确认可用的摘要算法。旧配置缺少加载入口时，采用内置入口。
    local load_hashes = config.manual_load_hashes or require("shorturl.hashv.init").load
    local fingerprint = load_hashes()
    local perm_raw, err = read_file(opts.perm_path, opts.max_file_bytes)
    if not perm_raw then error(err) end
    local temp_raw
    temp_raw, err = read_file(opts.temp_path, opts.max_file_bytes)
    if not temp_raw then error(err) end
    -- 文件摘要及装载配置均匹配时，可沿用当前快照。
    local candidate_fingerprint = fingerprint(perm_raw, temp_raw)
    if current and current.fingerprint == candidate_fingerprint
        and current.perm_path == opts.perm_path and current.temp_path == opts.temp_path
        and current.max_file_bytes == opts.max_file_bytes
        and current.max_entries == opts.max_entries then
        return
    end
    local perm, temp
    perm, err = decode(perm_raw, "perm")
    if not perm then error(err) end
    temp, err = decode(temp_raw, "temp")
    if not temp then error(err) end
    local snapshot
    snapshot, err = validate(perm, temp, opts.max_entries)
    if not snapshot then error(err) end
    if dict:get("reload_lock") ~= owner then error("reload lock expired before staging") end

    local added, add_err = dict:safe_add("sequence", 0, 0)
    if not added and add_err ~= "exists" then error("cannot allocate generation counter") end
    local sequence, seq_err = dict:incr("sequence", 1)
    if not sequence then error(seq_err) end
    local generation = tostring(sequence)
    write(dict, "pending", generation)
    local staged, stage_err = pcall(function()
        -- 先写短码清单，以便装载中断后按清单清除候选代数据。
        write(dict, "index:" .. generation, assert(cjson.encode(snapshot.codes)))
        for _, code in ipairs(snapshot.codes) do
            write(dict, data_key(generation, code), snapshot.entries[code])
        end
        if dict:get("reload_lock") ~= owner then error("reload lock expired before commit") end
        local next_active = {
            generation = generation,
            previous = current and current.generation or nil,
            retire_after = publish_now() + RETIRE_GRACE,
            fingerprint = candidate_fingerprint,
            perm_path = opts.perm_path, temp_path = opts.temp_path,
            max_file_bytes = opts.max_file_bytes, max_entries = opts.max_entries,
            perm_count = snapshot.counts.perm, temp_count = snapshot.counts.temp,
            revision = snapshot.revision,
        }
        write(dict, "meta:" .. generation, assert(cjson.encode(next_active)))
        -- active 使用固定大小的数字。若改用长度可变的 JSON，替换时可能先删除旧值再遇到分配失败。
        write(dict, "active", sequence)
    end)
    if not staged then
        cleanup(dict, generation)
        if dict:get("pending") == generation then dict:delete("pending") end
        error(stage_err)
    end
    dict:delete("pending")
    return generation, snapshot.counts.perm, snapshot.counts.temp
end

local function release(dict, owner)
    if dict:get("reload_lock") == owner then dict:delete("reload_lock") end
end

-- 在线程池中完成读取、校验和发布。刷新任务自身释放锁，避免重载时旧定时器退出后留下锁。
function M.publish(opts, owner)
    local dict = ngx.shared.su_manual
    local ok, result, perm_count, temp_count = pcall(publish, dict, opts, owner)
    release(dict, owner)
    if not ok then error(result, 0) end
    return result, perm_count, temp_count
end

local function due(dict, now, interval)
    local next_check = dict:get("next_check") or 0
    -- 周期回调和共享截止时间可能相差数毫秒，避免因此跳过整个检查周期。
    local slack = math.min(0.02, interval / 10)
    return next_check <= now + slack or next_check > now + interval
end

function M.reload(premature)
    if premature or not options then return false end
    local dict = ngx.shared.su_manual
    if not dict then return false end
    local opts = options
    local now = ngx.now()
    if not due(dict, now, opts.interval) then return ready(dict) end
    local owner = tostring(ngx.worker.pid()) .. ":" .. tostring(now)
    local locked = dict:safe_add("reload_lock", owner, 300)
    if not locked then return ready(dict) end
    local ok, generation, perm_count, temp_count = pcall(function()
        now = ngx.now()
        if not due(dict, now, opts.interval) then return end
        write(dict, "next_check", now + opts.interval)
        if not opts.thread_pool then return M.publish(opts, owner) end
        local ran, result, perm, temp = ngx.run_worker_thread(
            opts.thread_pool, "shorturl.manual", "publish", opts, owner)
        if not ran then error(result, 0) end
        return result, perm, temp
    end)
    -- 任务未启动或线程调度失败时，由定时器释放锁。
    release(dict, owner)
    if not ok then
        ngx.log(ngx.ERR, "ShortURL: manual reload rejected; keeping last good snapshot: ", tostring(generation))
    elseif generation then
        ngx.log(ngx.NOTICE, "ShortURL: manual snapshot published, generation=", generation,
            " perm=", perm_count, " temp=", temp_count)
    end
    return ready(dict)
end

-- 从 init_worker 启动。旧站点也可由首次请求补注册，但请求只读取当前快照。
function M.start(opts)
    if timer_registered then return true end
    if not ngx.shared.su_manual then
        ngx.log(ngx.ERR, "ShortURL: manual mode requires lua_shared_dict su_manual")
        return false
    end
    if type(opts.interval) ~= "number" or opts.interval ~= opts.interval
        or opts.interval < 0.1 or opts.interval > 3600
        or type(opts.max_file_bytes) ~= "number" or opts.max_file_bytes < 1
        or opts.max_file_bytes > 64 * 1024 * 1024
        or opts.max_file_bytes ~= math.floor(opts.max_file_bytes)
        or type(opts.max_entries) ~= "number" or opts.max_entries < 1
        or opts.max_entries > 30000 or opts.max_entries ~= math.floor(opts.max_entries)
        or type(opts.perm_path) ~= "string" or type(opts.temp_path) ~= "string"
        or opts.perm_path == "" or opts.temp_path == ""
        or opts.perm_path == opts.temp_path
        or (opts.thread_pool ~= nil and (type(opts.thread_pool) ~= "string" or opts.thread_pool == "")) then
        ngx.log(ngx.ERR, "ShortURL: invalid manual storage configuration")
        return false
    end
    options = opts
    if opts.thread_pool and not ngx.run_worker_thread then
        ngx.log(ngx.ERR, "ShortURL: manual thread pool requires OpenResty with worker thread support")
        return false
    end
    local ok, err = ngx.timer.every(opts.interval, M.reload)
    if not ok then
        ngx.log(ngx.ERR, "ShortURL: manual timer registration failed: ", tostring(err))
        return false
    end
    timer_registered = true
    if not opts.thread_pool then
        ngx.log(ngx.WARN, "ShortURL: manual refresh runs synchronously in the timer without a thread pool")
    end
    ok, err = ngx.timer.at(0, M.reload)
    if not ok then
        ngx.log(ngx.WARN, "ShortURL: initial manual timer failed; periodic timer will retry: ", tostring(err))
    end
    return true
end

function M.init(opts)
    if not timer_registered then
        M.start(options or opts or require("shorturl.runtime").manual_options())
    end
    local dict = ngx.shared.su_manual
    return dict ~= nil and ready(dict)
end

function M.lookup(code)
    local dict = ngx.shared.su_manual
    if not dict or not ready(dict) then return nil, "unavailable" end
    local current = active(dict)
    if not current then return nil, "unavailable" end
    local packed = dict:get(data_key(current.generation, code))
    if not packed then
        -- 读取期间旧代可能已被回收。短码未命中时再查当前代，避免切换造成误报 404。
        local latest = active(dict)
        if not latest then return nil, "unavailable" end
        if latest.generation ~= current.generation then
            packed = dict:get(data_key(latest.generation, code))
        end
    end
    if type(packed) ~= "string" then return nil end
    local separator = packed:find("\n", 1, true)
    if not separator then return nil, "unavailable" end
    local expires = tonumber(packed:sub(1, separator - 1))
    if not expires then return nil, "unavailable" end
    if expires ~= 0 and expires <= ngx.time() then return nil end
    return packed:sub(separator + 1)
end

return M
