-- 任务模板（spec §3.2 任务层；M2 产品期补全）
CREATE TABLE IF NOT EXISTS task_templates (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);
