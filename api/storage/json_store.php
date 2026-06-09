<?php
/**
 * JsonStore — JSON 文件读写（安全写入：备份 + 原子写入 + 验证 + 回滚）
 *
 * 数据格式：{"v":1, "at":"ISO8601", "d":{"code": {entry}, ...}}
 * 所有写入通过 .php.tmp + 原子 rename 完成（v2.1 safe_write 规范）
 * PHP 使用 .php.tmp 后缀，与 OpenResty 的 .lua.tmp 后缀区分，避免并发误读
 *
 * 写入保护（v2.1）：
 *   1. 备份：写前将当前文件复制为 .bak
 *   2. 原子写入：.php.tmp → rename() → 目标文件
 *   3. 验证：写后立即读回并 json_decode 校验完整性
 *   4. 回滚：验证失败时从 .bak 恢复
 *
 * 关于 read() 不加锁的设计说明：
 * Linux 上 rename() 是原子操作，file_get_contents 始终读到完整文件（旧版或新版），
 * 不存在半写/损坏状态。需要原子读-检查-写的操作应使用 lockBegin()/lockEnd()
 * 在整个临界区持有 LOCK_EX。
 */

class JsonStore {
    private $path;
    private $tz_offset;
    private $lockFp = null;

    public function __construct($path, $tz_offset = '+08:00') {
        $dir = dirname($path);
        if (!is_dir($dir) && !mkdir($dir, 0755, true)) {
            throw new \RuntimeException("无法创建目录: {$dir}");
        }
        $this->path = $path;
        $this->tz_offset = $tz_offset;
    }

    /**
     * 读取 JSON 文件，返回 'd' 数据对象
     * 文件不存在或无效时返回空数组
     *
     * 注意：无锁读取。rename() 保证始终读到完整文件。
     * 需要原子读-检查-写时，请使用 lockBegin()/lockEnd()。
     *
     * @return array  code => entry 关联数组
     */
// @关键_$13：read — 无锁读取 JSON 文件，返回 d 数据对象（rename 保证原子性）
    public function read() {
        if (!file_exists($this->path)) {
            return [];
        }
        $raw = @file_get_contents($this->path);
        if ($raw === false) {
            error_log("[json_store] 文件读取失败，路径: {$this->path}");
            return [];
        }
        $data = json_decode($raw, true);
        if (!is_array($data) || !isset($data['d']) || !is_array($data['d'])) {
            // 文件存在但内容损坏，记录日志（read 不抛异常，因为不直接触发写入）
            error_log("[json_store] JSON 损坏，路径: {$this->path}");
            return [];
        }
        return $data['d'];
    }

    /**
     * 安全写入 JSON 文件（v2.1 safe_write 规范）
     * 流程：备份 → 原子写入 → 验证 → 失败回滚
     *
     * @param array $data  code => entry 关联数组
     * @throws \RuntimeException  备份失败、写入失败、验证失败
     */
// @关键_$14：write — 安全写入 JSON 文件（备份 + .php.tmp + rename + 验证 + 回滚）
    public function write(array $data) {
        $envelope = [
            'v'  => 1,
            'at' => formatIso8601(time(), $this->tz_offset),
            'd'  => $data,
        ];
        $json = json_encode($envelope, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);

        $bak = $this->path . '.bak';
        $tmp = $this->path . '.php.tmp';

        // 1. 备份当前文件
        if (file_exists($this->path)) {
            if (!copy($this->path, $bak)) {
                throw new \RuntimeException("备份失败：$bak");
            }
        }

        // 2. 原子写入（.php.tmp + rename）
        $written = file_put_contents($tmp, $json, LOCK_EX);
        if ($written === false) {
            @unlink($tmp);
            throw new \RuntimeException("写入临时文件失败：$tmp");
        }
        if (!rename($tmp, $this->path)) {
            @unlink($tmp);
            throw new \RuntimeException("rename 失败：$tmp → {$this->path}");
        }

        // 3. 验证写入完整性
        $verify = @file_get_contents($this->path);
        if ($verify === false || json_decode($verify, true) === null) {
            // 3b. 验证失败 → 回滚
            if (file_exists($bak) && @copy($bak, $this->path)) {
                throw new \RuntimeException("写入验证失败，已回滚：{$this->path}");
            }
            error_log("[safe_write] 写入验证失败且回滚失败：{$this->path}");
            throw new \RuntimeException("写入验证失败且回滚失败，请检查磁盘：{$this->path}");
        }
        // 验证通过，备份保留至下次覆盖
    }

    /**
     * 按短码查找条目（不区分大小写，存储统一小写）
     * @param string $code
     * @return array|null
     */
// @关键_$15：find — 按短码查找条目（大小写不敏感）
    public function find($code) {
        $code = strtolower($code);
        $data = $this->read();
        return isset($data[$code]) ? $data[$code] : null;
    }

    /**
     * 统计总条目数
     * @return int
     */
// @关键_$16：count — 统计总条目数
    public function count() {
        return count($this->read());
    }

