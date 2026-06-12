<?php
/**
 * GET /headless/api/list - 查询所有短链（无头链路）
 *
 * 认证：X-Headless-Token 请求头（bootstrap.php 已处理）
 * 业务逻辑与 api/list.php 完全一致，仅错误输出使用 hl_error()
 */
// @外接口_#7：GET /headless/api/list — 查询所有短链（无头链路入口）
require __DIR__ . '/bootstrap.php';

// ── 一次性密钥禁止进行短链管理 ─────────────────────────
if (($AUTH['type'] ?? null) === 'onetime') {
    hl_error('onetime_forbidden', '一次性密钥无权进行短链管理', 403);
}

$permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
$tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);

// 永久短链：无 t 字段，exp 固定为 "permanent"
$perm = [];
foreach ($permStore->read() as $code => $item) {
    if (!isset($item['id'], $item['url'], $item['lurl'])) continue;
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
    if (!isset($item['id'], $item['url'], $item['lurl'])) continue;
    if (isExpired($item['t'] ?? null)) {
        continue;
    }
    $temp[] = [
        'id'   => $item['id'],
        'url'  => $item['url'],
        'lurl' => $item['lurl'],
        'exp'  => $item['t'] ?? null,
    ];
}

echo json_encode(['permanent' => $perm, 'temporary' => $temp], JSON_UNESCAPED_UNICODE);
