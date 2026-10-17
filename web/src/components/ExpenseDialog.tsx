import { useState } from "react";
import { api, ApiError, type Expense, type Member } from "../api";
import { categories, money, parseMoneyInput, todayISO } from "../format";
import { Confirm, Modal, useToast } from "./ui";

// Создание и редактирование траты. Конфликт версий (трату изменили в боте) -> 409:
// показываем понятное сообщение и перечитываем данные.
export function ExpenseDialog({ expense, members, meID, onClose, onSaved }: {
  expense: Expense | null; members: Member[]; meID: number; onClose: () => void; onSaved: () => void;
}) {
  const toast = useToast();
  const isNew = expense === null;
  const [amount, setAmount] = useState(expense ? String(Number(expense.amount)).replace(".", ",") : "");
  const [category, setCategory] = useState(expense?.category ?? "еда");
  const [date, setDate] = useState(expense?.spent_at ?? todayISO());
  const [title, setTitle] = useState(expense?.title ?? "");
  const [comment, setComment] = useState(expense?.comment ?? "");
  const [userID, setUserID] = useState(expense?.user_id ?? meID);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [touched, setTouched] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const parsed = parseMoneyInput(amount);
  const amountInvalid = touched && parsed === null;

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setTouched(true);
    if (!parsed) return;
    setBusy(true);
    setError("");
    const input = { user_id: userID, amount: parsed, category, spent_at: date, title: title.trim(), comment: comment.trim() };
    try {
      if (isNew) await api.createExpense(input);
      else await api.updateExpense(expense.id, expense.version, input);
      toast(isNew ? "Трата добавлена" : "Изменения сохранены");
      onSaved();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        toast("Эту трату только что изменили в боте. Обновил список — откройте её ещё раз.", true);
        onSaved();
        return;
      }
      setError(err instanceof ApiError ? err.message : "Не удалось сохранить");
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!expense) return;
    try {
      await api.deleteExpense(expense.id);
      toast("Трата удалена");
      onSaved();
    } catch (err) {
      toast(err instanceof ApiError ? err.message : "Не удалось удалить", true);
      setConfirmDelete(false);
    }
  }

  const who = (m: Member) => (m.relation ? `${m.display_name || "Без имени"} · ${m.relation}` : m.display_name || "Без имени");

  if (confirmDelete && expense) {
    return (
      <Confirm
        title="Удалить трату?"
        text={`«${expense.title || expense.comment || expense.category}» на ${money(expense.amount)}. Это действие нельзя отменить.`}
        confirm="Удалить" danger onConfirm={remove} onClose={() => setConfirmDelete(false)}
      />
    );
  }

  return (
    <Modal title={isNew ? "Новая трата" : "Трата"} onClose={onClose}>
      <form onSubmit={save} noValidate>
        <div className="form-grid" style={{ marginTop: 14 }}>
          <label className="field">
            <span>Сумма, ₽</span>
            <input
              className="input" inputMode="decimal" placeholder="350" value={amount} autoComplete="off"
              aria-invalid={amountInvalid} onBlur={() => setTouched(true)}
              onChange={(e) => setAmount(e.target.value)}
            />
            {amountInvalid && <span className="error-text">Например: 350 или 1 200,50</span>}
          </label>
          <label className="field">
            <span>Дата</span>
            <input className="input" type="date" value={date} max={todayISO(new Date(Date.now() + 86_400_000))}
              onChange={(e) => setDate(e.target.value)} required />
          </label>

          <fieldset className="field full" style={{ border: 0, padding: 0, margin: 0 }}>
            <span>Категория</span>
            <div className="chips" style={{ marginBottom: 0 }}>
              {categories.map((c) => (
                <button type="button" key={c.id} className="chip" aria-pressed={category === c.id} onClick={() => setCategory(c.id)}>
                  <span aria-hidden="true">{c.emoji}</span>{c.label}
                </button>
              ))}
            </div>
          </fieldset>

          <label className="field">
            <span>Название</span>
            <input className="input" placeholder="кофе" value={title} maxLength={64} onChange={(e) => setTitle(e.target.value)} />
          </label>
          {isNew && members.length > 1 ? (
            <label className="field">
              <span>Чья трата</span>
              <select className="select" value={userID} onChange={(e) => setUserID(Number(e.target.value))}>
                {members.map((m) => <option key={m.user_id} value={m.user_id}>{who(m)}{m.user_id === meID ? " (вы)" : ""}</option>)}
              </select>
            </label>
          ) : <div />}
          <label className="field full">
            <span>Комментарий</span>
            <input className="input" placeholder="необязательно" value={comment} maxLength={200} onChange={(e) => setComment(e.target.value)} />
          </label>
        </div>

        {error && <p className="error-text" role="alert" style={{ marginBottom: 0 }}>{error}</p>}

        <div className="dialog-actions">
          {!isNew && (
            <button type="button" className="btn btn-danger" onClick={() => setConfirmDelete(true)} disabled={busy}>Удалить</button>
          )}
          <span className="spacer" />
          <button type="button" className="btn" onClick={onClose} disabled={busy}>Отмена</button>
          <button type="submit" className="btn btn-primary" disabled={busy}>{busy ? "Сохраняю…" : isNew ? "Добавить" : "Сохранить"}</button>
        </div>
      </form>
    </Modal>
  );
}
