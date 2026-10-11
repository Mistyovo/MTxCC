package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// setupDone 判断是否已完成首启（存在 admin 用户）。
func (s *Server) setupDone() bool {
	var n int
	_ = s.store.QueryRow(sqlCtx(), `SELECT count(*) FROM users WHERE username='admin'`).Scan(&n)
	return n > 0
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]bool{"initialized": s.setupDone()})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.setupDone() {
		errJSON(w, http.StatusConflict, "已完成初始化")
		return
	}
	var in struct{ Password string `json:"password"` }
	if err := readJSON(r, &in); err != nil || len(in.Password) < 8 {
		errJSON(w, http.StatusBadRequest, "密码至少 8 位")
		return
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if _, err := s.store.Exec(sqlCtx(),
		`INSERT INTO users (username, pass_hash, role) VALUES ('admin', $1, 'admin')`, string(hash)); err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		errJSON(w, http.StatusBadRequest, "bad request")
		return
	}
	var hash string
	if err := s.store.QueryRow(sqlCtx(),
		`SELECT pass_hash FROM users WHERE username=$1 AND NOT disabled`, in.Username).Scan(&hash); err != nil {
		errJSON(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		errJSON(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	tok, err := s.issueToken(in.Username)
	if err != nil {
		errJSON(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"token": tok})
}

// jwtSecret 由主密钥派生。
func (s *Server) jwtSecret() []byte {
	sum := sha256.Sum256([]byte("mtx-jwt:" + s.opts.MasterKey))
	return sum[:]
}

// issueToken 签发 HMAC token（user.exp.sig）。
func (s *Server) issueToken(user string) (string, error) {
	exp := time.Now().Add(24 * time.Hour).Unix()
	payload := fmt.Sprintf("%s.%d", user, exp)
	mac := hmac.New(sha256.New, s.jwtSecret())
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return payload + "." + sig, nil
}

// verifyToken 校验 token。
func (s *Server) verifyToken(tok string) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[0] == "" {
		return false
	}
	payload := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, s.jwtSecret())
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return false
	}
	var exp int64
	if _, err := fmt.Sscanf(parts[1], "%d", &exp); err != nil || time.Now().Unix() > exp {
		return false
	}
	return true
}
