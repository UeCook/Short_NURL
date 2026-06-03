<?php
/**
 * KeyStore — API Key 管理（SHA-256 哈希，安全写入）
 *
 * 哈希运算已解耦至 auth_hash.php，本文件通过 auth_hash() 函数调用。
 * 换哈希算法时改 auth_hash.php 即可，此处不动。
 */
require_once __DIR__ . '/auth_hash.php';
require_once __DIR__ . '/base58.php';

/**
 * 两种 Key 类型：
 *   - 常驻 Key（Resident）：单个密钥，7 天有效期，不限使用次数
 *   - 一次性 Key 池（Onetime Pool）：最多 N 个（默认 20），用前永不过期，
 *     使用后自动销毁（不自动补充，需 CLI 手动补充）
 *
 * 写入保护（v2.1 safe_write 规范）：
 *   所有写入操作（writeLockedFile）均包含备份 + 验证 + 回滚三层保护
 *
 * keys.json 结构：
 * {
 *   "resident": {
 *     "key_hash": "<sha256>",
 *     "key_prefix": "su_xK9m",
 *     "created": "2026-05-17T10:00:00+08:00",
 *     "expires": "2026-05-24T10:00:00+08:00"
 *   },
 *   "onetime_pool": [
 *     {
 *       "key_hash": "<sha256>",
 *       "key_prefix": "su_aB3c",
 *       "created": "2026-05-17T10:00:00+08:00"
 *     }
 *   ],
 * }
 *
 * 安全设计：
 *   - 仅存 SHA-256 哈希，不存明文
 *   - hash_equals 恒定时间比较，防时序攻击
 *   - API 不返回 Key 明文（仅 CLI 显示）
 *   - 一次性 Key 单次使用后自动销毁（不自动补充，需 CLI 手动补充）
 */

class KeyStore {
    private $path;
    private $tz_offset;
    private $ttlDays;
    private $poolSize;

    public function __construct($path, $tz_offset = '+08:00', $ttlDays = 7, $poolSize = 20) {
        $dir = dirname($path);
        if (!is_dir($dir)) mkdir($dir, 0755, true);
        $this->path = $path;
        $this->tz_offset = $tz_offset;
        $this->ttlDays = $ttlDays;
        $this->poolSize = $poolSize;
    }

    /**
     * 生成原始密钥：32 字节随机数 → Base58 编码 → 添加 su_ 前缀
     * Base58 编码将 32 字节压缩为约 44 个字符（相比十六进制的 64 字符更短）
     *
     * @return string  完整明文密钥（su_ + base58 编码）
     */
// @关键_$35：generateRawKey — 生成原始密钥（32 字节随机数 → Base58 编码 → su_ 前缀）
    private function generateRawKey() {
        $bytes = random_bytes(32);
        // @外调用_&15：base58_encode — Base58 编码（generateRawKey 内，将 32 字节随机数编码为 Base58）
        $encoded = base58_encode($bytes);
        return 'su_' . $encoded;
    }

    // ── 文件读写 ───────────────────────────────────────

// @关键_$4：readFile — 读取 keys.json 文件，返回常驻 Key 和一次性 Key 池数据
    private function readFile() {
        if (!file_exists($this->path)) {
            return ['resident' => null, 'onetime_pool' => []];
        }
        $raw = file_get_contents($this->path);
        $data = json_decode($raw, true);
        if (!is_array($data)) {
            error_log("[key_readFile] keys.json 解码失败，内容可能损坏: " . $this->path);
            return ['resident' => null, 'onetime_pool' => []];
        }
        if (!isset($data['onetime_pool']) || !is_array($data['onetime_pool'])) {
            $data['onetime_pool'] = [];
        }
        return $data;
    }

// @关键_$6：writeLockedFile — 在持有文件锁状态下安全写入 keys 数据（备份 + 写入 + 验证 + 回滚）
    private function writeLockedFile($fp, array $data) {
        $json = json_encode($data, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);
        $bak = $this->path . '.bak';

        // 1. 备份当前文件内容
        fseek($fp, 0);
        $currentContent = stream_get_contents($fp);
        if ($currentContent !== false && strlen($currentContent) > 0) {
            if (@file_put_contents($bak, $currentContent, LOCK_EX) === false) {
                throw new \RuntimeException("备份失败：$bak");
            }
        }

        // 2. 原地写入
        fseek($fp, 0);
        ftruncate($fp, 0);
        fwrite($fp, $json);
        fflush($fp);

        // 3. 验证写入完整性
        fseek($fp, 0);
        $verify = stream_get_contents($fp);
        if ($verify === false || json_decode($verify, true) === null) {
            if (file_exists($bak) && ($bakContent = @file_get_contents($bak)) !== false) {
                fseek($fp, 0);
                ftruncate($fp, 0);
                fwrite($fp, $bakContent);
                fflush($fp);
                throw new \RuntimeException("写入验证失败，已回滚：{$this->path}");
            }
            error_log("[safe_write] 写入验证失败且回滚失败：{$this->path}");
            throw new \RuntimeException("写入验证失败且回滚失败，请检查磁盘：{$this->path}");
        }
    }

