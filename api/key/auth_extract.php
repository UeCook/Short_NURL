<?php
/**
 * 认证凭证提取 — 从当前 HTTP 请求中取出凭证原文
 *
 * 唯一职责：提取凭证，返回 string|null。不做任何验证。
 *
 * 通过 $mode 参数隔离两条链路的凭证来源：
 *   - 'headless'：仅接受 X-Headless-Token（无头链路）
 *   - 'api'（默认）：仅接受 X-Token 和 body.key（前端链路），拒绝 X-Headless-Token
 *
 * 这样一个请求即使同时带了 X-Headless-Token 和 X-Token，也只会被当前链路
 * 认作有效凭证来源，凭证来源边界由函数签名保证，而非依赖调用顺序。
 *
 * 注意：POST body 通过参数 $rawInput 传入（由 bootstrap 缓存 php://input），
 *       不直接读流（php://input 只能读一次）。
 *
 * 换凭证来源时（如改 Cookie、Basic Auth、Bearer Token）：只改这一个文件。
 */

// @关键_$28：auth_extract — 从 HTTP 请求提取认证凭证（按 mode 隔离链路来源），不做验证，返回 string|null
function auth_extract(?string $rawInput = null, string $mode = 'api'): ?string {
    if ($mode === 'headless') {
        // 无头链路：仅接受 X-Headless-Token
        $token = $_SERVER['HTTP_X_HEADLESS_TOKEN'] ?? '';
        return $token !== '' ? $token : null;
    }

    // api 链路（默认）：仅接受 X-Token 和 body.key，不接受 X-Headless-Token
    $xToken = $_SERVER['HTTP_X_TOKEN'] ?? '';
    if ($xToken !== '') {
        return $xToken;
    }
    if ($rawInput !== null && $rawInput !== '') {
        $json = json_decode($rawInput, true);
        if (is_array($json)
            && isset($json['key'])
            && is_string($json['key'])
            && $json['key'] !== '') {
            return $json['key'];
        }
    }

    return null;
}
