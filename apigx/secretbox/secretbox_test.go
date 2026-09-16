package secretbox

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func withKey(t *testing.T, hexKey string) {
	t.Helper()
	previous, had := os.LookupEnv(KeyEnvVar)
	require.NoError(t, os.Setenv(KeyEnvVar, hexKey))
	ResetKeyCacheForTest()
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(KeyEnvVar, previous)
		} else {
			_ = os.Unsetenv(KeyEnvVar)
		}
		ResetKeyCacheForTest()
	})
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	withKey(t, testKeyHex)

	plaintext := "sk-upstream-SECRET-abcdefghijklmnop"
	envelope, err := Encrypt(plaintext)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(envelope, EnvelopePrefix))
	assert.True(t, IsEnvelope(envelope))

	// 密文里绝不能出现明文片段：这是「不落明文」的最直接证据
	assert.NotContains(t, envelope, plaintext)
	assert.NotContains(t, envelope, "SECRET")

	got, err := Decrypt(envelope)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got)
}

// 同一个明文两次加密必须得到**不同的**信封（随机 nonce）：否则密文可被用来判断「两个分享者是否用了同一把 Key」。
func TestEncryptIsNonDeterministic(t *testing.T) {
	withKey(t, testKeyHex)

	a, err := Encrypt("same-secret")
	require.NoError(t, err)
	b, err := Encrypt("same-secret")
	require.NoError(t, err)

	assert.NotEqual(t, a, b, "相同明文不得产生相同密文")
	assert.Len(t, strings.Split(strings.TrimPrefix(a, EnvelopePrefix), ":"), 2)

	gotA, errA := Decrypt(a)
	gotB, errB := Decrypt(b)
	require.NoError(t, errA)
	require.NoError(t, errB)
	assert.Equal(t, gotA, gotB)
}

// GCM 保证完整性：密文被改动必须解密失败，而不是返回垃圾明文。
//
// 篡改方式必须**确定性地改变解码后的字节**：早先的写法是替换 base64 的最后一个字符，
// 但 last-char 的部分比特在 RawURL 编码里可能被忽略，于是「篡改」有时根本没发生，
// 测试变成偶发通过/失败。改成翻转解码后的第一个字节，再重新编码。
func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	withKey(t, testKeyHex)

	envelope, err := Encrypt("sk-tamper-me")
	require.NoError(t, err)

	parts := strings.Split(strings.TrimPrefix(envelope, EnvelopePrefix), ":")
	require.Len(t, parts, 2)

	sealed, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	require.NotEmpty(t, sealed)

	sealed[0] ^= 0xFF // 确定性翻转一个字节
	tampered := EnvelopePrefix + parts[0] + ":" + base64.RawURLEncoding.EncodeToString(sealed)

	_, err = Decrypt(tampered)
	assert.ErrorIs(t, err, ErrDecrypt, "密文被改动必须完整性校验失败")

	// 篡改 nonce 同样必须失败
	nonce, err := base64.RawURLEncoding.DecodeString(parts[0])
	require.NoError(t, err)
	nonce[0] ^= 0xFF
	tamperedNonce := EnvelopePrefix + base64.RawURLEncoding.EncodeToString(nonce) + ":" + parts[1]
	_, err = Decrypt(tamperedNonce)
	assert.ErrorIs(t, err, ErrDecrypt, "nonce 被改动必须失败")

	// 原信封仍然可解（证明上面两步确实只动了副本）
	plain, err := Decrypt(envelope)
	require.NoError(t, err)
	assert.Equal(t, "sk-tamper-me", plain)
}

// 换一把主密钥后旧密文不可解——这正是「主密钥与数据库分离」的意义所在。
func TestDecryptFailsWithWrongKey(t *testing.T) {
	withKey(t, testKeyHex)
	envelope, err := Encrypt("sk-under-key-1")
	require.NoError(t, err)

	otherKey := strings.Repeat("ab", KeyBytes)
	withKey(t, otherKey)
	_, err = Decrypt(envelope)
	assert.ErrorIs(t, err, ErrDecrypt)
}

// 主密钥未配置时必须**失败关闭**：拒绝加密，而不是退化成明文存储。
func TestEncryptFailsClosedWithoutKey(t *testing.T) {
	withKey(t, "")

	assert.False(t, IsConfigured())

	_, err := Encrypt("sk-must-not-be-plaintext")
	assert.ErrorIs(t, err, ErrKeyNotConfigured, "缺主密钥时必须拒绝写入，不能明文落库")

	_, err = Decrypt(EnvelopePrefix + "AAAA:BBBB")
	assert.ErrorIs(t, err, ErrKeyNotConfigured)
}

func TestKeyValidation(t *testing.T) {
	for _, tt := range []struct {
		name string
		key  string
	}{
		{"非十六进制", "not-hex-at-all"},
		{"长度不足", "00112233"},
		{"长度过长", testKeyHex + "aa"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			withKey(t, tt.key)
			assert.False(t, IsConfigured())
			_, err := Encrypt("x")
			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrDecrypt)
		})
	}
}

func TestEnvelopeFormatValidation(t *testing.T) {
	withKey(t, testKeyHex)

	for _, bad := range []string{
		"",
		"sk-plaintext-not-an-envelope",
		"enc:v2:AAAA:BBBB",     // 版本不受支持
		"enc:v1:only-one-part", // 缺少密文段
		"enc:v1:AAAA:BBBB:CC",  // 段数过多
		"enc:v1:!!!:###",       // base64 非法
	} {
		_, err := Decrypt(bad)
		assert.ErrorIs(t, err, ErrInvalidEnvelope, "输入：%q", bad)
	}

	_, err := Encrypt("")
	assert.ErrorIs(t, err, ErrInvalidEnvelope, "空明文应被拒绝")
}