    // ── 验证 ─────────────────────────────────────────

    /**
     * 验证原始 Key 是否与存储的哈希匹配
     *
     * @param string $rawKey  请求中传入的 Key
     * @return string|false   'resident' 或 'onetime'，无效返回 false
     */
// @关键_$7：verify — 验证 API Key（支持常驻 Key 和一次性 Key，恒定时间比较防时序攻击）
    public function verify($rawKey) {
        if (empty($rawKey)) return false;
        if (!file_exists($this->path)) return false;

        // @外调用_&8：auth_hash — 哈希运算（verify 内，换算法改 auth_hash.php）
        $hash = auth_hash($rawKey);
        $now = time();

        $fp = fopen($this->path, 'r+');
        if (!$fp) return false;
        flock($fp, LOCK_EX);
        try {
            fseek($fp, 0);
            $raw = stream_get_contents($fp);
            $keys = json_decode($raw, true);
            if (!is_array($keys)) {
                error_log("[key_verify] keys.json 解码失败，内容可能损坏，路径: {$this->path}");
                return false;
            }
            if (!isset($keys['onetime_pool']) || !is_array($keys['onetime_pool'])) {
                $keys['onetime_pool'] = [];
            }

            // ── 检查常驻 Key ──────────────────────────
            if (isset($keys['resident']) && is_array($keys['resident'])) {
                $slot = $keys['resident'];
                if (isset($slot['key_hash']) && hash_equals($slot['key_hash'], $hash)) {
                    $expiresTs = strtotime($slot['expires']);
                    if ($expiresTs !== false && $expiresTs > $now) {
                        return 'resident';
                    }
                    // 已过期 — 销毁
                    $keys['resident'] = null;
                    $this->writeLockedFile($fp, $keys);
                    return false;
                }
            }

            // ── 检查一次性 Key 池 ────────────────────
            foreach ($keys['onetime_pool'] as $i => $slot) {
                if (isset($slot['key_hash']) && hash_equals($slot['key_hash'], $hash)) {
                    // 命中 — 从池中移除（不自动补充，因自动生成的 Key 明文无法被获取）
                    // 池补充应通过 CLI 工具 (su-keygen fill) 手动执行
                    array_splice($keys['onetime_pool'], $i, 1);
                    $this->writeLockedFile($fp, $keys);
                    return 'onetime';
                }
            }

            return false;

        } finally {
            flock($fp, LOCK_UN);
            fclose($fp);
        }
    }

    // ── 通用锁内执行 ────────────────────────────────────

    /**
     * 在 flock 排他锁内执行回调，保证 CLI 与 API 操作互斥
     * 所有对 keys.json 的写操作都通过此方法在同一个 inode 上互斥，
     * 避免 CLI 的 tmp+rename 与 API 的 flock 在不同 inode 上互相覆盖
     *
     * @param callable $callback  回调函数，接收 &$data 引用，可修改后自动写入
     * @return mixed  回调的返回值
     * @throws \RuntimeException
     */
// @关键_$28：withLock — 在 flock 排他锁内执行回调（保证 CLI 与 API 操作互斥，统一 inode）
    private function withLock($callback) {
        $dir = dirname($this->path);
        if (!is_dir($dir)) mkdir($dir, 0755, true);
        $fp = fopen($this->path, 'c+');
        if (!$fp) throw new \RuntimeException("无法打开文件获取锁: " . $this->path);
        flock($fp, LOCK_EX);
        try {
            fseek($fp, 0);
            $raw = stream_get_contents($fp);
            $data = json_decode($raw, true);
            if (!is_array($data)) {
                error_log("[key_withLock] keys.json 解码失败，内容可能损坏，路径: {$this->path}");
                throw new \RuntimeException("keys.json 数据损坏，请手动检查: " . $this->path);
            }
            if (!isset($data['onetime_pool']) || !is_array($data['onetime_pool'])) {
                $data['onetime_pool'] = [];
            }
            // 执行业务逻辑，回调修改 $data 后由 writeLockedFile 写入
            $result = $callback($data, $fp);
            return $result;
        } finally {
            flock($fp, LOCK_UN);
            fclose($fp);
        }
    }