    /**
     * 统计有效（未过期）条目数
     * 通过 strtotime() 做 Unix 时间戳比较，时区安全
     * @return int
     */
// @关键_$17：countActive — 统计有效（未过期）条目数
    public function countActive() {
        $count = 0;
        foreach ($this->read() as $code => $item) {
            if (!isExpired($item['t'] ?? null)) {
                $count++;
            }
        }
        return $count;
    }

    /**
     * 获取排他文件锁，用于原子读-检查-写操作
     *
     * 以 'c+' 模式打开文件（不存在则创建，可读写）并获取 LOCK_EX。
     * 获取锁后用 readLocked() 读取、writeLocked() 写入，最后 lockEnd() 释放。
     *
     * 用法：
     *   $store->lockBegin();
     *   try {
     *       $data = $store->readLocked();
     *       // ... 配额检查、修改数据 ...
     *       $store->writeLocked($data);
     *   } finally {
     *       $store->lockEnd();
     *   }
     */
// @关键_$18：lockBegin — 获取排他文件锁，用于原子读-检查-写操作
    public function lockBegin() {
        // 确保目录存在（防止运行时目录被意外删除导致 fopen 失败）
        $dir = dirname($this->path);
        if (!is_dir($dir) && !mkdir($dir, 0755, true)) {
            throw new \RuntimeException("无法创建目录: {$dir}");
        }
        $this->lockFp = fopen($this->path, 'c+');
        if (!$this->lockFp) {
            throw new \RuntimeException("无法打开文件获取锁: " . $this->path);
        }
        flock($this->lockFp, LOCK_EX);
    }

    /**
     * 在持有排他锁的状态下读取数据
     * 从已锁定的文件指针读取，确保一致性
     * @return array  code => entry 关联数组
     */
// @关键_$19：readLocked — 在持有排他锁状态下读取数据
    public function readLocked() {
        if (!$this->lockFp) {
            throw new \RuntimeException("未持有锁，请先调用 lockBegin()");
        }
        fseek($this->lockFp, 0);
        $raw = stream_get_contents($this->lockFp);
        // 空文件 = 首次部署，合理返回空
        if (empty($raw) || trim($raw) === '') {
            return [];
        }
        $data = json_decode($raw, true);
        if (!is_array($data) || !isset($data['d']) || !is_array($data['d'])) {
            error_log("[json_store] JSON 损坏，路径: {$this->path}");
            throw new \RuntimeException("数据文件 JSON 损坏: " . $this->path);
        }
        return $data['d'];
    }

    /**
     * 在持有排他锁的状态下安全写入数据（v2.1 safe_write 规范）
     * 流程：备份 → 写入 → 验证 → 失败回滚
     *
     * 注意：此方法在文件锁内直接操作文件指针，不使用 tmp+rename（因为锁绑定在文件句柄上）
     * 改为先保存备份到 .bak 文件，再原地写入，最后读回验证
     *
     * @param array $data  code => entry 关联数组
     * @throws \RuntimeException  备份失败、验证失败且回滚失败
     */
// @关键_$20：writeLocked — 在持有排他锁状态下安全写入数据（备份 + 写入 + 验证 + 回滚）
    public function writeLocked(array $data) {
        if (!$this->lockFp) {
            throw new \RuntimeException("未持有锁，请先调用 lockBegin()");
        }
        $envelope = [
            'v'  => 1,
            'at' => formatIso8601(time(), $this->tz_offset),
            'd'  => $data,
        ];
        $json = json_encode($envelope, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);

        $bak = $this->path . '.bak';

        // 1. 备份当前文件内容到 .bak
        fseek($this->lockFp, 0);
        $currentContent = stream_get_contents($this->lockFp);
        if ($currentContent !== false && strlen($currentContent) > 0) {
            if (@file_put_contents($bak, $currentContent, LOCK_EX) === false) {
                throw new \RuntimeException("备份失败：$bak");
            }
        }

        // 2. 原地写入（截断 + 写入 + 刷盘）
        fseek($this->lockFp, 0);
        ftruncate($this->lockFp, 0);
        fwrite($this->lockFp, $json);
        fflush($this->lockFp);

        // 3. 验证写入完整性
        fseek($this->lockFp, 0);
        $verify = stream_get_contents($this->lockFp);
        if ($verify === false || json_decode($verify, true) === null) {
            // 3b. 验证失败 → 从备份回滚
            if (file_exists($bak) && ($bakContent = @file_get_contents($bak)) !== false) {
                fseek($this->lockFp, 0);
                ftruncate($this->lockFp, 0);
                fwrite($this->lockFp, $bakContent);
                fflush($this->lockFp);
                throw new \RuntimeException("写入验证失败，已回滚：{$this->path}");
            }
            error_log("[safe_write] 写入验证失败且回滚失败：{$this->path}");
            throw new \RuntimeException("写入验证失败且回滚失败，请检查磁盘：{$this->path}");
        }
        // 验证通过，备份保留至下次覆盖
    }

    /**
     * 释放排他文件锁
     */
// @关键_$21：lockEnd — 释放排他文件锁
    public function lockEnd() {
        if ($this->lockFp) {
            flock($this->lockFp, LOCK_UN);
            fclose($this->lockFp);
            $this->lockFp = null;
        }
    }
}
