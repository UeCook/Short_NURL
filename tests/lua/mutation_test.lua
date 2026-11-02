package.path = "backend/lua/?.lua;" .. package.path
local params
package.preload["cjson.safe"] = function()
    return { decode = function() return params end }
end

local marker_ok, marker_ttl, marker_key, response
ngx = {
    ERR = "error",
    req = {
        read_body = function() end,
        get_body_data = function() return "{}" end,
    },
    shared = { su_meta = {
        safe_set = function(_, key, value, ttl)
            assert(value == 1)
            marker_key, marker_ttl = key, ttl
            if marker_ok then return true end
            return nil, "no memory"
        end,
        set = function() error("must not evict other mutation markers") end,
    } },
    log = function() end,
    say = function(body) response = body end,
    exit = function(status) return status end,
}
local handler = require "shorturl.internal_mutation"
for _, action in ipairs({"begin", "end"}) do
    for _, succeeds in ipairs({true, false}) do
        params = { code = "abc", action = action }
        marker_ok, marker_key, marker_ttl, response = succeeds, nil, nil, nil
        ngx.header, ngx.status = {}, 200
        local status = handler.handle()
        assert(marker_key == "mutation:abc")
        assert(marker_ttl == ((action == "begin") and 120 or 60))
        if succeeds then
            assert(status == 200 and response == '{"ok":true}')
        else
            assert(status == 503 and ngx.status == 503)
            assert(response == '{"error":"mutation marker unavailable"}')
        end
    end
end
print("PASS: mutation begin/end success and allocation failure")
