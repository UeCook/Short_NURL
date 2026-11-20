-- 从 su_url 和 su_exp 共享字典删除热存储条目。
-- Go 服务通过 POST /internal/delete 调用。处理器按条目类型更新 su_meta 中的计数。
--
-- 优先用 Go 从冷存储查到的类型。调用未提供类型时，通过 su_exp 推断。
-- 两处均无类型时，跳过计数递减并记录警告。

local cjson = require "cjson.safe"

local M = {}

function M.handle()
    ngx.req.read_body()
    local body = ngx.req.get_body_data()
    if not body then
        -- 请求体超过 client_body_buffer_size 时内容落入临时文件，
        -- get_body_data() 返回 nil，需回退 get_body_file() 读取，否则误报 "empty body"
        local fname = ngx.req.get_body_file()
        if fname then
            local f = io.open(fname, "rb")
            if f then
                body = f:read("*a")
                f:close()
            end
        end
    end

    if not body then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"empty body"}')
        ngx.exit(400)
        return
    end

    local params = cjson.decode(body)
    if type(params) ~= "table" or not params.code then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"missing code"}')
        ngx.exit(400)
        return
    end

    local code = params.code
    local forced_type = params.type  -- Go 提供的类型为 "perm" 或 "temp"。

    -- Lua 侧独立验证内部 API 的输入。
    -- 短码须符合 ^[0-9a-z-]{1,5}$。Lua 模式通过可选字符展开长度范围。
    if type(code) ~= "string"
       or not code:match("^[0-9a-z%-][0-9a-z%-]?[0-9a-z%-]?[0-9a-z%-]?[0-9a-z%-]?$") then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid code"}')
        ngx.exit(400)
        return
    end

    -- type：只能是 nil（降级回退）或 "perm" 或 "temp"，杜绝非对称分支被滥用
    if forced_type ~= nil and forced_type ~= "perm" and forced_type ~= "temp" then
        ngx.status = 400
        ngx.header["Content-Type"] = "application/json"
        ngx.say('{"error":"invalid type"}')
        ngx.exit(400)
        return
    end
    -- ── 验证结束 ──────────────────────────────────────────────

    local su_url = ngx.shared.su_url
    local su_exp = ngx.shared.su_exp
    local su_meta = ngx.shared.su_meta

    -- 共享字典重建或 LRU 淘汰后，su_exp 可能缺失，因此优先采用调用方提供的类型。
    local count_key = nil
    local source = nil

    if forced_type then
        -- 直接使用 Go 从冷存储查到的类型。
        count_key = (forced_type == "perm") and "perm_count" or "temp_count"
        source = "php:" .. forced_type
    else
        local exp_str = su_exp:get(code)
        if exp_str then
            if exp_str == "0" then
                count_key = "perm_count"
            else
                count_key = "temp_count"
            end
            source = "su_exp"
        end
    end

    local existed = su_url:get(code) ~= nil or su_exp:get(code) ~= nil
    if count_key and existed then
        local new_val, err = su_meta:incr(count_key, -1, 0)
        if not new_val then
            ngx.log(ngx.WARN, "ShortURL: delete incr failed for ", count_key, ": ", tostring(err))
        elseif new_val < 0 then
            su_meta:set(count_key, 0)
        end
    elseif not count_key then
        -- 无法判断条目类型时跳过计数递减。
        ngx.log(ngx.WARN, "ShortURL: delete - cannot determine type for code=", code,
            ", skipping count decrement (su_exp missing, no PHP type hint)")
    end

    su_url:delete(code)
    su_exp:delete(code)

    ngx.header["Content-Type"] = "application/json"
    ngx.say('{"ok":true}')
    ngx.exit(200)
end

return M
