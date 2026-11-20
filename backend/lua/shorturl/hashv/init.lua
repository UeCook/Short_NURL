-- 手工模式的摘要加载入口。支持单个算法或两种算法同时参与变更检测。
local M = {}

-- 从加载器自身的路径定位算法文件，避免从 package.path 中的其他安装目录补齐已移除的算法。
local directory = assert(debug.getinfo(1, "S").source:match("^@(.*[/\\])"),
    "cannot locate manual hashv directory")
local supported = {
    { name = "sha3-256", filename = "sha3_256.lua" },
    { name = "sm3", filename = "sm3.lua" },
}
local ENOENT = 2 -- 支持的平台上用于标识文件或目录不存在。
local algorithms

-- 只探测支持列表中的文件。单个文件缺失可继续，读取失败或模块无效则拒绝刷新。
local function load_algorithms()
    local loaded = {}
    for _, entry in ipairs(supported) do
        local path = directory .. entry.filename
        local file, err, errno = io.open(path, "rb")
        if file then
            file:close()
            local hash = assert(loadfile(path))()
            assert(type(hash) == "function", path .. " must return a digest function")
            loaded[#loaded + 1] = { name = entry.name, hash = hash }
        elseif errno ~= ENOENT then
            error(err)
        end
    end
    if #loaded == 0 then
        error("manual hash modules missing: expected sha3_256.lua or sm3.lua in " .. directory)
    end
    return loaded
end

-- 同名替换模块接收原始字节，返回 64 个十六进制字符。统一为小写后再比较。
local function checksum(algorithm, raw)
    local hex, err = algorithm.hash(raw)
    assert(hex, err or algorithm.name .. " digest failed")
    assert(type(hex) == "string" and #hex == 64 and hex:match("^%x+$"),
        algorithm.name .. " digest must be 64 hexadecimal characters")
    return hex:lower()
end

-- 先记录算法组合，再按固定顺序为两份 JSON 分别计算摘要。所有摘要均匹配才视为未变更。
local function fingerprint(perm_raw, temp_raw)
    local parts = { #algorithms == 2 and "dual" or algorithms[1].name }
    for _, algorithm in ipairs(algorithms) do
        parts[#parts + 1] = checksum(algorithm, perm_raw)
        parts[#parts + 1] = checksum(algorithm, temp_raw)
    end
    return table.concat(parts, ":")
end

-- 成功加载后在当前工作进程内复用。重载 OpenResty 后，新进程按文件现状重新选择算法。
function M.load()
    algorithms = algorithms or load_algorithms()
    return fingerprint
end

return M
