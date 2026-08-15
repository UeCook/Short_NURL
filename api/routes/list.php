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

// ── 一次性密钥禁止进行短链管理 ─────────────────────────
if (($AUTH['type'] ?? null) === 'onetime') {
    http_response_code(403);
    echo json_encode(['error' => '一次性密钥无权进行短链管理'], JSON_UNESCAPED_UNICODE);
    exit;
}

$permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
$tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);

// 数据文件损坏时 read() 抛 RuntimeException：捕获并输出结构化 JSON，避免空白 500
try {
    $permData = $permStore->read();
    $tempData = $tempStore->read();
} catch (\RuntimeException $e) {
    error_log('[list] 冷存储读取失败: ' . $e->getMessage());
    http_response_code(500);
    echo json_encode(['error' => '数据文件损坏，请检查服务端日志'], JSON_UNESCAPED_UNICODE);
    exit;
}

// 永久短链：无 t 字段，exp 固定为 "permanent"
$perm = [];
foreach ($permData as $code => $item) {
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
foreach ($tempData as $code => $item) {
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
