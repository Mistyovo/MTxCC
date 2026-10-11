package traffic

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProxyServer 是记录型 MITM 代理（spec §6.5）：
//   - 明文 HTTP（绝对 URI）直接转发并记录；
//   - CONNECT：本方 CA 给 host 签临时证书做 TLS 拦截，解密记录后转发上游。
type ProxyServer struct {
	Addr string // 如 :8788
	Idx  *Index
	caDir string

	ca        tls.Certificate
	caKey     *rsa.PrivateKey
	caTLSCert *x509.Certificate
	leafMu    sync.Mutex
	leafs     map[string]*tls.Certificate

	srv *http.Server
	lnAddr string
}

// NewProxyServer 建代理（首次运行在 caDir 生成自签 CA）。
func NewProxyServer(addr, caDir string, idx *Index) (*ProxyServer, error) {
	p := &ProxyServer{Addr: addr, Idx: idx, caDir: caDir, leafs: map[string]*tls.Certificate{}}
	if err := p.loadOrCreateCA(); err != nil {
		return nil, err
	}
	return p, nil
}

// Start 启动监听。
func (p *ProxyServer) Start() error {
	p.srv = &http.Server{Addr: p.Addr, Handler: p}
	ln, err := net.Listen("tcp", p.Addr)
	if err != nil {
		return err
	}
	p.lnAddr = ln.Addr().String()
	go func() { _ = p.srv.Serve(ln) }()
	return nil
}

// ListenerAddr 返回实际监听地址。
func (p *ProxyServer) ListenerAddr() string { return p.lnAddr }

// Stop 关闭。
func (p *ProxyServer) Stop() error {
	if p.srv == nil {
		return nil
	}
	return p.srv.Close()
}

// ServeHTTP 处理明文代理请求；CONNECT 由 http.Server 的 TLS 握手钩子外处理
//（http.Server 不自动处理 CONNECT，此处 Hijack）。
func (p *ProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	// 明文代理：绝对 URI 请求。
	req, err := http.NewRequestWithContext(r.Context(), r.Method, r.RequestURI, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header = r.Header.Clone()
	resp, err := p.transport().RoundTrip(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	p.record(r.Method, req.Host, 80, "http", req.URL.Path, resp.StatusCode, r.Header, "", resp.Header, string(body))
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

// handleConnect MITM：与客户端用本方 CA 证书握手，与上游用真实 TLS。
func (p *ProxyServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	host := r.Host
	clientConn, bufRW, err := hj.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	// 先回 200 再开始 TLS 握手（客户端等待 CONNECT 成功响应）。
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	_ = bufRW

	leaf, err := p.leafFor(hostOnly(host))
	if err != nil {
		return
	}
	tlsConn := tls.Server(clientConn, &tls.Config{Certificates: []tls.Certificate{*leaf}})
	if err := tlsConn.Handshake(); err != nil {
		dbgPrintf("MITM handshake err: %v", err)
		return
	}
	defer tlsConn.Close()

	// 内层 HTTP/1.1 手工循环（不依赖 http.Server 的连接管理，行为可控）。
	br := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(br)
		if err != nil {
			if err != io.EOF {
				dbgPrintf("MITM read request: %v", err)
			}
			return
		}
		if !p.serveInner(tlsConn, req, host) {
			return
		}
	}

}

// serveInner 转发单条 HTTPS 请求并写回响应；返回 false 时连接终止。
func (p *ProxyServer) serveInner(tlsConn *tls.Conn, req *http.Request, connectHost string) bool {
	req.RequestURI = ""
	req.URL.Scheme = "https"
	if req.URL.Host == "" {
		req.URL.Host = req.Host
	}
	if req.URL.Host == "" {
		req.URL.Host = connectHost
	}
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(io.LimitReader(req.Body, 256*1024))
		req.Body = io.NopCloser(strings.NewReader(string(reqBody)))
	}
	resp, err := p.transportTLS().RoundTrip(req)
	if err != nil {
		dbgPrintf("MITM upstream: %v", err)
		_, _ = tlsConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n"))
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	p.record(req.Method, req.Host, 443, "https", req.URL.Path, resp.StatusCode,
		req.Header, string(reqBody), resp.Header, string(body))

	var hb strings.Builder
	for k, vv := range resp.Header {
		for _, v := range vv {
			hb.WriteString(k + ": " + v + "\r\n")
		}
	}
	head := fmt.Sprintf("HTTP/1.1 %d %s\r\n%sContent-Length: %d\r\nConnection: keep-alive\r\n\r\n",
		resp.StatusCode, http.StatusText(resp.StatusCode), hb.String(), len(body))
	_, _ = tlsConn.Write([]byte(head))
	_, _ = tlsConn.Write(body)
	return true
}

func (p *ProxyServer) record(method, host string, port int, scheme, path string, status int,
	reqH http.Header, reqBody string, respH http.Header, respBody string) {
	_ = p.Idx.Record(Exchange{
		Method: method, Host: hostOnly(host), Port: port, Scheme: scheme, Path: path,
		Status: status, ReqHeaders: headersString(reqH), ReqBody: reqBody,
		RespHeaders: headersString(respH), RespBody: respBody,
	})
}

func (p *ProxyServer) transport() *http.Transport {
	return &http.Transport{Proxy: nil, DisableKeepAlives: true}
}

// transportTLS 出站：M0 记录代理对上游证书不做严格校验（目标真实性由上层工具/CA 策略负责）。
func (p *ProxyServer) transportTLS() *http.Transport {
	return &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
	}
}

// loadOrCreateCA 首次生成 CA（mitm.key / mitm.pem），之后复用。
func (p *ProxyServer) loadOrCreateCA() error {
	certPath := filepath.Join(p.caDir, "mitm.pem")
	keyPath := filepath.Join(p.caDir, "mitm.key")
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		p.ca = cert
		p.caKey = cert.PrivateKey.(*rsa.PrivateKey)
		p.caTLSCert, _ = x509.ParseCertificate(cert.Certificate[0])
		return nil
	}
	if err := os.MkdirAll(p.caDir, 0o700); err != nil {
		return err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	tpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "MTxCC MITM CA", Organization: []string{"MTxCC"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER := x509.MarshalPKCS1PrivateKey(key)
	if err := os.WriteFile(certPath, toPEM("CERTIFICATE", der), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, toPEM("RSA PRIVATE KEY", keyDER), 0o600); err != nil {
		return err
	}
	p.ca = tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	p.caKey = key
	p.caTLSCert, _ = x509.ParseCertificate(der)
	return nil
}

// leafFor 给 host 签/取缓存叶子证书。
func (p *ProxyServer) leafFor(host string) (*tls.Certificate, error) {
	p.leafMu.Lock()
	defer p.leafMu.Unlock()
	if c, ok := p.leafs[host]; ok {
		return c, nil
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tpl.IPAddresses = []net.IP{ip}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, p.caTLSCert, &key.PublicKey, p.caKey)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	p.leafs[host] = c
	return c, nil
}

var dbgPrintf = func(string, ...any) {}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.TrimSuffix(hostport, ".")
}

func headersString(h http.Header) string {
	var sb strings.Builder
	for k, vv := range h {
		for _, v := range vv {
			sb.WriteString(k + ": " + v + "\n")
		}
	}
	return sb.String()
}

// randomSerial 生成正随机序列号。
func randomSerial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

// toPEM 编码 PEM 块。
func toPEM(blockType string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
}
