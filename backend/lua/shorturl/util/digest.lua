-- 通过 OpenSSL EVP 接口计算摘要，供 hashv 目录中的内置算法共用。
local ffi = require "ffi"
local M = {}

ffi.cdef [[
    struct evp_md_st;
    struct engine_st;
    const struct evp_md_st *EVP_get_digestbyname(const char *name);
    int EVP_Digest(const void *data, size_t count, unsigned char *md,
        unsigned int *size, const struct evp_md_st *type, struct engine_st *impl);
]]

local libraries = {
    Windows = { "libcrypto-3-x64.dll", "libcrypto-3-arm64.dll", "libcrypto-3.dll",
        "libcrypto-1_1-x64.dll", "libcrypto-1_1.dll" },
    OSX = { "libcrypto.3.dylib", "libcrypto.1.1.dylib", "crypto" },
}
local crypto
local methods = {} -- 缓存已查到的算法描述符，供同一工作进程复用。

local function has_digest_api(lib)
    local ok, available = pcall(function()
        return lib.EVP_get_digestbyname ~= nil and lib.EVP_Digest ~= nil
    end)
    return ok and available
end

local function load_crypto()
    -- 优先使用 OpenResty 已导出的 OpenSSL 接口，包括部分静态构建。否则尝试加载动态库。
    if has_digest_api(ffi.C) then return ffi.C end
    local names = libraries[ffi.os] or { "libcrypto.so.3", "libcrypto.so.1.1", "crypto" }
    for _, name in ipairs(names) do
        local ok, lib = pcall(ffi.load, name)
        if ok and has_digest_api(lib) then return lib end
    end
end

--- 计算原始字节的 256 位摘要，返回小写十六进制字符串。失败时返回 nil 和错误信息。
function M.hex(algorithm, raw)
    crypto = crypto or load_crypto()
    if not crypto then
        return nil, "manual digest requires an OpenSSL libcrypto with support for " .. algorithm
    end
    local method = methods[algorithm]
    if not method then
        method = crypto.EVP_get_digestbyname(algorithm)
        if method == nil then return nil, "OpenSSL digest unavailable: " .. algorithm end
        methods[algorithm] = method
    end

    -- 按 EVP_MAX_MD_SIZE 分配输出缓冲区，并检查实际摘要为 32 字节。
    local output = ffi.new("unsigned char[64]")
    local length = ffi.new("unsigned int[1]")
    -- 显式传入字节数，保证内含零字节的数据也能完整参与计算。
    if crypto.EVP_Digest(raw, #raw, output, length, method, nil) ~= 1 or length[0] ~= 32 then
        return nil, "OpenSSL digest failed: " .. algorithm
    end
    return (ffi.string(output, length[0]):gsub(".", function(byte)
        return string.format("%02x", byte:byte())
    end))
end

return M
