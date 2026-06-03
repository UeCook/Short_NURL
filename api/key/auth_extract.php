<?php
/**
 * 认证凭证提取 — 从当前 HTTP 请求中取出凭证原文
 *
 * 唯一职责：提取凭证，返回 string|null。不做任何验证。
 *
 * 按优先级尝试：
 *   1. X-Headless-Token 请求头（无头链路）
 *   2. X-Token 请求头（前端 GET 接口）
 *   3. POST body JSON 的 key 字段（前端 POST 接口）
 *   4. 均未命中，返回 null
 *
 * 换凭证来源时（如改 Cookie、Basic Auth、Bearer Token）：只改这一个文件。
 *
 * 注意：POST body 通过参数 $rawInput 传入（由 bootstrap 缓存 php://input），
 *       不直接读流（php://input 只能读一次）。
 */

// @关键_$31：auth_extract — 从 HTTP 请求提取认证凭证（Header/Body 优先级），不做验证，返回 string|null
function auth_extract(?string $rawInput = null): ?string {
    // 1. X-Headless-Token（无头链路）
    $headlessToken = $_SERVER['HTTP_X_HEADLESS_TOKEN'] ?? '';
    if ($headlessToken !== '') {
        return $headlessToken;
    }

    // 2. X-Token（前端 GET 接口）
    $xToken = $_SERVER['HTTP_X_TOKEN'] ?? '';
    if ($xToken !== '') {
        return $xToken;
    }

    // 3. POST body JSON 的 key 字段
    if ($rawInput !== null && $rawInput !== '') {
        $json = json_decode($rawInput, true);
        if (is_array($json) && isset($json['key']) && $json['key'] !== '') {
            return $json['key'];
        }
    }

    // 4. 均未命中
    return null;
}
