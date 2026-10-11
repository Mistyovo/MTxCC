package store

import "context"

// ClaimIntent 以 CAS 语义领取一条 open 意图：
// 单条 UPDATE ... WHERE state='open'，影响行数=1 才算领到（天然防多 worker 双领）。
func (s *Store) ClaimIntent(ctx context.Context, explorationID, intentID int64, owner string) (bool, error) {
	tag, err := s.Exec(ctx, `UPDATE exploration_nodes
		SET state = 'running', owner = $1, updated_at = now()
		WHERE id = $2 AND exploration_id = $3 AND kind = 'intent' AND state = 'open'`,
		owner, intentID, explorationID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
