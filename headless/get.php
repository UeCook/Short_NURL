<?php
/**
 * GET /headless/api/get/{code} - 按短码查询单条短链（无头专属）
 *
 * 认证：X-Headless-Token 请求头（bootstrap.php 已处理）
 * 此接口仅存在于无头链路，不在 /api/ 下注册，避免前端意外依赖。
 *
 * 逻辑：
 *   1. 从 PATH_INFO 提取 code
 *   2. 查 perm.json，命中返回，exp 填 "permanent"
 *   3. 查 temp.json，命中检查 t 字段：未过期返回，exp 填 ISO 8601；已过期返回 404
 *   4. 均未命中，返回 404
 */
// @外接口_#9：GET /headless/api/get/{code} — 按短码查询单条短链（无头专属入口）
require __DIR__ . '/bootstrap.php';

// ── 从 PATH_INFO 提取短码 ────────────────────────────
$code = '';
if (isset($_SERVER['PATH_INFO'])) {
    $code = ltrim($_SERVER['PATH_INFO'], '/');
}
if (empty($code)) {
    hl_error('missing_code', '缺少短链后缀', 400);
}
$code = strtolower($code);

// ── 查永久存储 ───────────────────────────────────────
$permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
$permData = $permStore->read();
if (isset($permData[$code])) {
    $item = $permData[$code];
    echo json_encode([
        'id'   => $item['id'],
        'url'  => $item['url'],
        'lurl' => $item['lurl'],
        'exp'  => 'permanent',
    ]);
    exit;
}

// ── 查临时存储 ───────────────────────────────────────
$tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);
$tempData = $tempStore->read();
if (isset($tempData[$code])) {
    $item = $tempData[$code];
    // 检查是否过期
    if (isExpired($item['t'] ?? null)) {
        hl_error('expired', '该短链已过期', 404);
    }
    echo json_encode([
        'id'   => $item['id'],
        'url'  => $item['url'],
        'lurl' => $item['lurl'],
        'exp'  => isset($item['t']) ? $item['t'] : '-',
    ]);
    exit;
}

// ── 均未命中 ─────────────────────────────────────────
hl_error('not_found', '该短链不存在', 404);
