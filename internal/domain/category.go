package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Category - имя категории траты («еда»). Набор категорий - данные в таблице categories,
// а не константы: новая категория добавляется строкой в БД, без изменения схемы и кода.
type Category string

// CategoryInfo - категория со всем, что нужно для отображения.
type CategoryInfo struct {
	Name     Category
	Title    string // подпись в панели: «Еда»
	Emoji    string
	Position int  // порядок отображения
	Fallback bool // сюда попадает трата с нераспознанной категорией («другое»); ровно одна
}

// CategorySet - набор категорий, загруженный из БД при старте. Только для чтения,
// поэтому безопасен для одновременного использования.
type CategorySet struct {
	list     []CategoryInfo
	byName   map[Category]CategoryInfo
	fallback Category
}

// NewCategorySet проверяет набор: непустые уникальные имена и ровно одна запасная категория.
// list должен быть отсортирован по Position.
func NewCategorySet(list []CategoryInfo) (*CategorySet, error) {
	s := &CategorySet{list: list, byName: make(map[Category]CategoryInfo, len(list))}
	var errs []error
	for _, c := range list {
		if strings.TrimSpace(string(c.Name)) == "" {
			errs = append(errs, errors.New("category with empty name"))
			continue
		}
		if _, dup := s.byName[c.Name]; dup {
			errs = append(errs, fmt.Errorf("duplicate category %q", c.Name))
		}
		s.byName[c.Name] = c
		if c.Fallback {
			if s.fallback != "" {
				errs = append(errs, fmt.Errorf("two fallback categories: %q and %q", s.fallback, c.Name))
			}
			s.fallback = c.Name
		}
	}
	if len(list) == 0 {
		errs = append(errs, errors.New("no categories"))
	} else if s.fallback == "" {
		errs = append(errs, errors.New("no fallback category"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("categories: %w", err)
	}
	return s, nil
}

// List - категории в порядке отображения.
func (s *CategorySet) List() []CategoryInfo { return s.list }

// Names - имена категорий в порядке отображения (для схемы инструментов LLM).
func (s *CategorySet) Names() []string {
	out := make([]string, len(s.list))
	for i, c := range s.list {
		out[i] = string(c.Name)
	}
	return out
}

func (s *CategorySet) Valid(c Category) bool {
	_, ok := s.byName[c]
	return ok
}

// Fallback - категория для нераспознанных трат.
func (s *CategorySet) Fallback() Category { return s.fallback }

// Parse возвращает категорию или запасную, если значение неизвестно
// (например, модель выдумала свою категорию).
func (s *CategorySet) Parse(v string) Category {
	if c := Category(v); s.Valid(c) {
		return c
	}
	return s.fallback
}
