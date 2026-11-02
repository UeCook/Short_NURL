-- url.lua - 目标链接统一校验（与 Go internal/urlguard 对齐）
--
-- 使用方：internal_set（内部写入）、init（冷启动）、expire_sweep（恢复）、
-- redirect（跳转前复核）。这里不做 DNS 解析：Lua 侧没有解析器，域名解析
-- 检查由 Go 创建路径完成；本模块只拦截字面量私网/保留地址、保留域名，
-- 以及浏览器会解释为 IPv4 的非标准数字写法。
--
-- IP 字面量按浏览器（WHATWG URL）规则解析，与 Go urlguard.parseBrowserIPv4
-- 保持同一套判定；IPv6 先展开为 16 字节再按前缀判断，不再依赖字符串前缀匹配，
-- 因此 [0:0:0:0:0:0:0:0001]、[::ffff:7f00:1] 之类等价写法也能命中。

local M = {}

local MAX_LEN = 2048

-- 与 Go urlguard.forbiddenChars 一致：C0 控制字符、DEL、空白、双引号、
-- 尖括号、反斜杠。%z 匹配 \0，\1-\32 覆盖控制字符与空格。
local FORBIDDEN = '[%z\1-\32\127"<>\\]'

local LOCAL_SUFFIXES = { ".localhost", ".local", ".internal", ".home.arpa" }

