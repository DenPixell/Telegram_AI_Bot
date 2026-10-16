-- +goose Up
-- Кэш распознавания категорий для слов, которых нет в config/synonyms.yaml.
-- Пополняется онлайн-тезаурусом (Викисловарь) и, позже, ответами LLM. Словарь «учится».
CREATE TABLE learned_terms (
    stem       TEXT PRIMARY KEY,                 -- основа слова (стеммер)
    word       TEXT        NOT NULL,             -- слово, как его написал пользователь (нормализованное)
    title      TEXT        NOT NULL DEFAULT '',  -- канонический title траты
    category   TEXT CHECK (category IS NULL OR category IN
               ('еда','транспорт','жильё','развлечения','здоровье','покупки','другое')),
                                                 -- NULL = не распознано (отрицательный кэш)
    source     TEXT        NOT NULL CHECK (source IN ('wiktionary', 'llm')),
    hits       INT         NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE learned_terms;
