-- +goose Up
CREATE TABLE families (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL DEFAULT '',
    default_tz  TEXT        NOT NULL DEFAULT 'Europe/Moscow',   -- IANA
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    telegram_id  BIGINT PRIMARY KEY,
    display_name TEXT        NOT NULL DEFAULT '',
    username     TEXT        NOT NULL DEFAULT '',
    timezone     TEXT,                                          -- IANA, NULL = взять у семьи
    state        TEXT        NOT NULL DEFAULT 'new',            -- FSM онбординга
    state_data   JSONB       NOT NULL DEFAULT '{}',
    is_active    BOOLEAN     NOT NULL DEFAULT true,             -- false, если бот заблокирован (403)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE family_members (
    user_id    BIGINT PRIMARY KEY REFERENCES users(telegram_id),  -- PK: одна семья на пользователя
    family_id  BIGINT NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    role       TEXT   NOT NULL CHECK (role IN ('owner','member')),
    relation   TEXT   NOT NULL DEFAULT '',                        -- «жена», «муж», ...
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_members_family ON family_members (family_id);
-- ровно один владелец на семью:
CREATE UNIQUE INDEX uniq_family_owner ON family_members (family_id) WHERE role = 'owner';

CREATE TABLE invites (
    id          BIGSERIAL PRIMARY KEY,
    family_id   BIGINT NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    code        TEXT   NOT NULL UNIQUE,                           -- 10 символов, нормализованный вид
    created_by  BIGINT NOT NULL REFERENCES users(telegram_id),
    expires_at  TIMESTAMPTZ NOT NULL,
    max_uses    INT    NOT NULL DEFAULT 10,
    used_count  INT    NOT NULL DEFAULT 0,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_invites_family ON invites (family_id);

CREATE TABLE expenses (
    id           BIGSERIAL PRIMARY KEY,
    family_id    BIGINT NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    user_id      BIGINT NOT NULL REFERENCES users(telegram_id),   -- Telegram ID владельца траты
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0 AND amount_minor <= 100000000000),
    category     TEXT   NOT NULL CHECK (category IN
                 ('еда','транспорт','жильё','развлечения','здоровье','покупки','другое')),
    spent_at     DATE   NOT NULL,                                 -- локальная дата пользователя
    title        TEXT   NOT NULL DEFAULT '',                      -- канон из словаря: «ресторан»
    comment      TEXT   NOT NULL DEFAULT '' CHECK (char_length(comment) <= 200), -- как написал пользователь
    version      INT    NOT NULL DEFAULT 1,                       -- оптимистичная блокировка
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_exp_family_date  ON expenses (family_id, spent_at DESC);
CREATE INDEX idx_exp_user_date    ON expenses (user_id, spent_at DESC);
CREATE INDEX idx_exp_user_created ON expenses (user_id, created_at DESC, id DESC);  -- «последняя трата»

CREATE TABLE login_tokens (          -- одноразовые ссылки входа в панель
    token_hash BYTEA PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(telegram_id),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ
);

CREATE TABLE sessions (
    id_hash    BYTEA PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(telegram_id),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sessions_user ON sessions (user_id);

CREATE TABLE llm_usage (             -- учёт токенов для квот и контроля стоимости
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT,
    family_id         BIGINT,
    model             TEXT NOT NULL,
    prompt_tokens     INT  NOT NULL DEFAULT 0,
    completion_tokens INT  NOT NULL DEFAULT 0,
    latency_ms        INT  NOT NULL DEFAULT 0,
    ok                BOOLEAN NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_usage_user_time ON llm_usage (user_id, created_at DESC);
CREATE INDEX idx_usage_family_time ON llm_usage (family_id, created_at DESC);

CREATE TABLE processed_updates (     -- дедупликация апдейтов Telegram, чистка старше 2 суток
    update_id    BIGINT PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE processed_updates;
DROP TABLE llm_usage;
DROP TABLE sessions;
DROP TABLE login_tokens;
DROP TABLE expenses;
DROP TABLE invites;
DROP TABLE family_members;
DROP TABLE users;
DROP TABLE families;
