<?php
/**
 * POST /api/create - 创建短链
 */
// @外接口_#1：POST /api/create — 创建短链（前端链路入口）
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

// ── 验证目标链接 ──────────────────────────────────────
if (!isset($input['url']) || !is_string($input['url'])) {
    http_response_code(400);
    echo json_encode(['error' => '目标链接必须为字符串'], JSON_UNESCAPED_UNICODE);
    exit;
}
$url = trim($input['url']);
if ($url === '') {
    http_response_code(400);
    echo json_encode(['error' => '缺少目标链接'], JSON_UNESCAPED_UNICODE);
    exit;
}
if (!filter_var($url, FILTER_VALIDATE_URL) || !preg_match('#^https?://#i', $url)) {
    http_response_code(400);
    echo json_encode(['error' => '目标链接无效'], JSON_UNESCAPED_UNICODE);
    exit;
}
// URL 长度限制（与 Lua 侧 internal_set.lua MAX_URL_LEN 对齐）
if (strlen($url) > 2048) {
    http_response_code(400);
    echo json_encode(['error' => '目标链接过长（最大 2048 字符）'], JSON_UNESCAPED_UNICODE);
    exit;
}
// SSRF 防护：拒绝指向内网/保留地址的链接
if (isPrivateUrl($url)) {
    http_response_code(400);
    echo json_encode(['error' => '目标链接指向内网地址，已被拒绝'], JSON_UNESCAPED_UNICODE);
    exit;
}
if (!array_key_exists('ttl', $input)) {
    $ttl = 0;
} elseif (!is_int($input['ttl'])) {
    http_response_code(400);
    echo json_encode(['error' => 'TTL 必须为整数'], JSON_UNESCAPED_UNICODE);
    exit;
} else {
    $ttl = $input['ttl'];
}
if ($ttl < 0 || $ttl > $cfg['ttl_max']) {
    http_response_code(400);
    echo json_encode(['error' => 'TTL 超限'], JSON_UNESCAPED_UNICODE);
    exit;
}
$isTemp = $ttl > 0;

$permStore = new JsonStore($cfg['perm_path'], $cfg['tz_offset']);
$tempStore = new JsonStore($cfg['temp_path'], $cfg['tz_offset']);

$code = isset($input['code']) && is_string($input['code']) ? trim($input['code']) : '';
$isCustom = !empty($code);

// ── 服务密钥专用配置 ─────────────────────────────────
$isServiceKey = ($AUTH['type'] ?? null) === 'service';
$svcCodeLength = $isServiceKey ? ($cfg['svc_code_length'] ?? 4) : 4;
$maxCodeLen = $isServiceKey ? 5 : 4;

