<?php
/**
 * GET /api/stat - 查询配额状态
 *
 * 认证：X-Token 请求头
 * 调用 /internal/stat 读取 OpenResty 共享字典中的计数
 * 若内部 API 不可用则回退到冷存储，仅计有效条目
 */
// @外接口_#4：GET /api/stat — 查询配额状态（前端链路入口）
require __DIR__ . '/bootstrap.php';

// ── 验证密钥（GET 接口用 X-Token 请求头）──────────────
$key = $_SERVER['HTTP_X_TOKEN'] ?? '';
if (empty($key)) {
    http_response_code(406);
    echo json_encode(['error' => '缺少密钥（X-Token 请求头）'], JSON_UNESCAPED_UNICODE);
    exit;
}
// @外调用_&8：getKeyStore()->verify — 验证 API Key（KeyStore 密钥校验）
$keyType = getKeyStore()->verify($key);
if (!$keyType) {
    error_log('[auth] Key 验证失败（X-Token）：' . substr($key, 0, 8) . '...');
    http_response_code(406);
    echo json_encode(['error' => '密钥失效或不正确'], JSON_UNESCAPED_UNICODE);
    exit;
}

// 通过 /internal/stat 从热存储读取计数
// @外调用_&9：internalStat — 读取热存储计数（从 OpenResty 共享内存获取）
$stat = internalStat($cfg);

if ($stat) {
    echo json_encode([
        'perm_count' => intval($stat['perm_count'] ?? 0),
        'temp_count' => intval($stat['temp_count'] ?? 0),
        'perm_limit' => $cfg['perm_limit'],
        'temp_limit' => $cfg['temp_limit'],
    ]);
} else {
    // 回退：内部 API 不可用时从冷存储读取
    // countActive() 会用时区安全的方式过滤过期条目
    $permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
    $tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);

    echo json_encode([
        'perm_count' => $permStore->countActive(),
        'temp_count' => $tempStore->countActive(),
        'perm_limit' => $cfg['perm_limit'],
        'temp_limit' => $cfg['temp_limit'],
    ]);
}
