<?php
/**
 * 公共辅助函数
 *
 * 跨接口共享的工具函数。
 * 应在 json_store.php 等依赖文件之前 require_once。
 */

/**
 * 将 Unix 时间戳格式化为带时区偏移的 ISO 8601 字符串
 * @param int $ts     Unix 时间戳
 * @param string $tz  时区偏移（ISO 8601 格式，如 '+08:00'）
 * @return string     ISO 8601 格式的日期时间字符串
 * @throws \InvalidArgumentException  时区偏移格式无效时抛出
 */
// @关键_$1：formatIso8601 — 将 Unix 时间戳格式化为带时区偏移的 ISO 8601 字符串
function formatIso8601($ts, $tz) {
    if (!preg_match('/^[+-]\d{2}:\d{2}$/', $tz)) {
        throw new \InvalidArgumentException("无效时区偏移格式: {$tz}");
    }
    $sign = $tz[0];
    $parts = explode(':', substr($tz, 1));
    $offsetSec = intval($parts[0]) * 3600 + intval($parts[1]) * 60;
    if ($sign === '-') $offsetSec = -$offsetSec;
    $localTs = $ts + $offsetSec;
    return gmdate('Y-m-d\TH:i:s', $localTs) . $tz;
}

/**
 * 判断临时条目是否已过期（Unix 时间戳比较，时区安全）
 * strtotime 内部会将所有偏移归一化为 UTC，不受时区字符串格式影响。
 * @param string|null $expStr  过期时间 ISO 8601 字符串，null 或 '0' 表示永久
 * @return bool  已过期返回 true
 */
// @关键_$2：isExpired — 判断临时条目是否已过期（时区安全比较）
function isExpired($expStr) {
    if ($expStr === null || $expStr === '0' || $expStr === '') return false;
    $ts = strtotime($expStr);
    if ($ts === false) return true;  // 无法解析，视为已过期
    return $ts < time();
}

// @关键_$23：checkDataAccess — 检查 data 目录下三个文件的可读写性，不可访问返回 false
function checkDataAccess($cfg) {
    $files = [
        $cfg['perm_path']  ?? '',
        $cfg['temp_path']  ?? '',
        $cfg['keys_path']  ?? '',
    ];
    foreach ($files as $f) {
        if (!checkFileAccess($f)) {
            return false;
        }
    }
    return true;
}

/**
 * 检查单个文件的可访问性（支持文件不存在时检查目录可写性）
 * 文件存在 → 检查可读写性；文件不存在 → 检查父目录可写性（允许首次部署时自动创建）
 * @param string $path  文件路径
 * @return bool  可访问返回 true
 */
// @关键_$24：checkFileAccess — 检查单个文件的可访问性（文件存在检查可读写，不存在检查目录可写）
function checkFileAccess($path) {
    if ($path === '' || $path === null) return false;
    if (file_exists($path)) {
        return is_readable($path) && is_writable($path);
    }
    // 文件不存在 → 检查目录可写性（允许首次部署时自动创建）
    $dir = dirname($path);
    return is_dir($dir) && is_writable($dir);
}

/**
 * 从数据数组中原地移除过期条目
 * 由 create.php 和 delete.php 在写入时调用，清理 temp.json
 * @param array &$data  数据数组引用（code => entry）
 * @return int  移除的条目数
 */
// @关键_$3：cleanExpiredEntries — 从数据数组中原地移除过期条目
function cleanExpiredEntries(&$data) {
    $removed = 0;
    foreach ($data as $k => $item) {
        if (isExpired($item['t'] ?? null)) {
            unset($data[$k]);
            $removed++;
        }
    }
    return $removed;
}

/**
 * 获取全局配置的 KeyStore 单例（延迟初始化）
 * 提取自 api/common/bootstrap.php 和 headless/bootstrap.php，
 * 避免两处维护完全相同的函数定义。
 */
// @关键_$21/$24：getKeyStore — 获取全局 KeyStore 单例（延迟初始化，4 参数：path/tz/ttl/pool）
    if (!function_exists('getKeyStore')) {
    function getKeyStore() {
        global $cfg;
        static $instance = null;
        if ($instance === null) {
            $instance = new KeyStore(
                $cfg['keys_path'],
                $cfg['tz_offset'],
                $cfg['key_ttl_days'],
                $cfg['onetime_pool_size']
            );
        }
        return $instance;
    }
}

/**
 * SSRF 防护：检查 URL 是否指向内网/保留地址
 *
 * 阻止短链指向内部服务（127.0.0.1、10.x、172.16-31.x、192.168.x、169.254.x 等），
 * 防止通过短链跳转探测内网。先检查 IP 字面量，再 DNS 解析域名。
 *
 * 注意：gethostbyname 是阻塞调用，存在 DNS 解析延迟（通常 <1s）。
 * 解析失败（返回原 hostname）时放行，避免误伤无法解析的域名。
 *
 * @param string $url  待检查的完整 URL
 * @return bool  指向内网返回 true（应拒绝），公网返回 false
 */
// @关键_$36：isPrivateUrl — SSRF 防护检查（IP 字面量 + DNS 解析双重校验）
if (!function_exists('isPrivateUrl')) {
    function isPrivateUrl($url) {
        $host = parse_url($url, PHP_URL_HOST);
        if (!$host) return true;

        // 去除 IPv6 方括号
        $host = ltrim($host, '[');
        $host = rtrim($host, ']');

        // 直接是 IP 字面量
        $ip = filter_var($host, FILTER_VALIDATE_IP);
        if ($ip !== false) {
            return filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE) === false;
        }

        // 域名 → DNS 解析
        $resolved = @gethostbyname($host);
        if ($resolved === $host) {
            // 解析失败，放行（避免误伤，由调用方决定是否进一步限制）
            return false;
        }
        return filter_var($resolved, FILTER_VALIDATE_IP, FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE) === false;
    }
}
