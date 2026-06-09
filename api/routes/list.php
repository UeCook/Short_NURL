<?php
/**
 * GET /api/list - 查询所有短链
 *
 * 认证：X-Token 请求头
 * 读取 perm.json 和 temp.json，过滤过期临时条目，返回列表
 */
// @外接口_#3：GET /api/list — 查询所有短链（前端链路入口）
require __DIR__ . '/../common/bootstrap.php';

// 认证已由 bootstrap.php 统一处理，$AUTH['valid'] === true 保证到达此处

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
