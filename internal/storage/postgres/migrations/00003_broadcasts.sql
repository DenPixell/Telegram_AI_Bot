-- +goose Up
-- Рассылки всем пользователям бота (техобслуживание, новости). Хранятся для аудита и отчёта.
CREATE TABLE broadcasts (
    id          BIGSERIAL PRIMARY KEY,
    text        TEXT        NOT NULL CHECK (char_length(text) BETWEEN 1 AND 4000),
    created_by  BIGINT,                       -- Telegram ID администратора; NULL - через HTTP API
    status      TEXT        NOT NULL DEFAULT 'draft'
                CHECK (status IN ('draft', 'sending', 'done', 'cancelled')),
    total       INT         NOT NULL DEFAULT 0,
    sent        INT         NOT NULL DEFAULT 0,
    failed      INT         NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
-- Одновременно может идти только одна рассылка.
CREATE UNIQUE INDEX uniq_broadcast_sending ON broadcasts ((true)) WHERE status = 'sending';

-- +goose Down
DROP TABLE broadcasts;
