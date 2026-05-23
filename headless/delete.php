<?php
/**
 * POST /headless/api/delete - 删除短链（无头链路）
 *
 * 认证：X-Headless-Token 请求头（bootstrap.php 已处理）
 * 请求体：{ code }
 * 业务逻辑与 api/delete.php 完全一致，仅错误输出使用 hl_error()
 */
// @外接口_#6：POST /headless/api/delete — 删除短链（无头链路入口）
require __DIR__ . '/bootstrap.php';

// ── 解析输入 ─────────────────────────────────────────
$input = json_decode(file_get_contents('php://input'), true);
if (!$input) {
    hl_error('bad_request', '无效的请求体', 400);
}

// ── 验证短码 ─────────────────────────────────────────
$code = isset($input['code']) ? trim($input['code']) : '';
if (empty($code)) {
    hl_error('missing_code', '缺少短链后缀', 400);
}
$code = strtolower($code);

// ── 初始化存储 ───────────────────────────────────────
$permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
$tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);

// ── 在冷存储中查找 ───────────────────────────────────
$permData = $permStore->read();
$tempData = $tempStore->read();
$isTemp = isset($tempData[$code]);
$isPerm = isset($permData[$code]);

if (!$isTemp && !$isPerm) {
    hl_error('not_found', '该短链不存在', 404);
}

// ── 先删热存储，后删冷存储 ─────────────────────────
// @外调用_&12：internalDelete — 删除热存储（通知 OpenResty 清除共享内存）
internalDelete($cfg, $code);

// ── 从冷存储删除（使用文件锁保证读-检查-写一致性）────
if ($isTemp) {
    $tempStore->lockBegin();
    try {
        $tempData = $tempStore->readLocked();
        unset($tempData[$code]);
        cleanExpiredEntries($tempData);
        $tempStore->writeLocked($tempData);
    } catch (\RuntimeException $e) {
        error_log('[safe_write] ' . $e->getMessage());
        hl_error('write_failed', '服务器内部错误', 500);
    } finally { $tempStore->lockEnd(); }
} else {
    $permStore->lockBegin();
    try {
        $permData = $permStore->readLocked();
        unset($permData[$code]);
        $permStore->writeLocked($permData);
    } catch (\RuntimeException $e) {
        error_log('[safe_write] ' . $e->getMessage());
        hl_error('write_failed', '服务器内部错误', 500);
    } finally { $permStore->lockEnd(); }
}

// ── 返回结果 ─────────────────────────────────────────
echo json_encode(['ok' => true]);
