<?php
/**
 * POST /api/delete - 删除短链
 *
 * 请求体：{ code, key }
 * Key 在请求体中。无效 Key → 406。
 */
// @外接口_#2：POST /api/delete — 删除短链（前端链路入口）
require __DIR__ . '/bootstrap.php';

// ── 解析输入 ─────────────────────────────────────────
$input = json_decode(file_get_contents('php://input'), true);
if (!$input) {
    http_response_code(400);
    echo json_encode(['error' => '无效的请求体'], JSON_UNESCAPED_UNICODE);
    exit;
}

// ── 验证密钥 ──────────────────────────────────────────
$key = $input['key'] ?? '';
if (empty($key)) {
    http_response_code(406);
    echo json_encode(['error' => '缺少密钥'], JSON_UNESCAPED_UNICODE);
    exit;
}
// @外调用_&5：getKeyStore()->verify — 验证 API Key（KeyStore 密钥校验）
$keyType = getKeyStore()->verify($key);
if (!$keyType) {
    error_log('[auth] Key 验证失败：' . substr($key, 0, 8) . '...');
    http_response_code(406);
    echo json_encode(['error' => '密钥失效或不正确'], JSON_UNESCAPED_UNICODE);
    exit;
}

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

// ── 先删热存储，后删冷存储 ─────────────────────────
// 先删热可防止崩溃时出现幽灵链接（热删了最多 404，冷删了热还在就跳转）
// @外调用_&6：internalDelete — 删除热存储（通知 OpenResty 清除共享内存）
internalDelete($cfg, $code);

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

// ── 返回结果 ─────────────────────────────────────────
echo json_encode(['ok' => true], JSON_UNESCAPED_UNICODE);
