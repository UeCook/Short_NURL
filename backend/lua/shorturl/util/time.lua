-- time.lua - ISO 8601 formatting and comparison utilities
-- All timestamps use ISO 8601 extended format with timezone offset

local M = {}

--- Get the local system timezone offset in seconds (cached)
-- Uses os.date("!*t") vs os.date("*t") to compute the difference.
-- Cached after first call since timezone doesn't change during process lifetime.
-- @return number  offset in seconds (e.g. 28800 for UTC+8)
local function get_local_tz_offset()
    if M._tz_offset then return M._tz_offset end
    local now = os.time()
    local utc_t = os.date("!*t", now)
    local local_t = os.date("*t", now)
    utc_t.isdst = false
    local_t.isdst = false
    M._tz_offset = os.difftime(os.time(local_t), os.time(utc_t))
    return M._tz_offset
end

--- Format a unix timestamp to ISO 8601 string with timezone offset
-- @param ts   number  unix timestamp (seconds)
-- @param tz   string  timezone offset e.g. "+08:00"
-- @return     string  ISO 8601 string e.g. "2025-06-17T08:00:00+08:00"
function M.format_iso8601(ts, tz)
    tz = tz or "+08:00"

    -- Parse timezone offset to seconds
    local sign, hh, mm = tz:match("([+-])(%d%d):(%d%d)")
    if not sign then return "0" end
    local offset_sec = tonumber(hh) * 3600 + tonumber(mm) * 60
    if sign == "-" then offset_sec = -offset_sec end

    -- Apply offset to get local time
    local local_ts = ts + offset_sec
    local d = os.date("!%Y-%m-%dT%H:%M:%S", local_ts)

    return d .. tz
end

--- Get current time as ISO 8601 string
-- @param tz   string  timezone offset e.g. "+08:00"
-- @return     string  ISO 8601 string
function M.now_iso8601(tz)
    tz = tz or "+08:00"
    return M.format_iso8601(ngx.time(), tz)
end

--- Parse ISO 8601 string to unix timestamp (UTC epoch)
-- @param s    string  ISO 8601 string e.g. "2025-06-17T08:00:00+08:00"
-- @return     number  unix timestamp (UTC), or nil on failure
--
-- Note: os.time() interprets its argument as local time, not UTC.
-- We compensate by computing the local timezone offset and adjusting.
function M.parse_iso8601(s)
    if not s or s == "0" then return nil end

    local year, month, day, hour, min, sec, sign, oh, om =
        s:match("^(%d%d%d%d)%-(%d%d)%-(%d%d)T(%d%d):(%d%d):(%d%d)([+-])(%d%d):(%d%d)$")
    if not year then return nil end

    -- Build time table from the ISO string components
    local time_table = {
        year = tonumber(year), month = tonumber(month), day = tonumber(day),
        hour = tonumber(hour), min = tonumber(min), sec = tonumber(sec),
        isdst = false
    }
    -- os.time() interprets this as local time; compensate to get UTC epoch
    local epoch = os.time(time_table) + get_local_tz_offset()

    -- Apply the timezone offset from the ISO string to convert to UTC
    local offset = tonumber(oh) * 3600 + tonumber(om) * 60
    if sign == "+" then epoch = epoch - offset
    else epoch = epoch + offset end

    return epoch
end

return M
