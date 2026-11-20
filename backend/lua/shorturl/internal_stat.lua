-- 从 su_meta 共享字典读取计数，由 Go 服务通过 GET /internal/stat 调用。
--
-- internal_set 在新增时递增计数，internal_delete 在删除时递减，expire_sweep 负责冷热对账。
-- 条目过期、被淘汰或提交中断时，计数可能暂时偏离，下一轮成功对账后恢复。
-- 冷启动会根据 JSON 文件重新统计。

local cjson = require "cjson.safe"

local M = {}

function M.handle()
    local su_meta = ngx.shared.su_meta

    local perm_count = tonumber(su_meta:get("perm_count")) or 0
    local temp_count = tonumber(su_meta:get("temp_count")) or 0

    ngx.header["Content-Type"] = "application/json"
    ngx.say(cjson.encode({
        perm_count = perm_count,
        temp_count = temp_count
    }))
    ngx.exit(200)
end

return M
