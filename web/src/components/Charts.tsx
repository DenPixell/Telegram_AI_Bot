import { useState } from "react";
import type { Summary } from "../api";
import { useCategories } from "../categories";
import { addDays, money, moneyCompact, shortDay } from "../format";

// Графики одного ряда: один цвет (--series), подписи и значения - цветом текста.
// Таблица значений - это список трат и подписи у полос.

// Траты по категориям: горизонтальные полосы, значение у конца полосы.
export function CategoryBars({ data }: { data: Summary["by_category"] }) {
  const { meta: categoryMeta } = useCategories();
  const max = Math.max(...data.map((d) => Number(d.total)), 1);
  return (
    <div className="bars" role="list" aria-label="Траты по категориям">
      {data.map((d) => {
        const meta = categoryMeta(d.category);
        return (
          <div className="bar-row" role="listitem" key={d.category} title={`${meta.label}: ${money(d.total)}, ${d.count} шт.`}>
            <div className="bar-label"><span aria-hidden="true">{meta.emoji}</span><span>{meta.label}</span></div>
            <div className="bar-track">
              <div className="bar-fill" style={{ width: `${Math.max((Number(d.total) / max) * 100, 1.5)}%` }} />
              <span className="bar-value">{money(d.total)}</span>
            </div>
          </div>
        );
      })}
    </div>
  );
}

// Траты по людям - та же форма, подпись - имя.
export function MemberBars({ data, name }: { data: Summary["by_member"]; name: (id: number) => string }) {
  const max = Math.max(...data.map((d) => Number(d.total)), 1);
  return (
    <div className="bars members-bars" role="list" aria-label="Траты по членам семьи">
      {data.map((d) => (
        <div className="bar-row" role="listitem" key={d.user_id} title={`${name(d.user_id)}: ${money(d.total)}`}>
          <div className="bar-label"><span>{name(d.user_id)}</span></div>
          <div className="bar-track">
            <div className="bar-fill" style={{ width: `${Math.max((Number(d.total) / max) * 100, 1.5)}%` }} />
            <span className="bar-value">{money(d.total)}</span>
          </div>
        </div>
      ))}
    </div>
  );
}

type Point = { key: string; label: string; total: number };

// Траты по дням (или месяцам на длинном периоде): столбики от общей базовой линии,
// одна сетка с круглыми значениями, подсказка при наведении.
export function DailyColumns({ data, from, to }: { data: Summary["by_day"]; from?: string; to?: string }) {
  const [hover, setHover] = useState<number | null>(null);
  const points = toPoints(data, from, to);
  if (points.length === 0) return null;
  const max = Math.max(...points.map((p) => p.total), 1);
  const tick = niceTick(max);
  const top = Math.ceil(max / tick) * tick;
  const labelEvery = Math.ceil(points.length / 6);
  return (
    <div>
      <div className="columns" role="img" aria-label="Траты по дням">
        {[1, 2].map((i) => i * (top / 2) <= top && (
          <div key={i} className="gridline" style={{ bottom: `${(i * (top / 2) / top) * 100}%` }}>
            <span>{moneyCompact(i * (top / 2))}</span>
          </div>
        ))}
        {points.map((p, i) => (
          <div
            key={p.key}
            className={"column" + (p.total === 0 ? " empty" : "")}
            onMouseEnter={() => setHover(i)}
            onMouseLeave={() => setHover(null)}
            onFocus={() => setHover(i)}
            onBlur={() => setHover(null)}
            tabIndex={p.total > 0 ? 0 : -1}
            aria-label={`${p.label}: ${money(String(p.total))}`}
          >
            <div className="column-fill" style={{ height: `${(p.total / top) * 100}%` }} />
            {hover === i && <div className="tooltip">{p.label} · {money(String(p.total))}</div>}
          </div>
        ))}
      </div>
      <div className="columns-axis" aria-hidden="true">
        {points.filter((_, i) => i % labelEvery === 0).map((p) => <span key={p.key}>{p.label}</span>)}
      </div>
    </div>
  );
}

// toPoints заполняет пустые дни нулями; период длиннее двух месяцев - по месяцам.
// Суммы складываются в копейках, чтобы не накапливать ошибку float.
function toPoints(data: Summary["by_day"], from?: string, to?: string): Point[] {
  if (data.length === 0) return [];
  const start = from ?? data[0].date;
  const end = to ?? data[data.length - 1].date;
  const byDay = new Map(data.map((d) => [d.date, Math.round(Number(d.total) * 100)]));
  const days: string[] = [];
  for (let d = start; d <= end && days.length < 800; d = addDays(d, 1)) days.push(d);
  if (days.length <= 62) {
    return days.map((d) => ({ key: d, label: shortDay(d), total: (byDay.get(d) ?? 0) / 100 }));
  }
  const months = new Map<string, number>();
  for (const d of days) months.set(d.slice(0, 7), (months.get(d.slice(0, 7)) ?? 0) + (byDay.get(d) ?? 0));
  const names = ["янв", "фев", "мар", "апр", "май", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"];
  return [...months].map(([m, k]) => ({ key: m, label: `${names[Number(m.slice(5)) - 1]} ${m.slice(2, 4)}`, total: k / 100 }));
}

function niceTick(max: number): number {
  const raw = max / 2;
  const pow = 10 ** Math.floor(Math.log10(raw));
  const n = raw / pow;
  return (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * pow;
}
