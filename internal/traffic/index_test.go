package traffic

import (
	"context"
	"strings"
	"testing"
)

func newTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := OpenIndex(t.TempDir() + "/traffic.db")
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

// TestIndexRecordSearch 往返：记录交换后按 host+中文子串检索命中（FTS5 trigram）。
// 令其失败的生产改动：FTS 未建/分词器错、字段写串。
func TestIndexRecordSearch(t *testing.T) {
	idx := newTestIndex(t)
	err := idx.Record(Exchange{
		Method: "GET", Host: "app.example.com", Port: 80, Scheme: "http",
		Path: "/search?q=1", Status: 200,
		ReqBody:  "",
		RespBody: `<html>注入点：用户搜索"管理后台"</html>`,
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	refs, err := idx.Search(context.Background(), "app.example.com", "管理后台", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(refs) != 1 || refs[0].Host != "app.example.com" || refs[0].Status != 200 {
		t.Fatalf("refs = %+v", refs)
	}

	// 其他 host 不命中。
	if refs, _ := idx.Search(context.Background(), "other.example.com", "管理后台", 10); len(refs) != 0 {
		t.Fatalf("跨 host 泄漏: %+v", refs)
	}
}

// TestIndexBlobOverflow 大正文（>256KB）溢出到 blob 文件，预览 8KB 入库。
func TestIndexBlobOverflow(t *testing.T) {
	idx := newTestIndex(t)
	big := strings.Repeat("PAYLOAD_MARKER_", 20_000) // ~320KB
	if err := idx.Record(Exchange{
		Method: "POST", Host: "big.example.com", Port: 443, Scheme: "https",
		Path: "/upload", Status: 200, RespBody: big,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	refs, err := idx.Search(context.Background(), "big.example.com", "PAYLOAD_MARKER_", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("大正文应仍可检索（预览内含特征）: %d", len(refs))
	}
	if refs[0].BlobRef == "" {
		t.Fatal("大正文应有 blob 引用")
	}
	if blob, err := idx.ReadBlob(refs[0].BlobRef); err != nil || len(blob) != len(big) {
		t.Fatalf("blob 取回 = (%d 字节, %v), want %d", len(blob), err, len(big))
	}
}

// TestTrafficToolsHostRequired traffic_search 缺 host 参数 → 校验错误。
func TestTrafficToolsHostRequired(t *testing.T) {
	idx := newTestIndex(t)
	tools := NewTrafficTools(idx)
	var searchTool = tools[0]
	if searchTool.Name() != "traffic_search" {
		t.Fatalf("首个工具应为 traffic_search: %v", searchTool.Name())
	}
	if _, err := searchTool.Call(context.Background(), []byte(`{"body_contains":"x"}`)); err == nil {
		t.Fatal("缺 host 应报校验错误")
	}
}
