import { createContext, useContext, useMemo } from "react";
import type { CategoryInfo } from "./api";

// Справочник категорий приходит с сервера (GET /api/categories, таблица categories):
// новая категория появляется в панели без изменения кода.
export interface CategoryMeta { id: string; emoji: string; label: string }

const CategoriesContext = createContext<CategoryMeta[]>([]);

export function CategoriesProvider({ items, children }: { items: CategoryInfo[]; children: React.ReactNode }) {
  const list = useMemo(() => items.map((c) => ({ id: c.name, emoji: c.emoji, label: c.title })), [items]);
  return <CategoriesContext.Provider value={list}>{children}</CategoriesContext.Provider>;
}

export function useCategories() {
  const list = useContext(CategoriesContext);
  const meta = (c: string): CategoryMeta => list.find((x) => x.id === c) ?? { id: c, emoji: "📦", label: c };
  return { list, meta };
}
