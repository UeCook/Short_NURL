<?php
/**
 * GET /headless/api/stat - 查询配额状态（无头链路）
 *
 * 认证：X-Headless-Token 请求头（bootstrap.php 已处理）
 * 同时返回热存储和冷存储计数，与 api/stat.php 保持一致
 */
// @外接口_#8：GET /headless/api/stat — 查询配额状态（无头链路入口）
require __DIR__ . '/bootstrap.php';

// ── 一次性密钥禁止进行短链管理 ─────────────────────────
if (($AUTH['type'] ?? null) === 'onetime') {
    hl_error('onetime_forbidden', '一次性密钥无权进行短链管理', 403);
}

// ── 冷存储计数（始终计算，作为参考基准）──────────────────
$permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
$tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);
$cold_perm = $permStore->countActive();
$cold_temp = $tempStore->countActive();

// ── 热存储计数（权威来源）──────────────────────────────
// @外调用_&14：internalStat — 无头配额接口读取热存储计数
$stat = internalStat($cfg);

// ── 响应输出 ─────────────────────────────────────────
// 以冷存储为权威来源（与 api/routes/stat.php 保持一致），冷存 countActive() 是无状态遍历永不漂移
// 热存储计数保留作为 hot_perm_count / hot_temp_count 供诊断
$result = [
    'perm_count'    => $cold_perm,
    'temp_count'    => $cold_temp,
    'perm_limit'    => $cfg['perm_limit'],
    'temp_limit'    => $cfg['temp_limit'],
    'hot_available' => ($stat !== null),
];

if ($stat) {
    $result['hot_perm_count'] = intval($stat['perm_count'] ?? 0);
    $result['hot_temp_count'] = intval($stat['temp_count'] ?? 0);
} else {
    error_log("[headless_stat] 热存储不可用，使用冷存储计数：cold_perm={$cold_perm}, cold_temp={$cold_temp}");
}

echo json_encode($result, JSON_UNESCAPED_UNICODE);
