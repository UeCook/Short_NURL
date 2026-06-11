<?php
/**
 * 认证凭证验证 — 接收凭证原文，调 KeyStore 验证，返回标准 $authCtx
 *
 * 唯一职责：判断凭证是否有效。不知道凭证从哪来（HTTP/CLI），不知道结果用来做什么。
 *
 * 返回 $authCtx 结构：
 *   [
 *     'valid'  => bool,
 *     'type'   => string|null,   // 'resident' / 'onetime' / 'service' / null（验证失败时）
 *     'reason' => string|null    // 'missing' / 'invalid' / 'wrong_channel' / null（验证成功时）
 *   ]
 *
 * 依赖：keys.php（通过 getKeyStore() 调 KeyStore::verify()）
 *       不直接调 auth_hash.php，哈希的事是 keys.php 内部的事。
 *
 * 换验证方式时（如从 key 改成账号密码体系）：改这个文件 + keys.php。
 */

// @关键_$32：auth_verify — 验证凭证原文，返回标准 $authCtx（valid/type/reason），不含错误输出
function auth_verify(string $rawKey, string $mode = 'api'): array {
    if ($rawKey === '') {
        return [
            'valid'  => false,
            'type'   => null,
            'reason' => 'missing',
        ];
    }

    $keyType = getKeyStore()->verify($rawKey);

    if ($keyType !== false) {
        if ($keyType === 'service' && $mode !== 'headless') {
            return [
                'valid'  => false,
                'type'   => null,
                'reason' => 'wrong_channel',
            ];
        }
        return [
            'valid'  => true,
            'type'   => $keyType,
            'reason' => null,
        ];
    }

    return [
        'valid'  => false,
        'type'   => null,
        'reason' => 'invalid',
    ];
}
