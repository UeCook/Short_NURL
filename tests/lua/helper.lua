-- Deterministic shared-dict/time double. The integration test uses real OpenResty.
local M = {}

function M.new()
    local env = { now = 1800000000, timers = {}, logs = {}, response = {} }
    local function dictionary()
        local dict = { values = {}, unsafe_writes = 0 }
        function dict:get(key)
            if self.get_hook then self.get_hook(key) end
            local item = self.values[key]
            if item and item.expires and item.expires <= env.now then
                self.values[key], item = nil, nil
            end
            return item and item.value
        end
        function dict:safe_set(key, value, ttl)
            if self.fail and self.fail(key, value) then return nil, "no memory" end
            self.values[key] = { value = value, expires = ttl and ttl > 0 and env.now + ttl or nil }
            return true
        end
        function dict:set(key, value, ttl)
            self.unsafe_writes = self.unsafe_writes + 1
            return self:safe_set(key, value, ttl)
        end
        function dict:safe_add(key, value, ttl)
            if self:get(key) ~= nil then return nil, "exists" end
            return self:safe_set(key, value, ttl)
        end
        dict.add = dict.safe_add
        function dict:incr(key, amount)
            local value = self:get(key)
            if value == nil then return nil, "not found" end
            if type(value) ~= "number" then return nil, "not a number" end
            self.values[key].value = value + amount
            return value + amount
        end
        function dict:ttl(key)
            if self:get(key) == nil then return nil, "not found" end
            local expires = self.values[key].expires
            return expires and expires - env.now or 0
        end
        function dict:delete(key) self.values[key] = nil end
        return dict
    end
    env.ngx = {
        ERR = "ERR", WARN = "WARN", NOTICE = "NOTICE",
        time = function() return math.floor(env.now) end,
        now = function() return env.now end,
        worker = { pid = function() return env.worker_id or 1 end },
        shared = { su_manual = dictionary(), su_meta = dictionary(),
            su_blocklist = dictionary(), su_url = dictionary(), su_exp = dictionary() },
        ctx = {}, header = {},
        var = { remote_addr = "198.51.100.1", su_mode = "managed", [1] = "abc" },
        req = { get_method = function() return env.method or "GET" end },
        timer = { every = function(interval, callback)
            if env.timer_error then return nil, "timer allocation failed" end
            env.timers[#env.timers + 1] = { interval = interval, callback = callback }
            return true
        end },
        -- Compare contents directly in unit tests. Real MD5 is exercised by
        -- tests/interop/openresty.test.cjs, including edits of equal byte length.
        md5 = function(raw) return raw end,
        log = function(...)
            local fields = { ... }
            for i = 1, #fields do fields[i] = tostring(fields[i]) end
            env.logs[#env.logs + 1] = table.concat(fields)
        end,
        exit = function(status) env.response.status = status; return status end,
        redirect = function(url, status)
            env.response.status, env.response.location = status, url
            return status
        end,
        say = function(body) env.response.body = body end,
    }
    function env:advance(seconds) self.now = self.now + seconds end
    function env:request(ip, code, method)
        self.ngx.var.remote_addr, self.ngx.var[1], self.method = ip, code, method or "GET"
        self.ngx.header, self.response = {}, {}
    end
    _G.ngx = env.ngx
    return env
end

function M.module(name)
    package.loaded[name] = nil
    return require(name)
end

return M
