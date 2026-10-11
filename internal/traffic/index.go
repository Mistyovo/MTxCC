// Package traffic 实现全流量留痕：记录代理 + SQLite FTS 索引（spec §6.5）。
package traffic

import (
	"context"
	"encoding/json"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Exchange 是一次 HTTP 交换的记录。
type Exchange struct {
	Method      string
	Host        string
	Port        int
	Scheme      string
	Path        string
	Status      int
	ReqHeaders  string
	ReqBody     string
	RespHeaders string
	RespBody    string
}

// Ref 是检索结果引用。
type Ref struct {
	ID      int64
	Method  string
	Host    string
	Scheme  string
	Path    string
	Status  int
	BlobRef string
}

// Index 是流量索引（modernc sqlite，FTS5 trigram 支持子串与中文检索）。
type Index struct {
	db     *sql.DB
	blobDir string
}

const schema = `
CREATE TABLE IF NOT EXISTS exchanges (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at   TEXT NOT NULL,
	method       TEXT NOT NULL,
	host         TEXT NOT NULL,
	port         INTEGER NOT NULL DEFAULT 0,
	scheme       TEXT NOT NULL DEFAULT 'http',
	path         TEXT NOT NULL DEFAULT '',
	status       INTEGER NOT NULL DEFAULT 0,
	req_headers  TEXT NOT NULL DEFAULT '',
	req_body     TEXT NOT NULL DEFAULT '',
	resp_headers TEXT NOT NULL DEFAULT '',
	resp_body    TEXT NOT NULL DEFAULT '',
	blob_ref     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_exchanges_host ON exchanges (host, id);
CREATE VIRTUAL TABLE IF NOT EXISTS exchanges_fts USING fts5(
	req_body, resp_body, tokenize = 'trigram'
);
`

// OpenIndex 打开（或创建）索引库；blob 文件存同目录 blobs/。
func OpenIndex(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc sqlite 单写者
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("建表: %w", err)
	}
	blobDir := filepath.Join(filepath.Dir(path), "blobs")
	if err := os.MkdirAll(blobDir, 0o750); err != nil {
		return nil, err
	}
	return &Index{db: db, blobDir: blobDir}, nil
}

// Close 关闭索引。
func (i *Index) Close() error { return i.db.Close() }

// Record 落一条交换；>256KB 的请求/响应体溢出为 blob 文件（预览 8KB 入库）。
func (i *Index) Record(e Exchange) error {
	blobRef := ""
	if len(e.RespBody) > 256*1024 {
		ref, err := i.writeBlob([]byte(e.RespBody))
		if err != nil {
			return err
		}
		blobRef = ref
		e.RespBody = e.RespBody[:8*1024] + fmt.Sprintf("\n…[blob %s，全文 %d 字节]", ref, len(e.RespBody))
	}
	if len(e.ReqBody) > 256*1024 {
		ref, err := i.writeBlob([]byte(e.ReqBody))
		if err != nil {
			return err
		}
		if blobRef == "" {
			blobRef = ref
		}
		e.ReqBody = e.ReqBody[:8*1024] + fmt.Sprintf("\n…[blob %s]", ref)
	}

	res, err := i.db.Exec(`INSERT INTO exchanges
		(created_at, method, host, port, scheme, path, status, req_headers, req_body, resp_headers, resp_body, blob_ref)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		time.Now().UTC().Format(time.RFC3339Nano), e.Method, e.Host, e.Port, e.Scheme, e.Path,
		e.Status, e.ReqHeaders, e.ReqBody, e.RespHeaders, e.RespBody, blobRef)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	_, err = i.db.Exec(`INSERT INTO exchanges_fts (rowid, req_body, resp_body) VALUES (?,?,?)`,
		id, e.ReqBody, e.RespBody)
	return err
}

// Search 按 host（必填）+ 正文子串检索。
func (i *Index) Search(ctx context.Context, host, bodyContains string, limit int) ([]Ref, error) {
	if limit <= 0 {
		limit = 20
	}
	if host == "" {
		return nil, fmt.Errorf("host 参数必填（防止全库扫描）")
	}
	rows, err := i.db.QueryContext(ctx, `SELECT e.id, e.method, e.host, e.scheme, e.path, e.status, e.blob_ref
		FROM exchanges e JOIN exchanges_fts f ON f.rowid = e.id
		WHERE e.host = ? AND exchanges_fts MATCH ?
		ORDER BY e.id DESC LIMIT ?`,
		host, ftsQuote(bodyContains), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ref
	for rows.Next() {
		var r Ref
		if err := rows.Scan(&r.ID, &r.Method, &r.Host, &r.Scheme, &r.Path, &r.Status, &r.BlobRef); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReadBlob 取回溢出正文。
func (i *Index) ReadBlob(ref string) ([]byte, error) {
	if ref == "" {
		return nil, fmt.Errorf("空 blob 引用")
	}
	return os.ReadFile(filepath.Join(i.blobDir, ref))
}

// writeBlob 内容寻址落盘（sha256 文件名）。
func (i *Index) writeBlob(b []byte) (string, error) {
	sum := sha256.Sum256(b)
	ref := hex.EncodeToString(sum[:16])
	if err := os.WriteFile(filepath.Join(i.blobDir, ref), b, 0o640); err != nil {
		return "", err
	}
	return ref, nil
}

// ftsQuote 把用户子串安全转为 FTS 查询（trigram 需 ≥3 字符）。
func ftsQuote(s string) string {
	if len(s) < 3 {
		return `""` // 空匹配
	}
	return `"` + s + `"`
}

// Get 按 id 取回完整交换（JSON 序列化）。
func (i *Index) Get(id int64) (string, error) {
	var e struct {
		ID          int64
		CreatedAt   string
		Method      string
		Host        string
		Port        int
		Scheme      string
		Path        string
		Status      int
		ReqHeaders  string
		ReqBody     string
		RespHeaders string
		RespBody    string
		BlobRef     string
	}
	err := i.db.QueryRow(`SELECT id, created_at, method, host, port, scheme, path, status,
		req_headers, req_body, resp_headers, resp_body, blob_ref FROM exchanges WHERE id=?`, id).
		Scan(&e.ID, &e.CreatedAt, &e.Method, &e.Host, &e.Port, &e.Scheme, &e.Path, &e.Status,
			&e.ReqHeaders, &e.ReqBody, &e.RespHeaders, &e.RespBody, &e.BlobRef)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(e)
	return string(out), nil
}
