// Package secretbox 提供「上游凭据的静态加密封装」。
//
// 计划书 §10.3 对两类凭据的要求不同，必须分清：
//   - **平台 API Key**（使用者凭证）：按用户决定跟随底座现状（`tokens.key` 明文 + 等值查找），
//     见 docs/architecture/tech-debt.md 的 TD-07；本包不涉及。
//   - **上游 Key**（分享者的供应商凭据）：平台**必须**加密存储，主密钥与数据库分离，
//     界面不回显完整凭据，日志/错误/监控都不得携带。
//
// 设计要点：
//   - AES-256-GCM：同时保证机密性与完整性（密文被篡改会解密失败，而不是返回垃圾明文）。
//   - 信封格式 `enc:v1:<base64(nonce)>:<base64(ciphertext)>`：带版本号，便于将来换算法而不破坏旧数据。
//   - 主密钥来自环境变量 `APIGX_SECRET_KEY`（64 位十六进制 = 32 字节），**不入库**。
//   - 缺少或非法的主密钥时**失败关闭**：拒绝写入，绝不退化成明文存储。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

const (
	// EnvelopePrefix 是信封前缀，形如 `enc:v1:...`。
	EnvelopePrefix = "enc:v1:"
	// KeyEnvVar 是主密钥的环境变量名。
	KeyEnvVar = "APIGX_SECRET_KEY"
	// KeyBytes 是 AES-256 要求的密钥长度。
	KeyBytes = 32
)

var (
	// ErrKeyNotConfigured 表示主密钥未配置——写入凭据必须失败，不能退化为明文。
	ErrKeyNotConfigured = errors.New("secretbox: master key not configured (set " + KeyEnvVar + ")")
	// ErrInvalidEnvelope 表示信封格式非法或版本不受支持。
	ErrInvalidEnvelope = errors.New("secretbox: invalid envelope")
	// ErrDecrypt 表示解密失败（密钥不对或密文被篡改）。
	ErrDecrypt = errors.New("secretbox: decryption failed")
)

var (
	keyOnce sync.Once
	keyVal  []byte
	keyErr  error
)

// loadKey 从环境变量读取主密钥（进程内只读一次）。
func loadKey() ([]byte, error) {
	keyOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv(KeyEnvVar))
		if raw == "" {
			keyErr = ErrKeyNotConfigured
			return
		}
		decoded, err := hex.DecodeString(raw)
		if err != nil {
			keyErr = fmt.Errorf("secretbox: %s must be hex-encoded: %w", KeyEnvVar, err)
			return
		}
		if len(decoded) != KeyBytes {
			keyErr = fmt.Errorf("secretbox: %s must decode to %d bytes (got %d)", KeyEnvVar, KeyBytes, len(decoded))
			return
		}
		keyVal = decoded
	})
	return keyVal, keyErr
}

// ResetKeyCacheForTest 清空密钥缓存，供测试切换主密钥使用。**不得在请求路径上调用。**
func ResetKeyCacheForTest() {
	keyOnce = sync.Once{}
	keyVal = nil
	keyErr = nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Encrypt 把明文封装成信封字符串。
//
// 返回值形如 `enc:v1:<nonce-b64>:<ciphertext-b64>`，可直接存进 `secret_ref`。
// 主密钥缺失时返回 ErrKeyNotConfigured（**失败关闭**）。
func Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", fmt.Errorf("%w: refusing to encrypt empty secret", ErrInvalidEnvelope)
	}
	key, err := loadKey()
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return EnvelopePrefix +
		base64.RawURLEncoding.EncodeToString(nonce) + ":" +
		base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Decrypt 解出信封里的明文。密钥不对或密文被改动都会返回 ErrDecrypt。
func Decrypt(envelope string) (string, error) {
	if !strings.HasPrefix(envelope, EnvelopePrefix) {
		return "", ErrInvalidEnvelope
	}
	parts := strings.Split(strings.TrimPrefix(envelope, EnvelopePrefix), ":")
	if len(parts) != 2 {
		return "", ErrInvalidEnvelope
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	sealed, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	key, err := loadKey()
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(nonce) != gcm.NonceSize() {
		return "", ErrInvalidEnvelope
	}
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", ErrDecrypt
	}
	return string(plain), nil
}

// IsEnvelope 报告某个引用是否为本包生成的信封（用于区分「已加密」与「外部引用」）。
func IsEnvelope(ref string) bool { return strings.HasPrefix(ref, EnvelopePrefix) }

// IsConfigured 报告主密钥是否可用（供启动自检使用）。
func IsConfigured() bool {
	_, err := loadKey()
	return err == nil
}
