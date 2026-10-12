-- findings 去重键（spec M3 质量闭环）
ALTER TABLE findings ADD COLUMN IF NOT EXISTS asset_key text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_findings_dedup ON findings (task_id, vulnclass, asset_key);
