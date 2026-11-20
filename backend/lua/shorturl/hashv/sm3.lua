-- SM3（GB/T 32905-2016）算法入口。接收原始字节，返回 64 个小写十六进制字符或 nil 和错误信息。
local digest = require "shorturl.util.digest"

return function(raw)
    return digest.hex("sm3", raw)
end
