// Package vault 实现凭据保管（spec §7.5）：AES-GCM 存储；LLM 上下文只见
// ${CRED:<label>} 占位符——发送瞬间本地还原，输出回灌前重新掩码（双向网关）。
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// Vault 是内存态保管库（M1：进程内；M2 持久化到 credential_vault 表）。
type Vault struct {
	gcm cipher.AEAD
	mu  sync.RWMutex
	// taskID → label → 密文。
	store map[int64]map[string][]byte
}

// New 以主密钥（base64 32B）建库。
func New(masterKeyB64 string) (*Vault, error) {
	raw, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("主密钥须为 32 字节 base64")
	}
	blk, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	return &Vault{gcm: gcm, store: map[int64]map[string][]byte{}}, nil
}

// Put 加密存入。
func (v *Vault) Put(taskID int64, kind, label, secret string) error {
	nonce := make([]byte, v.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return err
	}
	enc := v.gcm.Seal(nonce, nonce, []byte(secret), nil)
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.store[taskID] == nil {
		v.store[taskID] = map[string][]byte{}
	}
	v.store[taskID][label] = enc
	return nil
}

// Get 解密读取。
func (v *Vault) Get(taskID int64, label string) (string, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	enc, ok := v.store[taskID][label]
	if !ok {
		return "", fmt.Errorf("凭据 %q 不存在", label)
	}
	ns := v.gcm.NonceSize()
	plain, err := v.gcm.Open(nil, enc[:ns], enc[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Labels 列出任务凭据标签（不回值）。
func (v *Vault) Labels(taskID int64) []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	var out []string
	for l := range v.store[taskID] {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// DumpEncrypted 导出密文（hex，供审计/持久化）——绝不含明文。
func (v *Vault) DumpEncrypted(taskID int64, label string) (string, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	enc, ok := v.store[taskID][label]
	if !ok {
		return "", fmt.Errorf("不存在")
	}
	return base64.StdEncoding.EncodeToString(enc), nil
}

// Placeholder 生成占位符。
func Placeholder(label string) string { return "${CRED:" + label + "}"}

// Expand 把命令中的占位符还原为真值（发送瞬间；仅此一处产生真值形态）。
func (v *Vault) Expand(taskID int64, s string) string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.expandLocked(taskID, s)
}

// Mask 把文本中出现的已知真值替换回收据占位符（回灌模型前）。
func (v *Vault) Mask(taskID int64, s string) string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.maskLocked(taskID, s)
}

// --- 内部（调用方持读锁） ---

func (v *Vault) decrypt(enc []byte) string {
	ns := v.gcm.NonceSize()
	plain, err := v.gcm.Open(nil, enc[:ns], enc[ns:], nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

func (v *Vault) expandLocked(taskID int64, s string) string {
	m := v.store[taskID]
	var out strings.Builder
	for {
		i := strings.Index(s, "${CRED:")
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		rest := s[i+len("${CRED:"):]
		j := strings.Index(rest, "}")
		if j < 0 {
			out.WriteString(s[i:])
			return out.String()
		}
		label := rest[:j]
		s = rest[j+1:]
		if enc, ok := m[label]; ok {
			if plain := v.decrypt(enc); plain != "" {
				out.WriteString(plain)
			}
		}
	}
}

func (v *Vault) maskLocked(taskID int64, s string) string {
	type pair struct{ plain, label string }
	var pairs []pair
	for label, enc := range v.store[taskID] {
		if plain := v.decrypt(enc); plain != "" {
			pairs = append(pairs, pair{plain, Placeholder(label)})
		}
	}
	// 长值优先替换，防短前缀遮蔽。
	sort.Slice(pairs, func(i, j int) bool { return len(pairs[i].plain) > len(pairs[j].plain) })
	for _, p := range pairs {
		s = strings.ReplaceAll(s, p.plain, p.label)
	}
	return s
}