--- 从 URL 中取出小写主机名；userinfo、端口、IPv6 方括号都按 URL 语义处理。
-- @return string|nil
local function host_of(raw)
    local authority = raw:match("^[%a][%w+.-]*://([^/%?#]*)")
    if not authority or authority == "" then
        return nil
    end
    -- 只取最后一个 @ 之后的部分，丢弃 userinfo。
    local at = authority:match(".*()@")
    if at then
        authority = authority:sub(at + 1)
    end

    local host
    if authority:sub(1, 1) == "[" then
        host = authority:match("^%[([^%]]+)%]")
        if not host then
            return nil
        end
        local rest = authority:sub(#host + 3)
        if rest ~= "" then
            local port = rest:match("^:(%d+)$")
            local n = port and tonumber(port)
            if not n or n < 1 or n > 65535 then
                return nil
            end
        end
    else
        local h, port = authority:match("^([^:]+):(.*)$")
        if h then
            if port == "" or not port:match("^%d+$") then
                return nil
            end
            local n = tonumber(port)
            if not n or n < 1 or n > 65535 then
                return nil
            end
            host = h
        else
            host = authority
        end
    end

    if not host or host == "" then
        return nil
    end
    -- 浏览器会解码主机中的 %xx，并通过 IDNA 映射 Unicode。
    -- 此处只接受 ASCII 主机（国际域名须先转 punycode），避免与浏览器解析不同。
    -- 只检查主机，保留 userinfo、路径、查询中的合法编码和 Unicode。
    if host:find("%", 1, true) or host:find("[^\33-\126]") then
        return nil
    end
    return host:lower()
end

-- ── IP 字面量解析（与 Go urlguard 对齐）──────────────────────────

local function split(s, sep)
    local out, start = {}, 1
    for i = 1, #s do
        if s:sub(i, i) == sep then
            out[#out + 1] = s:sub(start, i - 1)
            start = i + 1
        end
    end
    out[#out + 1] = s:sub(start)
    return out
end

--- 解析 base 进制数字串；空串、非法字符与溢出返回 nil。
local function parse_digits(str, base, max)
    if str == "" then
        return nil
    end
    local v = 0
    for i = 1, #str do
        local c = str:byte(i)
        local d
        if c >= 48 and c <= 57 then
            d = c - 48
        elseif c >= 97 and c <= 102 then
            d = c - 87
        else
            return nil
        end
        if d >= base then
            return nil
        end
        v = v * base + d
        if v > max then
            return nil
        end
    end
    return v
end

--- 按浏览器（WHATWG URL）规则解析 IPv4：1~4 段，十进制/八进制（前导 0）/
--- 十六进制（0x），最后一段可跨字节。与 Go urlguard.parseBrowserIPv4 一致。
-- @return number|nil a, b  （只返回前两段，供网段判定使用）
local function parse_browser_ipv4(host)
    local h = host:lower()
    if h == "" then
        return nil
    end
    local parts = split(h, ".")
    if #parts > 1 and parts[#parts] == "" then
        parts[#parts] = nil
    end
    if #parts == 0 or #parts > 4 then
        return nil
    end

    local nums = {}
    for i = 1, #parts do
        local p = parts[i]
        if p == "" then
            return nil
        end
        local base, digits = 10, p
        if p:sub(1, 2) == "0x" then
            base, digits = 16, p:sub(3)
            if digits == "" then
                digits = "0"
            end
        elseif #p > 1 and p:sub(1, 1) == "0" then
            base, digits = 8, p:sub(2)
        end
        local v = parse_digits(digits, base, 4294967295)
        if not v then
            return nil
        end
        nums[i] = v
    end

    local n = #nums
    -- 最后一段可覆盖剩余字节，上限 256^(5-n)。
    if nums[n] >= 2 ^ (8 * (5 - n)) then
        return nil
    end
    local value = nums[n]
    for i = 1, n - 1 do
        if nums[i] > 255 then
            return nil
        end
        value = value + nums[i] * 2 ^ (8 * (4 - i))
    end
    if value > 4294967295 then
        return nil
    end
    return math.floor(value / 16777216) % 256,
        math.floor(value / 65536) % 256,
        math.floor(value / 256) % 256,
        value % 256
end

--- 解析 IPv6 分组；入参不含 "::"，空串表示无分组。失败返回 nil。
local function parse_v6_groups(s)
    local out = {}
    if s == "" then
        return out
    end
    local parts = split(s, ":")
    for i = 1, #parts do
        local p = parts[i]
        if p == "" then
            return nil
        end
        if i == #parts and p:find(".", 1, true) then
            -- 末尾内嵌 IPv4（RFC 4291）
            local a, b, c, d = p:match("^(%d+)%.(%d+)%.(%d+)%.(%d+)$")
            if not a then
                return nil
            end
            a, b, c, d = tonumber(a), tonumber(b), tonumber(c), tonumber(d)
            if a > 255 or b > 255 or c > 255 or d > 255 then
                return nil
            end
            out[#out + 1] = a * 256 + b
            out[#out + 1] = c * 256 + d
        else
            if #p == 0 or #p > 4 or not p:match("^%x+$") then
                return nil
            end
            out[#out + 1] = tonumber(p, 16)
        end
    end
    return out
end

--- 解析 IPv6 字面量为 16 字节数组；失败返回 nil。
local function parse_ipv6(host)
    local h = host:lower()
    if h == "" then
        return nil
    end
    local first = h:find("::", 1, true)
    if first and h:find("::", first + 1, true) then
        return nil -- 多个 "::" 非法
    end

    local groups, tail
    if first then
        groups = parse_v6_groups(h:sub(1, first - 1))
        tail = parse_v6_groups(h:sub(first + 2))
        if not groups or not tail then
            return nil
        end
        if #groups + #tail > 7 then
            return nil -- "::" 至少要压缩一组
        end
        for _ = 1, 8 - #groups - #tail do
            groups[#groups + 1] = 0
        end
        for i = 1, #tail do
            groups[#groups + 1] = tail[i]
        end
    else
        groups = parse_v6_groups(h)
        if not groups or #groups ~= 8 then
            return nil
        end
    end

    local bytes = {}
    for i = 1, 8 do
        bytes[#bytes + 1] = math.floor(groups[i] / 256)
        bytes[#bytes + 1] = groups[i] % 256
    end
    return bytes
end

local function all_zero(bytes, from, to)
    for i = from, to do
        if bytes[i] ~= 0 then
            return false
        end
    end
    return true
end

-- 与 Go urlguard.isPrivateIP 的 IPv4 分支对齐。
local function is_private_ipv4(a, b)
    if a == 0 or a == 10 or a == 127 then return true end
    if a == 100 and b >= 64 and b <= 127 then return true end -- CGNAT 100.64/10
    if a == 169 and b == 254 then return true end             -- 链路本地
    if a == 172 and b >= 16 and b <= 31 then return true end  -- 172.16/12
    if a == 192 and b == 168 then return true end             -- 192.168/16
    if a >= 224 then return true end                          -- 组播 224/4 + 保留 240/4
    return false
end

--- IPv6 私网/保留判定（入参为 16 字节数组）。
local function is_private_ipv6(b)
    if all_zero(b, 1, 16) then
        return true -- ::
    end
    if b[16] == 1 and all_zero(b, 1, 15) then
        return true -- ::1
    end
    -- ::ffff:a.b.c.d（IPv4-mapped）
    if b[11] == 255 and b[12] == 255 and all_zero(b, 1, 10) then
        return is_private_ipv4(b[13], b[14])
    end
    if b[1] == 254 and b[2] >= 128 and b[2] <= 191 then
        return true -- fe80::/10
    end
    if b[1] >= 252 and b[1] <= 253 then
        return true -- fc00::/7
    end
    if b[1] == 255 then
        return true -- ff00::/8
    end
    return false
end

local function is_private_host(host)
    local v6 = parse_ipv6(host)
    if v6 then
        return is_private_ipv6(v6)
    end
    if host:find(":", 1, true) then
        -- 含冒号却不是合法 IPv6：无法安全判断，失败关闭（与 Go 一致）。
        return true
    end

    local a, b = parse_browser_ipv4(host)
    if a then
        return is_private_ipv4(a, b)
    end

    local h = host:gsub("%.$", ""):lower()
    if h == "localhost" then
        return true
    end
    for _, suffix in ipairs(LOCAL_SUFFIXES) do
        if #h > #suffix and h:sub(-#suffix) == suffix then
            return true
        end
    end

    -- 其余浏览器会当作 IPv4 的数字写法（如 1.2.3.4.5、畸形点分形式）。
    if h:match("^0x%x+$") or h:match("^%d+$") or h:match("^[%d%.]+$") then
        return true
    end
    return false
end

--- 校验目标链接。
-- @param raw string
-- @return boolean ok, string|nil reason
function M.check(raw)
    if type(raw) ~= "string" or raw == "" then
        return false, "empty url"
    end
    if #raw > MAX_LEN then
        return false, "url too long"
    end
    if not raw:lower():match("^https?://") then
        return false, "invalid scheme"
    end
    if raw:find(FORBIDDEN) then
        return false, "forbidden character"
    end

    local host = host_of(raw)
    if not host then
        return false, "missing host"
    end
    if is_private_host(host) then
        return false, "private address"
    end
    return true
end

return M
