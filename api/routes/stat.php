<?php
/**
 * GET /api/stat - 查询配额状态
 *
 * 认证：X-Token 请求头
 * 同时返回热存储（Lua shared dict）和冷存储（JSON 文件）计数：
 *   - perm_count / temp_count — 冷存储计数（权威来源，实时遍历 JSON 永不漂移）
 *   - hot_perm_count / hot_temp_count — 热存储计数（仅供内部参考）
 *   - hot_available — 热存储是否可用
 *
 * 以冷存储为主显示：冷存储是无状态遍历（countActive + isExpired），不会因
 * incr/decr 失败、su_exp 条目缺失等原因产生计数漂移，与无头模式 CLI 数据源一致。
 */
// @外接口_#4：GET /api/stat — 查询配额状态（前端链路入口）
require __DIR__ . '/../common/bootstrap.php';

// 认证已由 bootstrap.php 统一处理，$AUTH['valid'] === true 保证到达此处

// ── 一次性密钥禁止进行短链管理 ─────────────────────────
if (($AUTH['type'] ?? null) === 'onetime') {
    http_response_code(403);
    echo json_encode(['error' => '一次性密钥无权进行短链管理'], JSON_UNESCAPED_UNICODE);
    exit;
}

// ── 冷存储计数（始终计算，作为参考基准）──────────────────
$permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
$tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);
$cold_perm = $permStore->countActive();
$cold_temp = $tempStore->countActive();

// ── 热存储计数（内部参考）──────────────────────────────
// @外调用_&11：internalStat — 读取热存储计数（仅供内部参考）
$stat = internalStat($cfg);

// 始终以冷存储为权威来源：冷存 countActive() 是无状态遍历，永不漂移
// 热存储计数保留作为 hot_perm_count / hot_temp_count 供诊断
$result = [
    'perm_count'       => $cold_perm,
    'temp_count'       => $cold_temp,
    'perm_limit'       => $cfg['perm_limit'],
    'temp_limit'       => $cfg['temp_limit'],
    'hot_available'    => ($stat !== null),
];

if ($stat) {
    $result['hot_perm_count'] = intval($stat['perm_count'] ?? 0);
    $result['hot_temp_count'] = intval($stat['temp_count'] ?? 0);

    // 热/冷差异日志（可观测性）
    $drift_perm = abs($result['hot_perm_count'] - $cold_perm);
    $drift_temp = abs($result['hot_temp_count'] - $cold_temp);
    if ($drift_perm > 0 || $drift_temp > 0) {
        error_log("[stat] 热冷计数漂移: " .
            "hot perm={$result['hot_perm_count']} cold perm={$cold_perm} drift={$drift_perm}, " .
            "hot temp={$result['hot_temp_count']} cold temp={$cold_temp} drift={$drift_temp}");
    }
} else {
    error_log("[stat] 热存储不可用，使用冷存储计数：cold_perm={$cold_perm}, cold_temp={$cold_temp}");
}

echo json_encode($result, JSON_UNESCAPED_UNICODE);
