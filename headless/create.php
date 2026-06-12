<?php
/**
 * POST /headless/api/create - 创建短链（无头链路）
 *
 * 认证：X-Headless-Token 请求头（bootstrap.php 已处理）
 * 业务逻辑与 api/create.php 完全一致，仅错误输出使用 hl_error()
 */
// @外接口_#5：POST /headless/api/create — 创建短链（无头链路入口）
require __DIR__ . '/bootstrap.php';

// ── 解析输入 ─────────────────────────────────────────
// $RAW_INPUT 由 bootstrap.php 缓存（php://input 只能读一次）
$input = json_decode($GLOBALS['RAW_INPUT'] ?? '{}', true);
if (!$input) {
    hl_error('bad_request', '无效的请求体', 400);
}

// ── 验证目标链接 ──────────────────────────────────────
if (empty($input['url'])) {
    hl_error('missing_url', '缺少目标链接', 400);
}
$url = trim($input['url']);
if (!filter_var($url, FILTER_VALIDATE_URL) || !preg_match('#^https?://#i', $url)) {
    hl_error('invalid_url', '目标链接无效', 400);
}
$ttl = isset($input['ttl']) ? intval($input['ttl']) : 0;
if ($ttl < 0 || $ttl > $cfg['ttl_max']) {
    hl_error('ttl_exceeded', 'TTL 超限', 400);
}
$isTemp = $ttl > 0;

$permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
$tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);

$code = isset($input['code']) ? trim($input['code']) : '';
$isCustom = !empty($code);

// ── 永久链去重（写入前查冷存储，URL 完全匹配则复用）──
// @关键_$26：永久链去重 — ttl==0 且未传自定义短码时遍历 perm.json，URL 严格匹配则返回已有短码
// 传了自定义短码说明用户有明确意图，去重不干预
// 去重命中也返回固定响应结构（dedup=true），HTTP 统一为 200
// 调用 internalSet 确保热存储同步（防止冷启动后热存储缺失该条目）
if (!$isTemp && !$isCustom) {
    $permData = $permStore->read();
    foreach ($permData as $dedupCode => $existing) {
        if (isset($existing['url'], $existing['lurl']) && $existing['url'] === $url) {
            // @外调用_&17：internalSet — 无头创建去重路径写入热存储
            $dedupSynced = internalSet($cfg, $dedupCode, $existing['url'], 0, '0');
            $dedupWarning = null;
            if (!$dedupSynced) {
                $detail = getLastInternalError();
                $diagMsg = $detail ? $detail['message'] : '未知错误';
                error_log("[headless_create] 去重路径热存储同步失败：code={$dedupCode}，原因：{$diagMsg}");
                $dedupWarning = '热存储同步失败：短链暂不可用，请稍后重试';
            }
            echo json_encode([
                'short_url'    => $existing['lurl'],
                'exp'          => null,
                'dedup'        => true,
                'synced'       => $dedupSynced,
                'warning'      => $dedupWarning,
                'key_consumed' => ($AUTH['type'] ?? null) === 'onetime',
            ], JSON_UNESCAPED_UNICODE);
            exit;
        }
    }
}

if ($isCustom) {
    $code = strtolower($code);
    if (!preg_match('/^[0-9a-z-]{1,4}$/', $code)) { hl_error('invalid_code', '后缀格式错误', 400); }
    if (in_array($code, $cfg['reserved_codes'])) { hl_error('reserved_code', '保留字', 400); }
}

$now = time();
$domain = $cfg['domain'];
$tz = $cfg['tz_offset'];
$exp_str = $isTemp ? formatIso8601($now + $ttl, $tz) : '0';
$shortUrl = $domain . '/' . $code;
$entry = ['id' => $code, 'url' => $url, 'lurl' => $shortUrl];
if ($isTemp) $entry['t'] = $exp_str;

$store = $isTemp ? $tempStore : $permStore;
$otherStore = $isTemp ? $permStore : $tempStore;

// ── 原子读-检查-写 ──────────────────────────────────
$store->lockBegin();
$errorResponse = null;
try {
    $data = $store->readLocked();
    if ($isTemp) cleanExpiredEntries($data);
    if ($isCustom) {
        if (isset($data[$code])) { $errorResponse = ['conflict', '已占用', 409]; }
        elseif ($otherStore->find($code)) { $errorResponse = ['conflict', '已占用', 409]; }
    } else {
        $chars = '0123456789abcdefghijklmnopqrstuvwxyz';
        $maxLen = strlen($chars) - 1;
        $generated = false;
        for ($i = 0; $i < 8; $i++) {
            $candidate = ''; for ($j = 0; $j < 4; $j++) $candidate .= $chars[random_int(0, $maxLen)];
            if (in_array($candidate, $cfg['reserved_codes'])) continue;
            if (isset($data[$candidate])) continue;
            if ($otherStore->find($candidate)) continue;
            $code = $candidate; $generated = true; break;
        }
        if (!$generated) {
            $errorResponse = ['gen_failed', '无法生成后缀', 500];
        } else {
            $shortUrl = $domain . '/' . $code;
            $entry['id'] = $code; $entry['lurl'] = $shortUrl;
        }
    }
    if (!$errorResponse) {
        $activeCount = 0;
        foreach ($data as $item) { if (!isExpired($item['t'] ?? null)) $activeCount++; }
        $limit = $cfg[$isTemp ? 'temp_limit' : 'perm_limit'];
        if ($activeCount >= $limit) { $errorResponse = ['quota_exceeded', '已达上限', 429]; }
    }
    if (!$errorResponse) {
        $data[$code] = $entry;
        $store->writeLocked($data);
    }
} catch (\Exception $e) {
    error_log('[safe_write] ' . $e->getMessage());
    hl_error('write_failed', '服务器内部错误', 500);
} finally { $store->lockEnd(); }

if ($errorResponse) {
    hl_error($errorResponse[0], $errorResponse[1], $errorResponse[2]);
}

// @外调用_&12：internalSet — 无头创建接口写入热存储
$synced = internalSet($cfg, $code, $url, $isTemp ? $ttl : 0, $exp_str);
$warning = null;
if (!$synced) {
    $detail = getLastInternalError();
    $diagMsg = $detail ? $detail['message'] : '未知错误';
    error_log("[headless_create] 热存储同步失败：code={$code}，原因：{$diagMsg}");
    $warning = '热存储同步失败：短链已保存但暂不可用，请稍后重试';
}

// ── 统一响应结构 ─────────────────────────────────────
// 所有成功路径（新建 / 去重 / 同步失败）均返回固定字段集合，HTTP 统一为 200。
// 语义信息通过字段传达，不再用 201/207 区分：
//   - exp:          临时短链为 ISO 8601 字符串；永久短链为 null
//   - dedup:        true 表示命中永久链去重（未实际写入）
//   - synced:       false 表示冷存储已写但热存储同步失败（缓存窗口内可能跳不通）
//   - warning:      非 null 时为人类可读的同步告警（仅 synced=false 时有值）
//   - key_consumed: true 表示本次请求消费了一次性 Key
echo json_encode([
    'short_url'    => $shortUrl,
    'exp'          => $isTemp ? $exp_str : null,
    'dedup'        => false,
    'synced'       => $synced,
    'warning'      => $warning,
    'key_consumed' => ($AUTH['type'] ?? null) === 'onetime',
], JSON_UNESCAPED_UNICODE);
