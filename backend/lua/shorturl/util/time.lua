-- time.lua - ISO 8601 formatting and comparison utilities
-- All timestamps use ISO 8601 extended format with timezone offset

local M = {}

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

--- Parse ISO 8601 string to unix timestamp
-- @param s    string  ISO 8601 string e.g. "2025-06-17T08:00:00+08:00"
-- @return     number  unix timestamp, or nil on failure
--
-- WARNING: os.time() interprets its argument as local time, not UTC.
-- On servers with non-UTC system timezone (e.g. Asia/Shanghai), the returned
-- epoch will be offset by the local timezone difference. This is currently
-- harmless because expire_sweep uses string comparison, not this function.
-- If future code depends on accurate epoch values, either:
--   1. Set server TZ to UTC (export TZ=UTC), or
--   2. Compensate with local_tz_offset() subtraction.
function M.parse_iso8601(s)
    if not s or s == "0" then return nil end

    local year, month, day, hour, min, sec, sign, oh, om =
        s:match("^(%d%d%d%d)%-(%d%d)%-(%d%d)T(%d%d):(%d%d):(%d%d)([+-])(%d%d):(%d%d)$")
    if not year then return nil end

    -- Convert to epoch using os.time in UTC
    local utc_time = {
        year = tonumber(year), month = tonumber(month), day = tonumber(day),
        hour = tonumber(hour), min = tonumber(min), sec = tonumber(sec),
        isdst = false
    }
    local epoch = os.time(utc_time)

    -- Apply timezone offset
    local offset = tonumber(oh) * 3600 + tonumber(om) * 60
    if sign == "+" then epoch = epoch - offset
    else epoch = epoch + offset end

    return epoch
end

return M