    // ── 生成 ───────────────────────────────────────

    /**
     * 生成新的常驻 Key
     * @return string  明文 Key（仅此一次显示）
     */
// @关键_$9：generateResident — 生成新的常驻 Key（返回明文，仅此一次显示）
    public function generateResident() {
        return $this->withLock(function(&$data, $fp) {
            // @外调用_&16：generateRawKey — 密钥生成（generateResident 内，Base58 编码）
            $raw = $this->generateRawKey();

            // @外调用_&8：auth_hash — 哈希运算（generateResident 内，换算法改 auth_hash.php）
            $hash = auth_hash($raw);
            $prefix = substr($raw, 0, 8);
            $now = time();
            $expires = formatIso8601($now + ($this->ttlDays * 86400), $this->tz_offset);
            $created = formatIso8601($now, $this->tz_offset);

            $data['resident'] = [
                'key_hash'   => $hash,
                'key_prefix' => $prefix,
                'created'    => $created,
                'expires'    => $expires,
            ];
            $this->writeLockedFile($fp, $data);

            return $raw;
        });
    }

    /**
     * 将一次性 Key 池补充到最大容量
     * @return array  新生成的明文 Key 数组（仅此一次显示）
     */
// @关键_$10：fillPool — 将一次性 Key 池补充到最大容量
    public function fillPool() {
        return $this->withLock(function(&$data, $fp) {
            $pool = $data['onetime_pool'];
            $newKeys = [];

            while (count($pool) < $this->poolSize) {
                // @外调用_&16：generateRawKey — 密钥生成（fillPool 内，Base58 编码）
                $raw = $this->generateRawKey();

                // @外调用_&8：auth_hash — 哈希运算（fillPool 内，换算法改 auth_hash.php）
                $hash = auth_hash($raw);
                $prefix = substr($raw, 0, 8);
                $now = time();
                $created = formatIso8601($now, $this->tz_offset);

                $pool[] = [
                    'key_hash'   => $hash,
                    'key_prefix' => $prefix,
                    'created'    => $created,
                ];
                $newKeys[] = $raw;
            }

            $data['onetime_pool'] = $pool;
            $this->writeLockedFile($fp, $data);

            return $newKeys;
        });
    }

    // ── 吊销 ─────────────────────────────────────────

// @关键_$11：revoke — 吊销指定类型的 Key（常驻/一次性/全部）
    public function revoke($type = 'all') {
        $this->withLock(function(&$data, $fp) use ($type) {
            if ($type === 'all') {
                $data['resident'] = null;
                $data['onetime_pool'] = [];
            } elseif ($type === 'resident') {
                $data['resident'] = null;
            } elseif ($type === 'onetime') {
                $data['onetime_pool'] = [];
            } else {
                throw new \InvalidArgumentException("无效的吊销类型: {$type}");
            }
            $this->writeLockedFile($fp, $data);
        });
    }

    // ── 状态查询（仅 CLI 使用）───────────────────────

    /**
     * 获取当前 Key 状态，供 CLI 显示
     * @return array
     */
// @关键_$12：status — 获取当前 Key 状态（供 CLI 工具显示）
    public function status() {
        $keys = $this->readFile();
        $result = [
            'resident' => null,
            'onetime_count' => 0,
            'onetime_pool_size' => $this->poolSize,
            'onetime_pool' => [],
        ];

        if (isset($keys['resident']) && is_array($keys['resident'])) {
            $slot = $keys['resident'];
            $result['resident'] = [
                'prefix'  => $slot['key_prefix'] ?? '-',
                'created' => $slot['created'] ?? '-',
                'expires' => $slot['expires'] ?? '-',
            ];
        }

        $result['onetime_count'] = count($keys['onetime_pool'] ?? []);
        foreach ($keys['onetime_pool'] as $slot) {
            $result['onetime_pool'][] = [
                'prefix'  => $slot['key_prefix'] ?? '-',
                'created' => $slot['created'] ?? '-',
            ];
        }

        return $result;
    }
}
