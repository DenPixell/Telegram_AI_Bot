CREATE TABLE families (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT        NOT NULL,
    invite_code  TEXT        NOT NULL UNIQUE,
    head_user_id BIGINT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id          BIGSERIAL PRIMARY KEY,
    telegram_id BIGINT      NOT NULL UNIQUE,
    name        TEXT        NOT NULL,
    family_id   BIGINT REFERENCES families(id),
    role        TEXT        NOT NULL DEFAULT 'member',
    title       TEXT,
    timezone    TEXT        NOT NULL DEFAULT 'Europe/Moscow',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE categories (
    id        BIGSERIAL PRIMARY KEY,
    family_id BIGINT REFERENCES families(id),
    name      TEXT NOT NULL,
    UNIQUE (family_id, name)
);

CREATE TABLE expenses (
    id          BIGSERIAL PRIMARY KEY,
    family_id   BIGINT      NOT NULL REFERENCES families(id),
    user_id     BIGINT      NOT NULL REFERENCES users(id),
    amount      BIGINT      NOT NULL CHECK (amount > 0),
    currency    TEXT        NOT NULL DEFAULT 'RUB',
    category_id BIGINT REFERENCES categories(id),
    description TEXT,
    spent_at    TIMESTAMPTZ NOT NULL,
    raw_text    TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_expenses_family_spent ON expenses (family_id, spent_at);
