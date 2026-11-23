package.path = "backend/lua/?.lua;" .. package.path
local guard = require "shorturl.util.url"
local count = 0
for line in io.lines("tests/fixtures/url-contract.tsv") do
    if line ~= "" and line:sub(1, 1) ~= "#" then
        local expected, raw = line:match("^([01])\t(.+)$")
        assert(expected, "invalid contract fixture: " .. line)
        local ok, reason = guard.check(raw)
        assert(ok == (expected == "1"), "URL contract mismatch: " .. raw .. " " .. tostring(reason))
        count = count + 1
    end
end
print("PASS: " .. count .. " shared URL contract cases")
