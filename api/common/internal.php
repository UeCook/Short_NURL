<?php
/**
 * 内部 API 客户端 — 与 OpenResty Lua 后端通信
 * 端点：/internal/set、/internal/delete、/internal/stat
 * 仅 127.0.0.1 可访问（Nginx 层强制）
 */

/**
 * 写入热存储 — POST /internal/set
 * @param array  $cfg      配置数组
 * @param string $code     短码
 * @param string $url      原始链接
 * @param int    $ttl      TTL 秒数（0 = 永久）
 * @param string $exp_str  ISO 8601 过期时间字符串（永久为 "0"）
 * @return array|null
 */
// @外调用入口_%1：internalSet — 写入热存储（调用 OpenResty POST /internal/set）
function internalSet($cfg, $code, $url, $ttl, $exp_str) {
    // @外调用_&1：internalPost — 向 OpenResty 内部 API 发送 POST 请求
    return internalPost($cfg, '/internal/set', [
        'code'    => $code,
        'url'     => $url,
        'ttl'     => $ttl,
        'exp_str' => $exp_str,
    ]);
}

/**
 * 删除热存储 — POST /internal/delete
 * @param array  $cfg   配置数组
 * @param string $code  要删除的短码
 * @return array|null
 */
// @外调用入口_%2：internalDelete — 删除热存储（调用 OpenResty POST /internal/delete）
function internalDelete($cfg, $code) {
    // @外调用_&2：internalPost — 向 OpenResty 内部 API 发送 POST 请求
    return internalPost($cfg, '/internal/delete', [
        'code' => $code,
    ]);
}

/**
 * 读取热存储计数 — GET /internal/stat
 * @param array $cfg  配置数组
 * @return array|null  {perm_count, temp_count}
 */
// @外调用入口_%3：internalStat — 读取热存储计数（调用 OpenResty GET /internal/stat）
function internalStat($cfg) {
    $host = $cfg['internal_host'];
    $timeout = $cfg['internal_timeout'];
    $url = $host . '/internal/stat';

    $ch = curl_init($url);
    curl_setopt_array($ch, [
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT => $timeout,
        CURLOPT_CONNECTTIMEOUT => $timeout,
    ]);
    $resp = curl_exec($ch);
    $code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $contentType = curl_getinfo($ch, CURLINFO_CONTENT_TYPE);
    $err = curl_error($ch);
    curl_close($ch);

    if ($err) {
        error_log("[internal_stat] cURL 错误：{$err} ({$url})");
        return null;
    }
    if ($code === 200 && $resp) {
        // 注意：必须使用 === false 而非 !strpos()，因为 strpos 可能返回 0（位置在开头）
        // 例如 "application/json; charset=utf-8" 会返回 0，!strpos 会误判为 false
        if ($contentType && strpos($contentType, 'application/json') === false) {
            error_log("[internal_stat] 非 JSON 响应：{$contentType} ({$url})");
            return null;
        }
        $d = json_decode($resp, true);
        if (json_last_error() !== JSON_ERROR_NONE) return null;
        return is_array($d) ? $d : null;
    }
    error_log("[internal_stat] 非 200 响应：HTTP {$code} ({$url})");
    return null;
}

/**
 * 内部 POST 请求辅助函数
 * @param array  $cfg     配置数组
 * @param string $path    端点路径
 * @param array  $params  POST 请求体数据
 * @return array|null
 */
// @关键_$22：internalPost — 内部 POST 请求辅助函数（cURL 与 OpenResty 通信）
function internalPost($cfg, $path, $params = []) {
    $host = $cfg['internal_host'];
    $timeout = $cfg['internal_timeout'];
    $url = $host . $path;

    $ch = curl_init($url);
    curl_setopt_array($ch, [
        CURLOPT_POST => true,
        CURLOPT_POSTFIELDS => json_encode($params),
        CURLOPT_HTTPHEADER => ['Content-Type: application/json'],
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT => $timeout,
        CURLOPT_CONNECTTIMEOUT => $timeout,
    ]);
    $resp = curl_exec($ch);
    $code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
    $contentType = curl_getinfo($ch, CURLINFO_CONTENT_TYPE);
    $err = curl_error($ch);
    curl_close($ch);

    if ($err) {
        error_log("[internal_post] cURL 错误：{$err} ({$url})");
        return null;
    }
    if ($code === 200 && $resp) {
        // 注意：必须使用 === false 而非 !strpos()，因为 strpos 可能返回 0（位置在开头）
        // 例如 "application/json; charset=utf-8" 会返回 0，!strpos 会误判为 false
        if ($contentType && strpos($contentType, 'application/json') === false) {
            error_log("[internal_post] 非 JSON 响应：{$contentType} ({$url})");
            return null;
        }
        $d = json_decode($resp, true);
        if (json_last_error() !== JSON_ERROR_NONE) return null;
        return is_array($d) ? $d : null;
    }
    error_log("[internal_post] 非 200 响应：HTTP {$code} ({$url})");
    return null;
}
