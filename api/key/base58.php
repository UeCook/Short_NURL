<?php
/**
 * Base58 编码/解码模块
 *
 * 独立模块，零外部依赖（内置 bcmath → GMP → 纯 PHP 三级回退）。
 * 提供 base58 编码与解码函数，用于将原始二进制密钥转换为可读的 Base58 字符串。
 *
 * Base58 字母表（Bitcoin 标准）：
 *   去掉了容易混淆的字符：0（零）、O（大写O）、I（大写I）、l（小写L）
 *
 * 用法：
 *   $encoded = base58_encode(random_bytes(32));  // 32 字节 → ~44 字符
 *   $decoded = base58_decode($encoded);          // 还原为原始字节
 */

/**
 * Base58 编码字母表（Bitcoin 标准）
 * 123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz
 */
define('BASE58_ALPHABET', '123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz');

/* ── bcmath 多项式回退 ──────────────────────────────
 * base58 仅使用正整数十进制字符串算术（无小数），
 * 此回退覆盖该最小子集。
 * 优先级：bcmath 扩展 > GMP 扩展 > 纯 PHP 字符串算术
 */
if (!function_exists('bcmul')) {
    if (function_exists('gmp_init')) {
        function bcmul($a, $b) { return gmp_strval(gmp_mul(gmp_init($a), gmp_init($b))); }
        function bcadd($a, $b) { return gmp_strval(gmp_add(gmp_init($a), gmp_init($b))); }
        function bccomp($a, $b) { return gmp_cmp(gmp_init($a), gmp_init($b)); }
        function bcmod($a, $b) { return gmp_strval(gmp_mod(gmp_init($a), gmp_init($b))); }
        function bcdiv($a, $b, $scale = 0) { return gmp_strval(gmp_div(gmp_init($a), gmp_init($b))); }
    } else {
        function bccomp($a, $b) {
            $a = ltrim($a, '0') ?: '0';
            $b = ltrim($b, '0') ?: '0';
            if (strlen($a) !== strlen($b)) return strlen($a) > strlen($b) ? 1 : -1;
            return strcmp($a, $b) ?: 0;
        }
        function bcadd($a, $b) {
            $a = ltrim($a, '0') ?: '0';
            $b = ltrim($b, '0') ?: '0';
            $len = max(strlen($a), strlen($b));
            $a = str_pad($a, $len, '0', STR_PAD_LEFT);
            $b = str_pad($b, $len, '0', STR_PAD_LEFT);
            $result = '';
            $carry = 0;
            for ($i = $len - 1; $i >= 0; $i--) {
                $sum = ord($a[$i]) + ord($b[$i]) - 96 + $carry;
                $carry = (int) ($sum >= 10);
                $result = ($sum % 10) . $result;
            }
            if ($carry) $result = '1' . $result;
            return $result;
        }
        function bcmul($a, $b) {
            $a = ltrim($a, '0') ?: '0';
            $b = ltrim($b, '0') ?: '0';
            if ($a === '0' || $b === '0') return '0';
            $lenA = strlen($a);
            $lenB = strlen($b);
            $result = array_fill(0, $lenA + $lenB, 0);
            for ($i = $lenA - 1; $i >= 0; $i--) {
                $carry = 0;
                $dA = ord($a[$i]) - 48;
                for ($j = $lenB - 1; $j >= 0; $j--) {
                    $dB = ord($b[$j]) - 48;
                    $product = $dA * $dB + $result[$i + $j + 1] + $carry;
                    $carry = (int) ($product / 10);
                    $result[$i + $j + 1] = $product % 10;
                }
                $result[$i] += $carry;
            }
            $str = implode('', $result);
            return ltrim($str, '0') ?: '0';
        }
        function bcmod($a, $b) {
            $a = ltrim($a, '0') ?: '0';
            $b = ltrim($b, '0') ?: '0';
            $rem = '0';
            $len = strlen($a);
            for ($i = 0; $i < $len; $i++) {
                $rem = bcmul($rem, '10');
                $rem = bcadd($rem, (string) (ord($a[$i]) - 48));
                while (bccomp($rem, $b) >= 0) {
                    $rem = bcsub_internal($rem, $b);
                }
            }
            return $rem;
        }
        function bcdiv($a, $b, $scale = 0) {
            $a = ltrim($a, '0') ?: '0';
            $b = ltrim($b, '0') ?: '0';
            if (bccomp($a, $b) < 0) return '0';
            $result = '';
            $rem = '0';
            $len = strlen($a);
            for ($i = 0; $i < $len; $i++) {
                $rem = bcmul($rem, '10');
                $rem = bcadd($rem, (string) (ord($a[$i]) - 48));
                $quot = 0;
                while (bccomp($rem, $b) >= 0) {
                    $rem = bcsub_internal($rem, $b);
                    $quot++;
                }
                $result .= $quot;
            }
            return ltrim($result, '0') ?: '0';
        }
        // 内部减法辅助（保证 $a >= $b）
        function bcsub_internal($a, $b) {
            $len = max(strlen($a), strlen($b));
            $a = str_pad($a, $len, '0', STR_PAD_LEFT);
            $b = str_pad($b, $len, '0', STR_PAD_LEFT);
            $result = '';
            $borrow = 0;
            for ($i = $len - 1; $i >= 0; $i--) {
                $diff = (ord($a[$i]) - 48) - (ord($b[$i]) - 48) - $borrow;
                if ($diff < 0) { $diff += 10; $borrow = 1; } else { $borrow = 0; }
                $result = $diff . $result;
            }
            return ltrim($result, '0') ?: '0';
        }
    }
}

