<?php
/**
 * GET /api/list - 查询所有短链
 *
 * 认证：X-Token 请求头
 * 读取 perm.json 和 temp.json，过滤过期临时条目，返回列表
 */
// @外接口_#3：GET /api/list — 查询所有短链（前端链路入口）
require __DIR__ . '/bootstrap.php';

// ── 验证密钥（GET 接口用 X-Token 请求头）──────────────
$key = $_SERVER['HTTP_X_TOKEN'] ?? '';
if (empty($key)) {
    http_response_code(406);
    echo json_encode(['error' => '缺少密钥（X-Token 请求头）'], JSON_UNESCAPED_UNICODE);
    exit;
}
// @外调用_&7：getKeyStore()->verify — 验证 API Key（KeyStore 密钥校验）
$keyType = getKeyStore()->verify($key);
if (!$keyType) {
    error_log('[auth] Key 验证失败（X-Token）：' . substr($key, 0, 8) . '...');
    http_response_code(406);
    echo json_encode(['error' => '密钥失效或不正确'], JSON_UNESCAPED_UNICODE);
    exit;
}

$permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
$tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);

// 永久短链：无 t 字段，exp 固定为 "permanent"
$perm = [];
foreach ($permStore->read() as $code => $item) {
    $perm[] = [
        'id'   => $item['id'],
        'url'  => $item['url'],
        'lurl' => $item['lurl'],
        'exp'  => 'permanent',
    ];
}

// 临时短链：用时区安全的 isExpired() 过滤过期条目
$temp = [];
foreach ($tempStore->read() as $code => $item) {
    if (isExpired($item['t'] ?? null)) {
        continue;
    }
    $temp[] = [
        'id'   => $item['id'],
        'url'  => $item['url'],
        'lurl' => $item['lurl'],
        'exp'  => isset($item['t']) ? $item['t'] : '-',
    ];
}

echo json_encode(['permanent' => $perm, 'temporary' => $temp], JSON_UNESCAPED_UNICODE);
