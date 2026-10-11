-- 001_core.sql — MTxCC 核心schema（spec §3.1-§3.6），全部幂等。

-- ===== 身份与审计（§3.1） =====
CREATE TABLE IF NOT EXISTS users (
    id          bigserial PRIMARY KEY,
    username    text NOT NULL UNIQUE,
    pass_hash   text NOT NULL,
    role        text NOT NULL DEFAULT 'admin' CHECK (role IN ('admin','auditor','operator','viewer')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    disabled    boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS audit_log (
    id          bigserial PRIMARY KEY,
    user_id     bigint REFERENCES users(id),
    action      text NOT NULL,
    target_type text NOT NULL,
    target_id   text NOT NULL DEFAULT '',
    detail      jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_user_time ON audit_log (user_id, created_at DESC);

-- ===== 任务与探索（§3.2） =====
CREATE TABLE IF NOT EXISTS tasks (
    id                 bigserial PRIMARY KEY,
    title              text NOT NULL DEFAULT '',
    target_text        text NOT NULL,
    status             text NOT NULL DEFAULT 'created'
                       CHECK (status IN ('created','queued','running','paused','done','failed','timeout')),
    timeout_seconds    integer NOT NULL DEFAULT 7200,
    category           text NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL DEFAULT now(),
    started_at         timestamptz,
    finished_at        timestamptz,
    llm_profile_chain  jsonb NOT NULL DEFAULT '[]',
    sandbox_id         bigint,
    roe_id             bigint
);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks (status, created_at);

CREATE TABLE IF NOT EXISTS task_constraints (
    task_id  bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind     text NOT NULL CHECK (kind IN ('allow','deny')),
    text     text NOT NULL,
    PRIMARY KEY (task_id, kind, text)
);

CREATE TABLE IF NOT EXISTS task_scope (
    task_id  bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    asset_id bigint NOT NULL,
    mode     text NOT NULL DEFAULT 'include',
    PRIMARY KEY (task_id, asset_id)
);

CREATE TABLE IF NOT EXISTS explorations (
    id         bigserial PRIMARY KEY,
    task_id    bigint NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS exploration_nodes (
    id               bigserial PRIMARY KEY,
    exploration_id   bigint NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    kind             text NOT NULL
                     CHECK (kind IN ('origin','goal','intent','fact','finding','digest','hint')),
    state            text NOT NULL,
    owner            text NOT NULL DEFAULT '',
    priority         integer NOT NULL DEFAULT 5,
    title            text NOT NULL DEFAULT '',
    body             jsonb NOT NULL DEFAULT '{}',
    cold_since_round integer,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    -- kind 状态机（spec §3.2）：intent 七态 / goal 三态 / 其余 active|digested
    CONSTRAINT node_state_machine CHECK (
        (kind = 'intent' AND state IN ('open','running','done','exhausted','blocked','stopped','paused')) OR
        (kind = 'goal' AND state IN ('open','met','abandoned')) OR
        (kind IN ('origin','fact','finding','digest','hint') AND state IN ('active','digested'))
    )
);
CREATE INDEX IF NOT EXISTS idx_nodes_exp_kind_state
    ON exploration_nodes (exploration_id, kind, state);
CREATE INDEX IF NOT EXISTS idx_nodes_frontier
    ON exploration_nodes (exploration_id, priority DESC, id) WHERE kind = 'intent' AND state = 'open';

CREATE TABLE IF NOT EXISTS exploration_edges (
    id             bigserial PRIMARY KEY,
    exploration_id bigint NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    src            bigint NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    dst            bigint NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN ('spawns','derived_from','yields','proves','covers')),
    CONSTRAINT edge_no_self CHECK (src <> dst)
);
CREATE INDEX IF NOT EXISTS idx_edges_src ON exploration_edges (src);
CREATE INDEX IF NOT EXISTS idx_edges_dst ON exploration_edges (dst);

CREATE TABLE IF NOT EXISTS exploration_anchors (
    node_id  bigint NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    asset_id bigint NOT NULL,
    PRIMARY KEY (node_id, asset_id)
);

CREATE TABLE IF NOT EXISTS activity (
    id          bigserial PRIMARY KEY,
    task_id     bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    node_id     bigint,
    agent_role  text NOT NULL DEFAULT '',
    kind        text NOT NULL CHECK (kind IN ('tool_use','tool_result','text','thinking','usage','result')),
    payload     jsonb NOT NULL DEFAULT '{}',
    tok_in      bigint NOT NULL DEFAULT 0,
    tok_out     bigint NOT NULL DEFAULT 0,
    tok_cache   bigint NOT NULL DEFAULT 0,
    cost_usd    numeric NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_activity_task_id ON activity (task_id, id);
CREATE INDEX IF NOT EXISTS idx_activity_payload ON activity USING gin (payload);

-- 死亡账本（spec §5.4）：同 (vector_class,target_key) 累计失败 ≥2 即死向量
CREATE TABLE IF NOT EXISTS tried_ledger (
    id             bigserial PRIMARY KEY,
    exploration_id bigint NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    vector_class   text NOT NULL,
    target_key     text NOT NULL,
    outcome        text NOT NULL,
    detail         jsonb NOT NULL DEFAULT '{}',
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tried_key ON tried_ledger (exploration_id, vector_class, target_key);

-- ===== 资产与发现（§3.3） =====
CREATE TABLE IF NOT EXISTS companies (
    id   bigserial PRIMARY KEY,
    name text NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS company_scope (
    id         bigserial PRIMARY KEY,
    company_id bigint NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('domain','ip','cidr','icp','keyword')),
    value      text NOT NULL,
    UNIQUE (company_id, kind, value)
);

-- assets：类型化去重键统一收敛为 key 列（domain=name、service=host:port:scheme、endpoint=url+method）
CREATE TABLE IF NOT EXISTS assets (
    id         bigserial PRIMARY KEY,
    type       text NOT NULL CHECK (type IN ('root_domain','domain','ip','service','endpoint','os','role')),
    key        text NOT NULL,
    attrs      jsonb NOT NULL DEFAULT '{}',
    task_ids   bigint[] NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (type, key)
);
CREATE INDEX IF NOT EXISTS idx_assets_task_ids ON assets USING gin (task_ids);

CREATE TABLE IF NOT EXISTS findings (
    id          bigserial PRIMARY KEY,
    task_id     bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    node_id     bigint,
    vulnclass   text NOT NULL DEFAULT '',
    severity    text NOT NULL CHECK (severity IN ('info','low','medium','high','critical')),
    title       text NOT NULL,
    summary     text NOT NULL DEFAULT '',
    evidence    jsonb NOT NULL DEFAULT '[]',
    asset_ids   bigint[] NOT NULL DEFAULT '{}',
    status      text NOT NULL DEFAULT 'candidate'
                CHECK (status IN ('candidate','verified','false_positive','fixed')),
    confidence  text NOT NULL DEFAULT 'declared'
                CHECK (confidence IN ('declared','inferred','runtime_observed')),
    verify_state text NOT NULL DEFAULT 'unverified',
    report_md   text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    -- 证据门（spec §8.3）：severity 不得高于 confidence 天花板
    CONSTRAINT severity_confidence_gate CHECK (
        CASE confidence
            WHEN 'declared' THEN severity IN ('info','low')
            WHEN 'inferred' THEN severity IN ('info','low','medium')
            ELSE TRUE
        END
    )
);
CREATE INDEX IF NOT EXISTS idx_findings_task ON findings (task_id, severity);

CREATE TABLE IF NOT EXISTS finding_retests (
    id          bigserial PRIMARY KEY,
    finding_id  bigint NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    conclusion  text NOT NULL CHECK (conclusion IN ('reproduced','fixed','inconclusive')),
    detail      text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- ===== LLM 与 Agent 配置（§3.4） =====
CREATE TABLE IF NOT EXISTS llm_profiles (
    id            bigserial PRIMARY KEY,
    name          text NOT NULL UNIQUE,
    protocol      text NOT NULL CHECK (protocol IN ('anthropic','openai','openai_responses')),
    base_url      text NOT NULL DEFAULT '',
    api_key_enc   bytea,               -- AES-GCM 密文，主密钥来自 MTX_MASTER_KEY
    model         text NOT NULL,
    ctx_window_k  integer NOT NULL DEFAULT 200,
    max_tokens    integer NOT NULL DEFAULT 0,
    rate_per_s    integer NOT NULL DEFAULT 0,
    rate_per_m    integer NOT NULL DEFAULT 0,
    enabled       boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS llm_profile_health (
    profile_id     bigint PRIMARY KEY REFERENCES llm_profiles(id) ON DELETE CASCADE,
    state          text NOT NULL DEFAULT 'ok' CHECK (state IN ('ok','cooling')),
    fails          integer NOT NULL DEFAULT 0,
    cooldown_until timestamptz
);

CREATE TABLE IF NOT EXISTS llm_records (
    id         bigserial PRIMARY KEY,
    task_id    bigint,
    agent_role text NOT NULL DEFAULT '',
    profile_id bigint,
    tok_in     bigint NOT NULL DEFAULT 0,
    tok_out    bigint NOT NULL DEFAULT 0,
    tok_cache  bigint NOT NULL DEFAULT 0,
    cost_usd   numeric NOT NULL DEFAULT 0,
    latency_ms integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_llm_records_task ON llm_records (task_id, created_at);

CREATE TABLE IF NOT EXISTS agent_prompts (
    id      bigserial PRIMARY KEY,
    role    text NOT NULL,
    version integer NOT NULL DEFAULT 1,
    body    text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    UNIQUE (role, version)
);

CREATE TABLE IF NOT EXISTS agents (
    id                bigserial PRIMARY KEY,
    role              text NOT NULL UNIQUE,
    prompt_id         bigint REFERENCES agent_prompts(id),
    profile_id        bigint REFERENCES llm_profiles(id),
    max_turns         integer NOT NULL DEFAULT 0,
    run_seconds       integer NOT NULL DEFAULT 1200,
    tool_bindings     jsonb NOT NULL DEFAULT '[]',
    interactive_shell boolean NOT NULL DEFAULT false
);

-- ===== 工具 / 技能 / 拦截（§3.5） =====
CREATE TABLE IF NOT EXISTS tools (
    key             text PRIMARY KEY,
    kind            text NOT NULL CHECK (kind IN ('builtin','shell','mcp')),
    description     text NOT NULL DEFAULT '',
    schema          jsonb NOT NULL DEFAULT '{}',
    agent_bindings  jsonb NOT NULL DEFAULT '[]',
    enabled         boolean NOT NULL DEFAULT true,
    defaults        jsonb NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS mcp_servers (
    id        bigserial PRIMARY KEY,
    name      text NOT NULL UNIQUE,
    transport text NOT NULL CHECK (transport IN ('stdio','http','sse')),
    config    jsonb NOT NULL DEFAULT '{}',
    enabled   boolean NOT NULL DEFAULT true
);

CREATE TABLE IF NOT EXISTS mcp_tools_cache (
    server_id  bigint NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    name       text NOT NULL,
    schema     jsonb NOT NULL DEFAULT '{}',
    fetched_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, name)
);

CREATE TABLE IF NOT EXISTS intercept_rules (
    id           bigserial PRIMARY KEY,
    priority     integer NOT NULL DEFAULT 100,
    tool_name_pat text NOT NULL DEFAULT '',
    input_pat    text NOT NULL DEFAULT '',
    match        text NOT NULL DEFAULT 'string' CHECK (match IN ('string','regex')),
    action       text NOT NULL CHECK (action IN ('allow','deny','ask')),
    ttl_secs     integer NOT NULL DEFAULT 60,
    enabled      boolean NOT NULL DEFAULT true
);

CREATE TABLE IF NOT EXISTS intercept_pending (
    id          bigserial PRIMARY KEY,
    rule_id     bigint REFERENCES intercept_rules(id) ON DELETE SET NULL,
    task_id     bigint,
    tool_name   text NOT NULL,
    input       jsonb NOT NULL DEFAULT '{}',
    replacement jsonb,
    status      text NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending','allowed','denied','modified','expired')),
    decided_by  text NOT NULL DEFAULT '',
    expires_at  timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_intercept_pending_status ON intercept_pending (status, expires_at);

CREATE TABLE IF NOT EXISTS skills_meta (
    name    text PRIMARY KEY,
    version text NOT NULL DEFAULT '1',
    triggers text[] NOT NULL DEFAULT '{}',
    mitre   text[] NOT NULL DEFAULT '{}',
    enabled boolean NOT NULL DEFAULT true
);

-- ===== 安全与沙箱（§3.6） =====
CREATE TABLE IF NOT EXISTS roe_rules (
    id      bigserial PRIMARY KEY,
    scope   text NOT NULL CHECK (scope IN ('global','task')),
    task_id bigint REFERENCES tasks(id) ON DELETE CASCADE,
    data    jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS credential_vault (
    id         bigserial PRIMARY KEY,
    task_id    bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('password','hash','key','token')),
    label      text NOT NULL,
    secret_enc bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    UNIQUE (task_id, label)
);

CREATE TABLE IF NOT EXISTS sandboxes (
    id           bigserial PRIMARY KEY,
    task_id      bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    container_id text NOT NULL DEFAULT '',
    image        text NOT NULL,
    status       text NOT NULL DEFAULT 'creating'
                 CHECK (status IN ('creating','running','destroyed','failed')),
    created_at   timestamptz NOT NULL DEFAULT now()
);
