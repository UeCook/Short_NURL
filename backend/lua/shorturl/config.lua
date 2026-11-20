-- Lua 模块的运行配置。
--
-- 工作进程收到站点请求后，由 access_by_lua_block 触发清理定时器注册。
-- 托管模式优先使用 Nginx 的 $su_expire_interval，注册前会将其存入 su_meta。
-- su_meta 未提供有效周期时，定时器初始化会读取下方的默认值。
--
-- 部署时可通过站点配置中的 $su_expire_interval 指定清理周期。

local M = {
    -- Nginx 的 $su_mode 优先。managed 为 Go 托管模式，manual 只读 JSON。
    mode = "managed",
    expire_interval = 3600,
    manual_reload_interval = 5,
    manual_max_file_bytes = 16 * 1024 * 1024,
    manual_max_entries = 30000,
    -- 刷新线程加载此配置，再按 hashv 中的算法文件选择摘要组合。
    manual_load_hashes = function() return require("shorturl.hashv.init").load() end,

    -- 各工作进程通过 su_blocklist 共用限额。查找短码前先扣全局额度，
    -- 有效跳转也计入额度，因此轮换来源 IP 无法绕过全局限额。
    redirect_guard = {
        enabled = true,
        global_requests_per_second = 100,
        miss_window_seconds = 60,
        max_misses = 20,
        ban_seconds = 900,
    },
}

return M
