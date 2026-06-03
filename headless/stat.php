<?php
/**
 * GET /headless/api/stat - 查询配额状态（无头链路）
 *
 * 认证：X-Headless-Token 请求头（bootstrap.php 已处理）
 * 同时返回热存储和冷存储计数，与 api/stat.php 保持一致
 */
// @外接口_#8：GET /headless/api/stat — 查询配额状态（无头链路入口）
require __DIR__ . '/bootstrap.php';

// ── 冷存储计数（始终计算，作为参考基准）──────────────────
$permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
$tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);
$cold_perm = $permStore->countActive();
$cold_temp = $tempStore->countActive();

// ── 热存储计数（权威来源）──────────────────────────────
// @外调用_&14：internalStat — 无头配额接口读取热存储计数
$stat = internalStat($cfg);

if ($stat) {
    echo json_encode([
        'perm_count'  => intval($stat['perm_count'] ?? 0),
        'temp_count'  => intval($stat['temp_count'] ?? 0),
        'cold_perm_count' => $cold_perm,
        'cold_temp_count' => $cold_temp,
        'perm_limit'  => $cfg['perm_limit'],
        'temp_limit'  => $cfg['temp_limit'],
        'hot_available' => true,
    ], JSON_UNESCAPED_UNICODE);
} else {
    // 热存储不可用：主计数使用冷存储
    echo json_encode([
        'perm_count'  => $cold_perm,
        'temp_count'  => $cold_temp,
        'cold_perm_count' => $cold_perm,
        'cold_temp_count' => $cold_temp,
        'perm_limit'  => $cfg['perm_limit'],
        'temp_limit'  => $cfg['temp_limit'],
        'hot_available' => false,
    ], JSON_UNESCAPED_UNICODE);
}
