// Форматирование для показа. Деньги приходят строкой "350.00" и считаются только на сервере;
// здесь число нужно лишь для отображения и ширины столбиков.

const rub = new Intl.NumberFormat("ru-RU", { style: "currency", currency: "RUB", maximumFractionDigits: 2, minimumFractionDigits: 0 });
const rubCompact = new Intl.NumberFormat("ru-RU", { notation: "compact", maximumFractionDigits: 1 });

export const money = (s: string) => rub.format(Number(s));
export const moneyCompact = (n: number) => rubCompact.format(n) + " ₽";
export const amount = (s: string) => Number(s);

// Даты траты - "YYYY-MM-DD" без пояса: не превращаем их в Date, чтобы не сдвинуть день.
export function todayISO(d = new Date()): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

export function addDays(iso: string, n: number): string {
  const [y, m, d] = iso.split("-").map(Number);
  return todayISO(new Date(y, m - 1, d + n));
}

const months = ["января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"];
const weekdays = ["воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"];

export function dayLabel(iso: string, today = todayISO()): string {
  if (iso === today) return "Сегодня";
  if (iso === addDays(today, -1)) return "Вчера";
  const [y, m, d] = iso.split("-").map(Number);
  const wd = weekdays[new Date(y, m - 1, d).getDay()];
  const year = y === Number(today.slice(0, 4)) ? "" : ` ${y}`;
  return `${d} ${months[m - 1]}${year}, ${wd}`;
}

export function shortDay(iso: string): string {
  const [, m, d] = iso.split("-").map(Number);
  return `${d} ${months[m - 1].slice(0, 3)}`;
}

export function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10, m100 = n % 100;
  const w = m10 === 1 && m100 !== 11 ? one : m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14) ? few : many;
  return `${n} ${w}`;
}

export function greeting(d = new Date()): string {
  const h = d.getHours();
  if (h < 5) return "Доброй ночи";
  if (h < 12) return "Доброе утро";
  if (h < 18) return "Добрый день";
  return "Добрый вечер";
}

// Ввод суммы человеком: «1 200,50» -> "1200.50". null - не сумма.
export function parseMoneyInput(s: string): string | null {
  const v = s.replace(/\s|₽/g, "").replace(",", ".");
  if (!/^\d{1,10}(\.\d{1,2})?$/.test(v) || Number(v) <= 0) return null;
  return v;
}
