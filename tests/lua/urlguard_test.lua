package.path = "backend/lua/?.lua;" .. package.path
local guard = require "shorturl.util.url"
local denied = {
    "http://l%6fcalhost/",
    "http://%31%32%37.0.0.1/",
    "http://127%2e0%2e0%2e1/",
    "http://localhost%2e/",
    "http://user:pass@l%6fcalhost:8080/",
    "http://ｌｏｃａｌｈｏｓｔ/",
    "http://127。0。0。1/",
    "http://[0:0:0:0:0:0:0:0001]/",
    "http://[::ffff:7f00:1]/",
    "http://0177.0.0.1/",
    "http://0x7f.0.0.1/",
    "http://[fe80::1%25eth0]/",
}
local allowed = {
    "https://example.com/a%20b?q=%31%32%37#%20",
    "https://user%40name:pass%25word@example.com/path",
    "https://example.com/中文?q=中文",
    "https://xn--fiqs8s.example/",
    "http://8.8.8.8/",
    "http://[2606:4700:4700::1111]/",
}
local failed = 0
for _, raw in ipairs(denied) do
    if guard.check(raw) then
        print("FAIL accepted: " .. raw)
        failed = failed + 1
    end
end
for _, raw in ipairs(allowed) do
    local ok, reason = guard.check(raw)
    if not ok then
        print("FAIL rejected: " .. raw .. " " .. tostring(reason))
        failed = failed + 1
    end
end
assert(failed == 0, tostring(failed) .. " URL validation failures")
print("PASS: " .. (#denied + #allowed) .. " Lua URL regression cases")