// ── 永久链去重（写入前查冷存储，URL 完全匹配则复用）──
// @关键_$26：永久链去重 — ttl==0 且未传自定义短码时遍历 perm.json，URL 严格匹配则返回已有短码
// 传了自定义短码说明用户有明确意图，去重不干预
// 去重命中也返回固定响应结构（dedup=true），HTTP 统一为 200
// 调用 internalSet 确保热存储同步（防止冷启动后热存储缺失该条目）
if (!$isTemp && !$isCustom) {
    // 数据文件损坏时 read() 抛 RuntimeException：此处必须捕获并输出结构化 JSON，
    // 否则异常直达 PHP 顶层产生空白 500，前端只能兜底显示"服务器响应格式错误"
    try {
        $permData = $permStore->read();
    } catch (\RuntimeException $e) {
        error_log('[create] 冷存储读取失败: ' . $e->getMessage());
        http_response_code(500);
        echo json_encode(['error' => '数据文件损坏，请检查服务端日志'], JSON_UNESCAPED_UNICODE);
        exit;
    }
    foreach ($permData as $dedupCode => $existing) {
        if (isset($existing['url'], $existing['lurl']) && $existing['url'] === $url) {
            // @外调用_&7：internalSet — 前端创建去重路径写入热存储（带重试）
            $dedupSynced = internalSetWithRetry($cfg, $dedupCode, $existing['url'], 0, '0');
            $dedupWarning = null;
            if (!$dedupSynced) {
                $detail = getLastInternalError();
                $diagMsg = $detail ? $detail['message'] : '未知错误';
                error_log("[create] 去重路径热存储同步失败：code={$dedupCode}，原因：{$diagMsg}");
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
    if (!preg_match('/^[0-9a-z-]{1,' . $maxCodeLen . '}$/', $code)) { http_response_code(400); echo json_encode(['error'=>'后缀格式错误'], JSON_UNESCAPED_UNICODE); exit; }
    if (in_array($code, $cfg['reserved_codes'])) { http_response_code(400); echo json_encode(['error'=>'保留字'], JSON_UNESCAPED_UNICODE); exit; }
}

$now = time();
$domain = $cfg['domain'];
$tz = $cfg['tz_offset'];
// tz_offset 配置错误时 formatIso8601 抛 InvalidArgumentException：
// 捕获并输出结构化 JSON，避免空白 500 让用户无从排查
try {
    $exp_str = $isTemp ? formatIso8601($now + $ttl, $tz) : '0';
} catch (\InvalidArgumentException $e) {
    error_log('[create] 时区配置无效: ' . $e->getMessage());
    http_response_code(500);
    echo json_encode(['error' => '时区配置无效，请检查 config.php 的 tz_offset'], JSON_UNESCAPED_UNICODE);
    exit;
}
$shortUrl = $domain . '/' . $code;
$entry = ['id' => $code, 'url' => $url, 'lurl' => $shortUrl];
if ($isTemp) $entry['t'] = $exp_str;

$store = $isTemp ? $tempStore : $permStore;
$otherStore = $isTemp ? $permStore : $tempStore;

// ── 双 Store 原子读-检查-写（固定锁序，避免跨 store TOCTOU）──
$errorResponse = null;
try {
    $storePath = $store->getPath();
    $otherPath = $otherStore->getPath();
    $transactionResult = JsonStore::withBothLocks($permStore, $tempStore, function (&$allData) use (
        &$code, &$shortUrl, &$entry, &$errorResponse, $isTemp, $isCustom, $svcCodeLength,
        $cfg, $isServiceKey, $domain, $storePath, $otherPath
    ) {
        $data = &$allData[$storePath];
        $otherData = &$allData[$otherPath];
        if ($isTemp) cleanExpiredEntries($data);
        if ($isCustom) {
            if (isset($data[$code]) || isset($otherData[$code])) {
                $errorResponse = [409, '已占用'];
            }
        } else {
            $chars = '23456789abcdefghjkmnpqrstuvwxyz';
            $maxLen = strlen($chars) - 1;
            $generated = false;
            for ($i = 0; $i < 8; $i++) {
                $candidate = '';
                for ($j = 0; $j < $svcCodeLength; $j++) $candidate .= $chars[random_int(0, $maxLen)];
                if (in_array($candidate, $cfg['reserved_codes'])) continue;
                if (isset($data[$candidate]) || isset($otherData[$candidate])) continue;
                $code = $candidate; $generated = true; break;
            }
            if (!$generated) {
                error_log('[create] 短码生成失败：两个存储中均无法找到可用后缀');
                $errorResponse = [500, '无法生成后缀'];
            } else {
                $shortUrl = $domain . '/' . $code;
                $entry['id'] = $code; $entry['lurl'] = $shortUrl;
            }
        }
        if (!$errorResponse) {
            $activeCount = 0;
            foreach ($data as $item) { if (!isExpired($item['t'] ?? null)) $activeCount++; }
            $limit = $isServiceKey
                ? ($cfg[$isTemp ? 'svc_temp_limit' : 'svc_perm_limit'] ?? 9999)
                : $cfg[$isTemp ? 'temp_limit' : 'perm_limit'];
            $totalLimit = ($cfg['perm_limit'] ?? 10000) + ($cfg['temp_limit'] ?? 500) + ($cfg['svc_perm_limit'] ?? 18000) + ($cfg['svc_temp_limit'] ?? 1500);
            if ($totalLimit > 30000) $errorResponse = [500, '短链限制总和超过30000，请调整config.php'];
            if ($activeCount >= $limit) $errorResponse = [429, '已达上限'];
        }
        if (!$errorResponse) {
            $data[$code] = $entry;
            return ['write' => [$storePath], 'result' => true];
        }
        return ['write' => [], 'result' => false];
    });
} catch (\Exception $e) {
    error_log('[safe_write] ' . $e->getMessage());
    http_response_code(500);
    echo json_encode(['error' => '服务器内部错误'], JSON_UNESCAPED_UNICODE);
    exit;
}

if ($errorResponse) {
    http_response_code($errorResponse[0]);
    echo json_encode(['error' => $errorResponse[1]], JSON_UNESCAPED_UNICODE);
    exit;
}

// @外调用_&9：internalSet — 前端创建接口写入热存储（带重试）
$synced = internalSetWithRetry($cfg, $code, $url, $isTemp ? $ttl : 0, $exp_str);
$warning = null;
if (!$synced) {
    $detail = getLastInternalError();
    $diagMsg = $detail ? $detail['message'] : '未知错误';
    error_log("[create] 热存储同步失败：code={$code}，原因：{$diagMsg}");
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
