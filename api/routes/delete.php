<?php
/**
 * POST /api/delete - 删除短链
 *
 * 请求体：{ code, key }
 * Key 在请求体中。无效 Key → 406。
 */
// @外接口_#2：POST /api/delete — 删除短链（前端链路入口）
require __DIR__ . '/../common/bootstrap.php';

// ── 解析输入 ─────────────────────────────────────────
// $RAW_INPUT 由 bootstrap.php 缓存（php://input 只能读一次）
$input = json_decode($GLOBALS['RAW_INPUT'] ?? '{}', true);
if (!$input) {
    http_response_code(400);
    echo json_encode(['error' => '无效的请求体'], JSON_UNESCAPED_UNICODE);
    exit;
}

// 认证已由 bootstrap.php 统一处理，$AUTH['valid'] === true 保证到达此处

// ── 验证短码 ─────────────────────────────────────────
$code = isset($input['code']) ? trim($input['code']) : '';
if (empty($code)) {
    http_response_code(400);
    echo json_encode(['error' => '缺少短链后缀'], JSON_UNESCAPED_UNICODE);
    exit;
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
    http_response_code(404);
    echo json_encode(['error' => '该短链不存在'], JSON_UNESCAPED_UNICODE);
    exit;
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
            $tempStore->lockEnd();
            http_response_code(404);
            echo json_encode(['error' => '该短链不存在'], JSON_UNESCAPED_UNICODE);
            exit;
        }
        unset($tempData[$code]);
        // 删除时顺便清理过期条目
        cleanExpiredEntries($tempData);
        $tempStore->writeLocked($tempData);
    } catch (\RuntimeException $e) {
        error_log('[safe_write] ' . $e->getMessage());
        http_response_code(500);
        echo json_encode(['error' => '服务器内部错误'], JSON_UNESCAPED_UNICODE);
        exit;
    } finally { $tempStore->lockEnd(); }
} else {
    $permStore->lockBegin();
    try {
        $permData = $permStore->readLocked();
        // 锁内重新检查存在性（防止并发删除时两个请求都通过 404 检查）
        if (!isset($permData[$code])) {
            $permStore->lockEnd();
            http_response_code(404);
            echo json_encode(['error' => '该短链不存在'], JSON_UNESCAPED_UNICODE);
            exit;
        }
        unset($permData[$code]);
        $permStore->writeLocked($permData);
    } catch (\RuntimeException $e) {
        error_log('[safe_write] ' . $e->getMessage());
        http_response_code(500);
        echo json_encode(['error' => '服务器内部错误'], JSON_UNESCAPED_UNICODE);
        exit;
    } finally { $permStore->lockEnd(); }
}

// ── 冷存储删除成功，同步删除热存储 ─────────────────────
// @外调用_&10：internalDelete — 前端删除接口清除热存储
internalDelete($cfg, $code);

// ── 返回结果 ─────────────────────────────────────────
echo json_encode(['ok' => true], JSON_UNESCAPED_UNICODE);
