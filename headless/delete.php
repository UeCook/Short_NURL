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

// ── 一次性密钥禁止进行短链管理 ─────────────────────────
if (($AUTH['type'] ?? null) === 'onetime') {
    hl_error('onetime_forbidden', '一次性密钥无权进行短链管理', 403);
}

// ── 解析输入 ─────────────────────────────────────────
// $RAW_INPUT 由 bootstrap.php 缓存（php://input 只能读一次）
$input = json_decode($GLOBALS['RAW_INPUT'] ?? '{}', true);
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

// ── 先删冷存储，后删热存储 ─────────────────────────
// 冷存储是数据源，先确保冷存储删除成功，再同步到热存储
// 崩溃安全：冷删成功但热未删 → 最多多一次跳转，下次请求冷无数据后自然修复

// ── 从冷存储删除（使用文件锁保证读-检查-写一致性）────
if ($isTemp) {
    $tempStore->lockBegin();
    try {
        $tempData = $tempStore->readLocked();
        // 锁内重新检查存在性（防止并发删除时两个请求都通过 404 检查）
        if (!isset($tempData[$code])) {
            hl_error('not_found', '该短链不存在', 404);
        }
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
        // 锁内重新检查存在性（防止并发删除时两个请求都通过 404 检查）
        if (!isset($permData[$code])) {
            hl_error('not_found', '该短链不存在', 404);
        }
        unset($permData[$code]);
        $permStore->writeLocked($permData);
    } catch (\RuntimeException $e) {
        error_log('[safe_write] ' . $e->getMessage());
        hl_error('write_failed', '服务器内部错误', 500);
    } finally { $permStore->lockEnd(); }
}

// ── 冷存储删除成功，同步删除热存储 ─────────────────────
// @外调用_&13：internalDelete — 无头删除接口清除热存储
// 传递短链类型（由冷存储查找确定），让 Lua 精准递减对应计数器
$typeHint = $isTemp ? 'temp' : 'perm';
$delResult = internalDelete($cfg, $code, $typeHint);

if ($delResult === null) {
    $lastErr = getLastInternalError();
    error_log("[headless_delete] 热存储同步失败: code={$code}, type={$typeHint}, " .
        ($lastErr ? json_encode($lastErr, JSON_UNESCAPED_UNICODE) : 'unknown'));
}

// ── 返回结果 ─────────────────────────────────────────
echo json_encode([
    'ok'     => true,
    'synced' => ($delResult !== null),
], JSON_UNESCAPED_UNICODE);
