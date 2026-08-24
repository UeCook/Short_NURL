-- time.lua - ISO 8601 formatting and comparison utilities

local M = {}

--- Convert a Gregorian calendar date interpreted as UTC to Unix epoch seconds.
-- This avoids os.time(), whose interpretation depends on the host timezone/DST.
local function days_from_civil(year, month, day)
    year = year - ((month <= 2) and 1 or 0)
    local era = math.floor(year / 400)
    local yoe = year - era * 400
    local mp = month + ((month > 2) and -3 or 9)
    local doy = math.floor((153 * mp + 2) / 5) + day - 1
    local doe = yoe * 365 + math.floor(yoe / 4) - math.floor(yoe / 100) + doy
    return era * 146097 + doe - 719468
end

local function is_leap_year(year)
    return (year % 4 == 0 and year % 100 ~= 0) or year % 400 == 0
end

local function days_in_month(year, month)
    local days = { 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31 }
    if month == 2 and is_leap_year(year) then return 29 end
    return days[month]
end

--- Format a unix timestamp to ISO 8601 string with timezone offset.
function M.format_iso8601(ts, tz)
    tz = tz or "+08:00"
    local sign, hh, mm = tz:match("^([+-])(%d%d):(%d%d)$")
    if not sign then return "0" end
    local offset_sec = tonumber(hh) * 3600 + tonumber(mm) * 60
    if sign == "-" then offset_sec = -offset_sec end
    local local_ts = ts + offset_sec
    local d = os.date("!%Y-%m-%dT%H:%M:%S", local_ts)
    return d .. tz
end

function M.now_iso8601(tz)
    return M.format_iso8601(ngx.time(), tz or "+08:00")
end

--- Parse an ISO 8601 timestamp with a numeric offset into UTC epoch seconds.
function M.parse_iso8601(s)
    if not s or s == "0" then return nil end

    local year, month, day, hour, min, sec, sign, oh, om =
        s:match("^(%d%d%d%d)%-(%d%d)%-(%d%d)T(%d%d):(%d%d):(%d%d)([+-])(%d%d):(%d%d)$")
    if not year then return nil end

    year, month, day = tonumber(year), tonumber(month), tonumber(day)
    hour, min, sec = tonumber(hour), tonumber(min), tonumber(sec)
    oh, om = tonumber(oh), tonumber(om)

    if month < 1 or month > 12 or day < 1 or day > days_in_month(year, month)
        or hour > 23 or min > 59 or sec > 59
        or oh > 14 or om > 59 or (oh == 14 and om ~= 0) then
        return nil
    end

    local offset = oh * 3600 + om * 60
    if sign == "-" then offset = -offset end
    return days_from_civil(year, month, day) * 86400 + hour * 3600 + min * 60 + sec - offset
end

return M
