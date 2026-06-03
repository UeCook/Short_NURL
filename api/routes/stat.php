<?php
/**
 * GET /api/stat - 查询配额状态
 *
 * 认证：X-Token 请求头
 * 调用 /internal/stat 读取 OpenResty 共享字典中的计数
 * 若内部 API 不可用则回退到冷存储，仅计有效条目
 */
// @外接口_#4：GET /api/stat — 查询配额状态（前端链路入口）
require __DIR__ . '/../common/bootstrap.php';

// 认证已由 bootstrap.php 统一处理，$AUTH['valid'] === true 保证到达此处

// 通过 /internal/stat 从热存储读取计数
// @外调用_&11：internalStat — 前端配额接口读取热存储计数
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
