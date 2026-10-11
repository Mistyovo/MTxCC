package traffic

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func startProxy(t *testing.T) (*ProxyServer, *Index) {
	t.Helper()
	dir := t.TempDir()
	idx, err := OpenIndex(dir + "/traffic.db")
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	p, err := NewProxyServer("127.0.0.1:0", dir+"/ca", idx)
	if err != nil {
		t.Fatalf("NewProxyServer: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	return p, idx
}

// TestProxyRecordsPlainHTTP 明文代理请求被记录且可检索。
func TestProxyRecordsPlainHTTP(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("PLAIN_TARGET_MARKER_中文回显"))
	}))
	defer up.Close()
	upHost := hostOnly(up.Listener.Addr().String())

	p, idx := startProxy(t)
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: func(*http.Request) (*url.URL, error) {
				return url.Parse("http://" + p.ListenerAddr())
			},
		},
	}
	resp, err := client.Get(up.URL + "/probe")
	if err != nil {
		t.Fatalf("经代理请求: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		refs, _ := idx.Search(context.Background(), upHost, "中文回显", 10)
		if len(refs) == 1 && refs[0].Status == 200 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("明文交换未被索引")
}

// TestProxyMITMRecordsHTTPS CONNECT 经 MITM 解密记录。
func TestProxyMITMRecordsHTTPS(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("TLS_TARGET_MARKER_秘密内容"))
	}))
	defer up.Close()
	upHost := hostOnly(up.Listener.Addr().String())

	p, idx := startProxy(t)

	// 客户端信任代理 CA。
	caPool := x509.NewCertPool()
	caPool.AddCert(p.caTLSCert)
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(mustURL("http://" + p.ListenerAddr())),
			TLSClientConfig: &tls.Config{RootCAs: caPool},
		},
	}
	resp, err := client.Get(up.URL + "/secret")
	if err != nil {
		t.Fatalf("MITM 请求: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		refs, _ := idx.Search(context.Background(), upHost, "秘密内容", 10)
		if len(refs) >= 1 && refs[0].Scheme == "https" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("HTTPS 交换未被解密索引")
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
