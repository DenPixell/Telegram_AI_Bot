import { useCallback, useEffect, useMemo, useState } from "react";
import { api, ApiError, type Expense, type Filter, type Member, type Summary } from "../api";
import { CategoryBars, DailyColumns, MemberBars } from "../components/Charts";
import { ExpenseDialog } from "../components/ExpenseDialog";
import { Spinner, useToast } from "../components/ui";
import { addDays, categories, categoryMeta, dayLabel, money, plural, todayISO } from "../format";

type Period = "month" | "last_month" | "week" | "all" | "custom";

const periods: { id: Period; label: string }[] = [
  { id: "month", label: "Этот месяц" },
  { id: "last_month", label: "Прошлый" },
  { id: "week", label: "Неделя" },
  { id: "all", label: "Всё время" },
  { id: "custom", label: "Даты…" },
];

function range(p: Period, custom: { from: string; to: string }): { from?: string; to?: string } {
  const today = todayISO();
  const [y, m] = today.split("-").map(Number);
  const first = `${y}-${String(m).padStart(2, "0")}-01`;
  switch (p) {
    case "month": return { from: first, to: today };
    case "last_month": {
      const lastPrev = addDays(first, -1);
      return { from: lastPrev.slice(0, 8) + "01", to: lastPrev };
    }
    case "week": {
      const wd = (new Date().getDay() + 6) % 7; // неделя с понедельника
      return { from: addDays(today, -wd), to: today };
    }
    case "custom": return { from: custom.from || undefined, to: custom.to || undefined };
    default: return {};
  }
}

const pageSize = 50;

