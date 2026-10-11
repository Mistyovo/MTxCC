// Package graph 实现双图语义（spec §3.2/§3.3）：探索图（任务内血缘）×资产图（跨任务真值）。
package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"mtx/internal/store"
)

// GraphStore 是双图访问层（包装 store）。
type GraphStore struct {
	s *store.Store
}

// NewGraphStore 建图 store。
func NewGraphStore(s *store.Store) *GraphStore { return &GraphStore{s: s} }

// DB 暴露底层 store（测试与领域工具预置数据用）。
func (g *GraphStore) DB() *store.Store { return g.s }

// Node 是探索图节点。
type Node struct {
	ID       int64
	Kind     string
	State    string
	Owner    string
	Priority int
	Title    string
}

// VectorKey 是死亡账本键。
type VectorKey struct {
	VectorClass string
	TargetKey   string
}

// RawAsset 是待入库资产（key 已由调用方按类型化规则拼好或由本层拼）。
type RawAsset struct {
	Type  string
	Key   string
	Attrs map[string]any
}

// FindingIn 是发现写入参数。
type FindingIn struct {
	Vulnclass  string
	Severity   string
	Confidence string
	Title      string
	Summary    string
	Evidence   json.RawMessage
}

// Overview 是 planner 态势。
type Overview struct {
	Goals struct {
		Open, Met, Abandoned int
	}
	Intents struct {
		Open, Running, Done, Exhausted, Blocked int
	}
	RecentFacts []string
	RecentDone  []string
	DeadVectors map[VectorKey]int
}

// TraceHit 是执行过程检索命中。
type TraceHit struct {
	ActivityID int64
	NodeID     int64
	Kind       string
	Excerpt    string
}

