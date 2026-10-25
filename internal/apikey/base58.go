package apikey

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// Base58Encode 与 v2.0.3 的 base58_encode 逐字节等价。
func Base58Encode(in []byte) string {
	if len(in) == 0 {
		return ""
	}
	leadingZeros := 0
	for leadingZeros < len(in) && in[leadingZeros] == 0 {
		leadingZeros++
	}
	digits := make([]int, len(in))
	for i, b := range in {
		digits[i] = int(b)
	}
	encoded := make([]byte, 0, len(in)*2)
	start := leadingZeros
	for start < len(digits) {
		remainder := 0
		for i := start; i < len(digits); i++ {
			value := remainder*256 + digits[i]
			digits[i] = value / 58
			remainder = value % 58
		}
		encoded = append(encoded, base58Alphabet[remainder])
		for start < len(digits) && digits[start] == 0 {
			start++
		}
	}
	out := make([]byte, 0, leadingZeros+len(encoded))
	for i := 0; i < leadingZeros; i++ {
		out = append(out, '1')
	}
	for i := len(encoded) - 1; i >= 0; i-- {
		out = append(out, encoded[i])
	}
	return string(out)
}
