<?php
/**
 * GET /headless/api/stat - 查询配额状态（无头链路）
 *
 * 认证：X-Headless-Token 请求头（bootstrap.php 已处理）
 * 业务逻辑与 api/stat.php 完全一致，仅错误输出使用 hl_error()
 */
// @外接口_#8：GET /headless/api/stat — 查询配额状态（无头链路入口）
require __DIR__ . '/bootstrap.php';

// 通过 /internal/stat 从热存储读取计数
// @外调用_&13：internalStat — 读取热存储计数（从 OpenResty 共享内存获取）
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
    $permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
    $tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);

    echo json_encode([
        'perm_count' => $permStore->countActive(),
        'temp_count' => $tempStore->countActive(),
        'perm_limit' => $cfg['perm_limit'],
        'temp_limit' => $cfg['temp_limit'],
    ]);
}
