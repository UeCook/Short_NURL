-- Read-only JSON operation without Go, API keys, or internal HTTP endpoints.
-- Each publication is immutable. A single shared-dict value selects the active
-- generation; safe_set never evicts the previous working snapshot to stage a new one.
local cjson = require "cjson.safe"
local time_util = require "shorturl.util.time"
local url_util = require "shorturl.util.url"
local M = {}

local options, timer_registered
local cached_generation, cached_active
local RETIRE_GRACE = 1
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
    -- Accept UTF-8 BOM from Windows editors; change detection still uses raw bytes.
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

local function validate(perm, temp)
    if perm.revision ~= temp.revision then
        return nil, "perm/temp revision mismatch; waiting for a complete publication"
    end
    local entries, codes, seen = {}, {}, {}
    local counts = { perm = 0, temp = 0 }
    local total, now = 0, ngx.time()
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
            if total > options.max_entries then return nil, "entry limit exceeded" end
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
                -- One value keeps URL and absolute expiry consistent; no JSON
                -- decoding or filesystem access is needed for each redirect.
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

local function publish(dict, owner)
    local current = active(dict)
    local pending = dict:get("pending")
    if pending then
        -- Recover staging abandoned by a worker. A worker may also exit just
        -- after the atomic commit, so never remove an active generation.
        if not current or pending ~= current.generation then cleanup(dict, pending) end
        dict:delete("pending")
    end
    if current and current.previous then
        -- At most two generations need to fit. Defer another publication until
        -- readers have had a grace period to finish the previous one.
        if ngx.now() < current.retire_after then return end
        cleanup(dict, current.previous)
    end

    local perm_raw, err = read_file(options.perm_path, options.max_file_bytes)
    if not perm_raw then error(err) end
    local temp_raw
    temp_raw, err = read_file(options.temp_path, options.max_file_bytes)
    if not temp_raw then error(err) end
    -- Fingerprints are only change detectors, not authentication of input.
    local signature = ngx.md5(perm_raw) .. ngx.md5(temp_raw)
    if current and current.signature == signature
        and current.perm_path == options.perm_path and current.temp_path == options.temp_path
        and current.max_file_bytes == options.max_file_bytes
        and current.max_entries == options.max_entries then
        return
    end
    local perm, temp
    perm, err = decode(perm_raw, "perm")
    if not perm then error(err) end
    temp, err = decode(temp_raw, "temp")
    if not temp then error(err) end
    local snapshot
    snapshot, err = validate(perm, temp)
    if not snapshot then error(err) end
    if dict:get("reload_lock") ~= owner then error("reload lock expired before staging") end

    local added, add_err = dict:safe_add("sequence", 0, 0)
    if not added and add_err ~= "exists" then error("cannot allocate generation counter") end
    local sequence, seq_err = dict:incr("sequence", 1)
    if not sequence then error(seq_err) end
    local generation = tostring(sequence)
    write(dict, "pending", generation)
    local staged, stage_err = pcall(function()
        -- The manifest precedes data so an interrupted load can be cleaned up.
        write(dict, "index:" .. generation, assert(cjson.encode(snapshot.codes)))
        for _, code in ipairs(snapshot.codes) do
            write(dict, data_key(generation, code), snapshot.entries[code])
        end
        if dict:get("reload_lock") ~= owner then error("reload lock expired before commit") end
        local next_active = {
            generation = generation,
            previous = current and current.generation or nil,
            retire_after = ngx.now() + RETIRE_GRACE,
            signature = signature,
            perm_path = options.perm_path, temp_path = options.temp_path,
            max_file_bytes = options.max_file_bytes, max_entries = options.max_entries,
            perm_count = snapshot.counts.perm, temp_count = snapshot.counts.temp,
            revision = snapshot.revision,
        }
        write(dict, "meta:" .. generation, assert(cjson.encode(next_active)))
        -- A number always has the same-sized payload. Replacing a variable-sized
        -- JSON selector could discard the old selector if allocation then failed.
        write(dict, "active", sequence)
    end)
    if not staged then
        cleanup(dict, generation)
        if dict:get("pending") == generation then dict:delete("pending") end
        error(stage_err)
    end
    dict:delete("pending")
    ngx.log(ngx.NOTICE, "ShortURL: manual snapshot published, generation=", generation,
        " perm=", snapshot.counts.perm, " temp=", snapshot.counts.temp)
end

function M.reload(premature)
    if premature or not options then return false end
    local dict = ngx.shared.su_manual
    if not dict then return false end
    if ngx.now() < (dict:get("next_check") or 0) then return ready(dict) end
    local owner = tostring(ngx.worker.pid()) .. ":" .. tostring(ngx.now())
    local locked = dict:safe_add("reload_lock", owner, 300)
    if not locked then return ready(dict) end
    local ok, err = pcall(function()
        write(dict, "next_check", ngx.now() + options.interval)
        publish(dict, owner)
    end)
    if not ok then
        ngx.log(ngx.ERR, "ShortURL: manual reload rejected; keeping last good snapshot: ", tostring(err))
    end
    if dict:get("reload_lock") == owner then dict:delete("reload_lock") end
    return ready(dict)
end

function M.init(opts)
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
        or opts.perm_path == opts.temp_path then
        ngx.log(ngx.ERR, "ShortURL: invalid manual storage configuration")
        return false
    end
    options = opts
    if not timer_registered then
        local ok, err = ngx.timer.every(options.interval, M.reload)
        if ok then timer_registered = true
        else ngx.log(ngx.ERR, "ShortURL: manual timer registration failed: ", tostring(err)) end
    end
    return M.reload(false)
end

function M.lookup(code)
    local dict = ngx.shared.su_manual
    if not dict or not ready(dict) then return nil, "unavailable" end
    local current = active(dict)
    if not current then return nil, "unavailable" end
    local packed = dict:get(data_key(current.generation, code))
    if not packed then
        -- A stalled reader may outlive retired-generation cleanup. Retry the
        -- current generation so a publication cannot introduce a false 404.
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
