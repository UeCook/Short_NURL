-- 与 Go Store 共用锁文件。冷启动和对账不能读取尚未完成持久化的替换结果。
local ffi = require "ffi"
local M = {}
local acquire, close

if ffi.os == "Windows" then
    ffi.cdef [[
        int __stdcall MultiByteToWideChar(unsigned int, unsigned long, const char *, int, uint16_t *, int);
        void *__stdcall CreateFileW(const uint16_t *, unsigned long, unsigned long, void *,
                                    unsigned long, unsigned long, void *);
        int __stdcall CloseHandle(void *);
        unsigned long __stdcall GetLastError(void);
    ]]
    local win = ffi.load("kernel32")
    local invalid = ffi.cast("void *", -1)
    close = win.CloseHandle
    acquire = function(path, exclusive)
        local size = win.MultiByteToWideChar(65001, 8, path, -1, nil, 0)
        if size == 0 then return nil, "invalid lock path" end
        local wide = ffi.new("uint16_t[?]", size)
        if win.MultiByteToWideChar(65001, 8, path, -1, wide, size) == 0 then
            return nil, "invalid lock path"
        end
        local handle = win.CreateFileW(wide, 0x80000000, exclusive and 0 or 1, nil, 4, 128, nil)
        if handle ~= invalid then return handle end
        local err = tonumber(win.GetLastError())
        if err == 32 or err == 33 then return nil, "busy" end
        return nil, "CreateFileW failed (" .. err .. "): " .. path
    end
else
    ffi.cdef [[
        int open(const char *, int, ...);
        int flock(int, int);
        int close(int);
    ]]
    local C = ffi.C
    close = C.close
    local create_flag = (ffi.os == "OSX" or ffi.os == "BSD") and 0x0200 or 0x0040
    local would_block = (ffi.os == "OSX" or ffi.os == "BSD") and 35 or 11
    local mode = ffi.cast("int", 420) -- 0644，与 Go 锁文件一致。
    acquire = function(path, exclusive)
        local fd = C.open(path, create_flag, mode)
        if fd < 0 then return nil, "open failed (" .. ffi.errno() .. "): " .. path end
        if C.flock(fd, exclusive and 6 or 5) == 0 then return fd end
        local err = ffi.errno()
        close(fd)
        if err == would_block or err == 4 then return nil, "busy" end
        return nil, "flock failed (" .. err .. "): " .. path
    end
end

-- 以和 Go 相同的顺序尝试加锁；锁忙时立即返回，避免阻塞 OpenResty worker。
function M.with(perm_path, temp_path, exclusive, fn)
    if not perm_path or not temp_path or perm_path == temp_path then
        return false, "invalid cold storage paths"
    end
    local first, second = perm_path, temp_path
    if first > second then first, second = second, first end
    local a, err = acquire(first .. ".lock", exclusive)
    if not a then return false, err end
    local b, second_err = acquire(second .. ".lock", exclusive)
    if not b then
        close(a)
        return false, second_err
    end
    local ok, result = pcall(fn)
    close(b)
    close(a)
    return ok, result
end

return M