/**
 * 将二进制数据编码为 Base58 字符串
 *
 * @param string $bytes  原始二进制数据
 * @return string        Base58 编码字符串
 */
// @关键_$33：base58_encode — Base58 编码函数（Bitcoin 标准字母表，bcmath 大整数运算）
function base58_encode(string $bytes): string {
    if ($bytes === '') return '';

    $len = strlen($bytes);

    // 统计前导零字节（在 base58 中每个前导零字节对应一个 '1'）
    $leadingZeros = 0;
    for ($i = 0; $i < $len; $i++) {
        if ($bytes[$i] === "\x00") {
            $leadingZeros++;
        } else {
            break;
        }
    }

    // 将二进制数据转换为大整数（字符串表示，每位是 0-9 十进制数字）
    // 采用 256 进制 → 十进制的转换
    $num = '0';
    for ($i = 0; $i < $len; $i++) {
        // num = num * 256 + ord(byte)
        $num = bcmul($num, '256');
        $num = bcadd($num, (string) ord($bytes[$i]));
    }

    // 大整数 → 58 进制
    $result = '';
    while (bccomp($num, '0') > 0) {
        $remainder = bcmod($num, '58');
        $result = BASE58_ALPHABET[(int) $remainder] . $result;
        $num = bcdiv($num, '58', 0);
    }

    // 添加前导零字节对应的 '1'
    for ($i = 0; $i < $leadingZeros; $i++) {
        $result = '1' . $result;
    }

    return $result;
}

/**
 * 将 Base58 字符串解码为二进制数据
 *
 * @param string $base58  Base58 编码字符串
 * @return string         原始二进制数据
 */
// @关键_$34：base58_decode — Base58 解码函数（Bitcoin 标准字母表，bcmath 大整数运算）
function base58_decode(string $base58): string {
    if ($base58 === '') return '';

    $len = strlen($base58);

    // 统计前导 '1'（对应前导零字节）
    $leadingOnes = 0;
    for ($i = 0; $i < $len; $i++) {
        if ($base58[$i] === '1') {
            $leadingOnes++;
        } else {
            break;
        }
    }

    // Base58 字符 → 数值映射
    $alphabetMap = [];
    for ($i = 0; $i < 58; $i++) {
        $alphabetMap[BASE58_ALPHABET[$i]] = $i;
    }

    // 58 进制 → 大整数
    $num = '0';
    for ($i = 0; $i < $len; $i++) {
        $char = $base58[$i];
        if (!isset($alphabetMap[$char])) {
            throw new \InvalidArgumentException("无效的 Base58 字符：{$char}");
        }
        $num = bcmul($num, '58');
        $num = bcadd($num, (string) $alphabetMap[$char]);
    }

    // 大整数 → 二进制（256 进制）
    $result = '';
    while (bccomp($num, '0') > 0) {
        $remainder = bcmod($num, '256');
        $result = chr((int) $remainder) . $result;
        $num = bcdiv($num, '256', 0);
    }

    // 添加前导零字节
    for ($i = 0; $i < $leadingOnes; $i++) {
        $result = "\x00" . $result;
    }

    return $result;
}