export function ExpensesPage({ members, meID }: { members: Member[]; meID: number }) {
  const toast = useToast();
  const [period, setPeriod] = useState<Period>("month");
  const [custom, setCustom] = useState({ from: addDays(todayISO(), -30), to: todayISO() });
  const [memberID, setMemberID] = useState(0);
  const [cats, setCats] = useState<string[]>([]);
  const [q, setQ] = useState("");
  const [query, setQuery] = useState(""); // поиск с задержкой

  const [items, setItems] = useState<Expense[]>([]);
  const [count, setCount] = useState(0);
  const [summary, setSummary] = useState<Summary | null>(null);
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<Expense | null | undefined>(undefined); // null - новая

  useEffect(() => {
    const t = window.setTimeout(() => setQuery(q), 350);
    return () => window.clearTimeout(t);
  }, [q]);

  const { from, to } = range(period, custom);
  const filter: Filter = useMemo(
    () => ({ from, to, member_id: memberID || undefined, categories: cats, q: query }),
    [from, to, memberID, cats, query],
  );

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [list, sum] = await Promise.all([api.expenses({ ...filter, limit: pageSize }), api.summary(filter)]);
      setItems(list.items);
      setCount(list.count);
      setSummary(sum);
    } catch (err) {
      toast(err instanceof ApiError ? err.message : "Не удалось загрузить траты", true);
    } finally {
      setLoading(false);
    }
  }, [filter, toast]);

  useEffect(() => { load(); }, [load]);

  async function loadMore() {
    try {
      const list = await api.expenses({ ...filter, limit: pageSize, offset: items.length });
      setItems((prev) => [...prev, ...list.items]);
    } catch (err) {
      toast(err instanceof ApiError ? err.message : "Не удалось загрузить", true);
    }
  }

  const name = useCallback((id: number) => {
    const m = members.find((x) => x.user_id === id);
    if (!m) return "Бывший участник";
    const n = m.display_name || m.username || "Без имени";
    return m.relation ? `${n} · ${m.relation}` : n;
  }, [members]);

  const groups = useMemo(() => {
    const g: { date: string; items: Expense[]; total: number }[] = [];
    for (const e of items) {
      const last = g[g.length - 1];
      if (last?.date === e.spent_at) {
        last.items.push(e);
        last.total += Math.round(Number(e.amount) * 100);
      } else g.push({ date: e.spent_at, items: [e], total: Math.round(Number(e.amount) * 100) });
    }
    return g;
  }, [items]);

  const days = from && to ? Math.max(1, Math.round((Date.parse(to) - Date.parse(from)) / 86_400_000) + 1) : 0;
  const perDay = summary && days ? Number(summary.total) / days : 0;
  const filtered = memberID !== 0 || cats.length > 0 || query !== "";
  const top = summary?.by_category[0];

  return (
    <>
      <div className="filters">
        <div className="segmented" role="group" aria-label="Период">
          {periods.map((p) => (
            <button key={p.id} aria-pressed={period === p.id} onClick={() => setPeriod(p.id)}>{p.label}</button>
          ))}
        </div>
        {period === "custom" && (
          <div className="custom-range">
            <input className="input" type="date" aria-label="С" value={custom.from} max={custom.to} onChange={(e) => setCustom({ ...custom, from: e.target.value })} />
            <span aria-hidden="true">—</span>
            <input className="input" type="date" aria-label="По" value={custom.to} min={custom.from} onChange={(e) => setCustom({ ...custom, to: e.target.value })} />
          </div>
        )}
        {members.length > 1 && (
          <select className="select" aria-label="Член семьи" value={memberID} onChange={(e) => setMemberID(Number(e.target.value))}>
            <option value={0}>Вся семья</option>
            {members.map((m) => <option key={m.user_id} value={m.user_id}>{name(m.user_id)}</option>)}
          </select>
        )}
        <input className="input search" type="search" placeholder="Поиск: «такси», «рестик»…" aria-label="Поиск по тратам"
          value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <div className="chips" role="group" aria-label="Категории">
        {categories.map((c) => {
          const on = cats.includes(c.id);
          return (
            <button key={c.id} className="chip" aria-pressed={on}
              onClick={() => setCats(on ? cats.filter((x) => x !== c.id) : [...cats, c.id])}>
              <span aria-hidden="true">{c.emoji}</span>{c.label}
            </button>
          );
        })}
        {filtered && <button className="chip" onClick={() => { setCats([]); setMemberID(0); setQ(""); }}>✕ Сбросить</button>}
      </div>

      {loading && !summary ? <Spinner text="Загружаю траты…" /> : summary && (
        <>
          <div className="stats">
            <div className="stat stat-hero">
              <div className="stat-label">Потрачено</div>
              <div className="stat-value">{money(summary.total)}</div>
              <div className="stat-note">{plural(summary.count, "трата", "траты", "трат")}</div>
            </div>
            <div className="stat">
              <div className="stat-label">В среднем за день</div>
              <div className="stat-value">{days ? money(perDay.toFixed(2)) : "—"}</div>
              <div className="stat-note">{days ? plural(days, "день", "дня", "дней") + " в периоде" : "за всё время не считаем"}</div>
            </div>
            <div className="stat">
              <div className="stat-label">Больше всего</div>
              <div className="stat-value">{top ? `${categoryMeta(top.category).emoji} ${categoryMeta(top.category).label}` : "—"}</div>
              <div className="stat-note">{top ? money(top.total) : "пока нет трат"}</div>
            </div>
          </div>

          {summary.count > 0 && (
            <div className="grid-2">
              <section className="card" aria-labelledby="by-cat">
                <div className="card-head"><h2 className="card-title" id="by-cat">По категориям</h2></div>
                <CategoryBars data={summary.by_category} />
              </section>
              <section className="card" aria-labelledby="by-day">
                <div className="card-head"><h2 className="card-title" id="by-day">По дням</h2></div>
                <DailyColumns data={summary.by_day} from={from} to={to} />
                {summary.by_member.length > 1 && (
                  <>
                    <h3 className="card-title" style={{ fontSize: 15, margin: "20px 0 12px" }}>Кто сколько</h3>
                    <MemberBars data={summary.by_member} name={name} />
                  </>
                )}
              </section>
            </div>
          )}

          <section className="card" aria-labelledby="list-title">
            <div className="card-head">
              <div>
                <h2 className="card-title" id="list-title">Траты</h2>
                <p className="card-sub">Нажмите на трату, чтобы изменить или удалить её</p>
              </div>
              <button className="btn btn-primary" onClick={() => setEditing(null)}>＋ Добавить</button>
            </div>
            {items.length === 0 ? (
              <div className="empty-state">
                <span className="emoji" aria-hidden="true">{filtered ? "🔍" : "🌱"}</span>
                {filtered ? <p>Ничего не нашлось. Попробуйте другой фильтр.</p> : (
                  <>
                    <p>Здесь пока пусто.</p>
                    <p>Напишите боту, например, <code>кофе 350</code> — трата сразу появится здесь.</p>
                  </>
                )}
              </div>
            ) : (
              <>
                {groups.map((g) => (
                  <div className="day-group" key={g.date}>
                    <div className="day-head"><span>{dayLabel(g.date)}</span><span>{money(String(g.total / 100))}</span></div>
                    {g.items.map((e) => {
                      const meta = categoryMeta(e.category);
                      return (
                        <button className="expense" key={e.id} onClick={() => setEditing(e)}
                          aria-label={`${e.title || e.comment || meta.label}, ${money(e.amount)}, ${meta.label}${members.length > 1 ? ", " + name(e.user_id) : ""}. Изменить`}>
                          <span className="expense-icon" aria-hidden="true">{meta.emoji}</span>
                          <span style={{ minWidth: 0 }}>
                            <span className="expense-title" style={{ display: "block" }}>{e.title || e.comment || meta.label}</span>
                            <span className="expense-meta" style={{ display: "block" }}>
                              {meta.label}{members.length > 1 ? ` · ${name(e.user_id)}` : ""}
                              {e.comment && e.comment !== e.title ? ` · «${e.comment}»` : ""}
                            </span>
                          </span>
                          <span className="expense-amount">{money(e.amount)}</span>
                        </button>
                      );
                    })}
                  </div>
                ))}
                {items.length < count && (
                  <div className="more"><button className="btn" onClick={loadMore}>Показать ещё ({count - items.length})</button></div>
                )}
              </>
            )}
          </section>
        </>
      )}

      {editing !== undefined && (
        <ExpenseDialog
          expense={editing} members={members} meID={meID}
          onClose={() => setEditing(undefined)}
          onSaved={() => { setEditing(undefined); load(); }}
        />
      )}
    </>
  );
}