// Frontier 返回 open 意图（priority DESC, id ASC）。
func (g *GraphStore) Frontier(ctx context.Context, expID int64, limit int) ([]Node, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := g.s.Query(ctx, `SELECT id, kind, state, owner, priority, title
		FROM exploration_nodes
		WHERE exploration_id=$1 AND kind='intent' AND state='open'
		ORDER BY priority DESC, id ASC LIMIT $2`, expID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Kind, &n.State, &n.Owner, &n.Priority, &n.Title); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AddIntent 派生意图（parent 必须 fact|finding——意图必须锚在已确认知识上）。
func (g *GraphStore) AddIntent(ctx context.Context, expID int64, parentIDs []int64, title string, priority int, anchorAssets []int64) ([]int64, error) {
	for _, pid := range parentIDs {
		var kind string
		if err := g.s.QueryRow(ctx,
			`SELECT kind FROM exploration_nodes WHERE id=$1 AND exploration_id=$2`, pid, expID).Scan(&kind); err != nil {
			return nil, fmt.Errorf("parent %d 不存在: %w", pid, err)
		}
		if kind != "fact" && kind != "finding" {
			return nil, fmt.Errorf("非法 parent kind=%s（只准 fact|finding）", kind)
		}
	}
	var ids []int64
	for i := 0; i < 1; i++ {
		var id int64
		err := g.s.QueryRow(ctx, `INSERT INTO exploration_nodes
			(exploration_id, kind, state, priority, title, created_at, updated_at)
			VALUES ($1,'intent','open',$2,$3,now(),now()) RETURNING id`,
			expID, priority, title).Scan(&id)
		if err != nil {
			return nil, err
		}
		for _, pid := range parentIDs {
			if _, err := g.s.Exec(ctx,
				`INSERT INTO exploration_edges (exploration_id,src,dst,kind) VALUES ($1,$2,$3,'spawns')`,
				expID, pid, id); err != nil {
				return nil, err
			}
		}
		for _, a := range anchorAssets {
			if _, err := g.s.Exec(ctx,
				`INSERT INTO exploration_anchors (node_id,asset_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, a); err != nil {
				return nil, err
			}
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// RecordFact 记录事实节点并连 yields 边。
func (g *GraphStore) RecordFact(ctx context.Context, expID, fromIntent int64, title string, body json.RawMessage, confidence string, anchors []int64) (int64, error) {
	if confidence == "" {
		confidence = "inferred"
	}
	var id int64
	if err := g.s.QueryRow(ctx, `INSERT INTO exploration_nodes
		(exploration_id, kind, state, title, body, created_at, updated_at)
		VALUES ($1,'fact','active',$2::text,jsonb_build_object('confidence',$3::text,'body',$4::jsonb),now(),now())
		RETURNING id`,
		expID, title, confidence, string(body)).Scan(&id); err != nil {
		return 0, err
	}
	if fromIntent > 0 {
		if _, err := g.s.Exec(ctx,
			`INSERT INTO exploration_edges (exploration_id,src,dst,kind) VALUES ($1,$2,$3,'yields')`,
			expID, fromIntent, id); err != nil {
			return 0, err
		}
	}
	for _, a := range anchors {
		if _, err := g.s.Exec(ctx,
			`INSERT INTO exploration_anchors (node_id,asset_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, a); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// InsertAssets 类型化去重 upsert（attrs 追加合并、task_ids 累积）。
func (g *GraphStore) InsertAssets(ctx context.Context, companyID int64, raw []RawAsset, taskID int64) ([]int64, error) {
	var ids []int64
	for _, a := range raw {
		attrs, _ := json.Marshal(a.Attrs)
		var id int64
		// upsert：命中 (type,key) 则合并 attrs（jsonb || ）并追加 task_id。
		err := g.s.QueryRow(ctx, `
			WITH ins AS (
				INSERT INTO assets (type, key, attrs, task_ids, created_at)
				VALUES ($1,$2,$3::jsonb, ARRAY[$4]::bigint[], now())
				ON CONFLICT (type, key) DO UPDATE SET
					attrs = assets.attrs || EXCLUDED.attrs,
					task_ids = (
						SELECT array_agg(DISTINCT x) FROM unnest(assets.task_ids || EXCLUDED.task_ids) AS x
					)
				RETURNING id
			) SELECT id FROM ins`,
			a.Type, a.Key, string(attrs), taskID).Scan(&id)
		if err != nil {
			return nil, fmt.Errorf("资产 %s/%s: %w", a.Type, a.Key, err)
		}
		if companyID > 0 {
			if _, err := g.s.Exec(ctx, `INSERT INTO company_scope (company_id, kind, value)
				VALUES ($1,'domain',$2) ON CONFLICT DO NOTHING`, companyID, a.Key); err != nil {
				return nil, err
			}
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// ReportFinding 双写：finding 节点（血缘）+ findings 表（独立留存，任务删除不级联）。
// severity≤confidence 天花板由 DB CHECK 强制（declared≤low/inferred≤medium）。
func (g *GraphStore) ReportFinding(ctx context.Context, taskID, expID, fromIntent int64, f FindingIn) (int64, error) {
	if f.Evidence == nil {
		f.Evidence = json.RawMessage(`[]`)
	}
	var nodeID int64
	if err := g.s.QueryRow(ctx, `INSERT INTO exploration_nodes
		(exploration_id, kind, state, title, body, created_at, updated_at)
		VALUES ($1,'finding','active',$2::text,jsonb_build_object('vulnclass',$3::text,'severity',$4::text,'confidence',$5::text),now(),now())
		RETURNING id`,
		expID, f.Title, f.Vulnclass, f.Severity, f.Confidence).Scan(&nodeID); err != nil {
		return 0, err
	}
	var id int64
	err := g.s.QueryRow(ctx, `INSERT INTO findings
		(task_id, node_id, vulnclass, severity, title, summary, evidence, confidence, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,'candidate',now()) RETURNING id`,
		taskID, nodeID, f.Vulnclass, f.Severity, f.Title, f.Summary, string(f.Evidence), f.Confidence).Scan(&id)
	if err != nil {
		// 回滚节点保持双写一致。
		_, _ = g.s.Exec(ctx, `DELETE FROM exploration_nodes WHERE id=$1`, nodeID)
		return 0, fmt.Errorf("证据门或写库拒绝: %w", err)
	}
	if fromIntent > 0 {
		_, _ = g.s.Exec(ctx,
			`INSERT INTO exploration_edges (exploration_id,src,dst,kind) VALUES ($1,$2,$3,'yields')`,
			expID, fromIntent, nodeID)
	}
	return id, nil
}

// GraphOverview 汇总 planner 态势。
func (g *GraphStore) GraphOverview(ctx context.Context, expID int64) (Overview, error) {
	var o Overview
	err := g.s.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE kind='goal' AND state='open'),
		count(*) FILTER (WHERE kind='goal' AND state='met'),
		count(*) FILTER (WHERE kind='goal' AND state='abandoned'),
		count(*) FILTER (WHERE kind='intent' AND state='open'),
		count(*) FILTER (WHERE kind='intent' AND state='running'),
		count(*) FILTER (WHERE kind='intent' AND state='done'),
		count(*) FILTER (WHERE kind='intent' AND state='exhausted'),
		count(*) FILTER (WHERE kind='intent' AND state='blocked')
		FROM exploration_nodes WHERE exploration_id=$1`, expID).Scan(
		&o.Goals.Open, &o.Goals.Met, &o.Goals.Abandoned,
		&o.Intents.Open, &o.Intents.Running, &o.Intents.Done, &o.Intents.Exhausted, &o.Intents.Blocked)
	if err != nil {
		return o, err
	}
	rows, err := g.s.Query(ctx, `SELECT title FROM exploration_nodes
		WHERE exploration_id=$1 AND kind='fact' AND state='active'
		ORDER BY id DESC LIMIT 10`, expID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var t string
			_ = rows.Scan(&t)
			o.RecentFacts = append(o.RecentFacts, t)
		}
	}
	o.DeadVectors = g.DeadVectors(ctx, expID)
	return o, nil
}

// SearchTraces 全文检索执行过程（排除 excludeIntent，供 worker 互查）。
func (g *GraphStore) SearchTraces(ctx context.Context, taskID int64, q string, excludeIntent int64, limit int) ([]TraceHit, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := g.s.Query(ctx, `SELECT id, coalesce(node_id,0), kind,
			substring(payload::text from 1 for 300)
		FROM activity
		WHERE task_id=$1 AND ($2=0 OR coalesce(node_id,0) <> $2)
		  AND payload::text ILIKE '%' || $3 || '%'
		ORDER BY id DESC LIMIT $4`, taskID, excludeIntent, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TraceHit
	for rows.Next() {
		var h TraceHit
		if err := rows.Scan(&h.ActivityID, &h.NodeID, &h.Kind, &h.Excerpt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// MarkTried 登记一次尝试结果（死亡账本）。
func (g *GraphStore) MarkTried(ctx context.Context, expID int64, vectorClass, targetKey, outcome string) error {
	_, err := g.s.Exec(ctx, `INSERT INTO tried_ledger (exploration_id, vector_class, target_key, outcome, created_at)
		VALUES ($1,$2,$3,$4,now())`, expID, vectorClass, targetKey, outcome)
	return err
}

// DeadVectors 返回失败 ≥2 的向量（class@target → 次数）。
func (g *GraphStore) DeadVectors(ctx context.Context, expID int64) map[VectorKey]int {
	out := map[VectorKey]int{}
	rows, err := g.s.Query(ctx, `SELECT vector_class, target_key, count(*) AS fails
		FROM tried_ledger WHERE exploration_id=$1 AND outcome <> 'ok'
		GROUP BY vector_class, target_key HAVING count(*) >= 2`, expID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k VectorKey
		var n int
		if err := rows.Scan(&k.VectorClass, &k.TargetKey, &n); err == nil {
			out[k] = n
		}
	}
	return out
}

var _ = time.Now
