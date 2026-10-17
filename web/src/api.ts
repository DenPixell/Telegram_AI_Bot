// Клиент REST API панели (api/openapi.yaml). Сессия - HttpOnly cookie, её JS не видит;
// CSRF-токен приходит в /api/me и отправляется в заголовке изменяющих запросов.

export interface Family { id: number; name: string; timezone: string; created_at: string }
export interface Me { user_id: number; role: "owner"; family: Family; csrf_token?: string; auth_mode: "magic_link" | "none" }
export interface Expense {
  id: number; user_id: number; amount: string; category: string; spent_at: string;
  title: string; comment: string; version: number; created_at: string; updated_at: string;
}
export interface ExpenseList { items: Expense[]; total: string; count: number; limit: number; offset: number }
export interface Summary {
  total: string; count: number;
  by_category: { category: string; total: string; count: number }[];
  by_member: { user_id: number; total: string; count: number }[];
  by_day: { date: string; total: string }[];
}
export interface Member { user_id: number; display_name: string; username: string; role: "owner" | "member"; relation: string; joined_at: string }
export interface Invite { id: number; code: string; link?: string; expires_at: string; max_uses: number; used_count: number; created_at: string }

export interface Filter {
  member_id?: number; from?: string; to?: string; categories?: string[]; q?: string; limit?: number; offset?: number;
}

export interface ExpenseInput { user_id?: number; amount: string; category: string; spent_at: string; title: string; comment: string }

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
  }
}

let csrf = "";
export const setCSRF = (t?: string) => { csrf = t ?? ""; };

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (method !== "GET" && csrf) headers["X-CSRF-Token"] = csrf;
  let res: Response;
  try {
    res = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), credentials: "same-origin" });
  } catch {
    throw new ApiError(0, "network", "Нет связи с сервером. Проверьте интернет и попробуйте ещё раз.");
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const e = (data as { error?: { code?: string; message?: string } }).error;
    throw new ApiError(res.status, e?.code ?? "unknown", e?.message ?? "Что-то пошло не так. Попробуйте ещё раз.");
  }
  return data as T;
}

function query(f: Filter): string {
  const p = new URLSearchParams();
  if (f.member_id) p.set("member_id", String(f.member_id));
  if (f.from) p.set("from", f.from);
  if (f.to) p.set("to", f.to);
  if (f.categories?.length) p.set("category", f.categories.join(","));
  if (f.q?.trim()) p.set("q", f.q.trim());
  if (f.limit) p.set("limit", String(f.limit));
  if (f.offset) p.set("offset", String(f.offset));
  const s = p.toString();
  return s ? "?" + s : "";
}

export const api = {
  me: () => request<Me>("GET", "/api/me"),
  exchange: (token: string) => request<Me>("POST", "/api/auth/exchange", { token }),
  logout: () => request<void>("POST", "/api/auth/logout"),

  expenses: (f: Filter) => request<ExpenseList>("GET", "/api/expenses" + query(f)),
  summary: (f: Filter) => request<Summary>("GET", "/api/expenses/summary" + query({ ...f, limit: undefined, offset: undefined })),
  createExpense: (e: ExpenseInput) => request<Expense>("POST", "/api/expenses", e),
  updateExpense: (id: number, version: number, e: ExpenseInput) => {
    const { user_id: _ignored, ...rest } = e;
    return request<Expense>("PUT", `/api/expenses/${id}`, { version, ...rest });
  },
  deleteExpense: (id: number) => request<void>("DELETE", `/api/expenses/${id}`),

  family: () => request<Family>("GET", "/api/family"),
  updateFamily: (name: string, timezone: string) => request<Family>("PUT", "/api/family", { name, timezone }),
  members: () => request<{ items: Member[] }>("GET", "/api/members").then((r) => r.items),
  setRelation: (userID: number, relation: string) => request<void>("PATCH", `/api/members/${userID}`, { relation }),
  removeMember: (userID: number) => request<void>("DELETE", `/api/members/${userID}`),
  transferOwnership: (userID: number) => request<void>("POST", `/api/members/${userID}/transfer-ownership`),
  invites: () => request<{ items: Invite[] }>("GET", "/api/invites").then((r) => r.items),
  createInvite: () => request<Invite>("POST", "/api/invites"),
  revokeInvite: (id: number) => request<void>("DELETE", `/api/invites/${id}`),
};
