-- +goose Up
-- Категории - данные, а не ограничение схемы: новая категория добавляется INSERT-ом,
-- без миграции с блокировкой таблицы expenses. Приложение читает набор при старте.
CREATE TABLE categories (
    name        TEXT PRIMARY KEY,                       -- то, что хранится в expenses.category
    title       TEXT    NOT NULL,                       -- подпись в панели
    emoji       TEXT    NOT NULL DEFAULT '',
    position    INT     NOT NULL,                       -- порядок отображения
    is_fallback BOOLEAN NOT NULL DEFAULT false,         -- куда попадает нераспознанное («другое»)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Запасная категория ровно одна.
CREATE UNIQUE INDEX uniq_fallback_category ON categories (is_fallback) WHERE is_fallback;

INSERT INTO categories (name, title, emoji, position, is_fallback) VALUES
    ('еда',         'Еда',         '🍽️', 1, false),
    ('транспорт',   'Транспорт',   '🚕', 2, false),
    ('жильё',       'Жильё',       '🏠', 3, false),
    ('развлечения', 'Развлечения', '🎉', 4, false),
    ('здоровье',    'Здоровье',    '💊', 5, false),
    ('покупки',     'Покупки',     '🛍️', 6, false),
    ('другое',      'Другое',      '📦', 7, true);

-- Вместо CHECK со списком - внешние ключи. ON UPDATE CASCADE: переименование категории
-- переносится на все траты; удалить категорию, у которой есть траты, нельзя.
ALTER TABLE expenses DROP CONSTRAINT expenses_category_check;
ALTER TABLE expenses ADD CONSTRAINT expenses_category_fkey
    FOREIGN KEY (category) REFERENCES categories (name) ON UPDATE CASCADE;

ALTER TABLE learned_terms DROP CONSTRAINT learned_terms_category_check;
ALTER TABLE learned_terms ADD CONSTRAINT learned_terms_category_fkey
    FOREIGN KEY (category) REFERENCES categories (name) ON UPDATE CASCADE ON DELETE SET NULL;

-- +goose Down
ALTER TABLE learned_terms DROP CONSTRAINT learned_terms_category_fkey;
ALTER TABLE learned_terms ADD CONSTRAINT learned_terms_category_check CHECK (category IS NULL OR category IN
    ('еда','транспорт','жильё','развлечения','здоровье','покупки','другое'));
ALTER TABLE expenses DROP CONSTRAINT expenses_category_fkey;
ALTER TABLE expenses ADD CONSTRAINT expenses_category_check CHECK (category IN
    ('еда','транспорт','жильё','развлечения','здоровье','покупки','другое'));
DROP TABLE categories;
