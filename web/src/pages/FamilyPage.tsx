import { useCallback, useEffect, useState } from "react";
import { api, ApiError, type Family, type Invite, type Member } from "../api";
import { Confirm, Modal, Spinner, useToast } from "../components/ui";

const zones = [
  "Europe/Kaliningrad", "Europe/Moscow", "Europe/Samara", "Asia/Yekaterinburg", "Asia/Omsk", "Asia/Novosibirsk",
  "Asia/Krasnoyarsk", "Asia/Irkutsk", "Asia/Yakutsk", "Asia/Vladivostok", "Asia/Magadan", "Asia/Kamchatka",
  "Europe/Minsk", "Europe/Kyiv", "Asia/Almaty", "Asia/Tashkent", "Asia/Tbilisi", "Asia/Yerevan", "Asia/Baku",
  "Europe/Istanbul", "Asia/Dubai", "Europe/Berlin", "Europe/London", "UTC",
];

type Pending =
  | { kind: "remove"; member: Member }
  | { kind: "transfer"; member: Member }
  | { kind: "revoke"; invite: Invite };

export function FamilyPage({ meID, family, members, onFamilyChange, onMembersChange, onLostAccess }: {
  meID: number; family: Family; members: Member[];
  onFamilyChange: (f: Family) => void; onMembersChange: () => Promise<void>; onLostAccess: () => void;
}) {
  const toast = useToast();
  const [invites, setInvites] = useState<Invite[] | null>(null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [relationFor, setRelationFor] = useState<Member | null>(null);

  const loadInvites = useCallback(async () => {
    try { setInvites(await api.invites()); }
    catch (err) { toast(err instanceof ApiError ? err.message : "Не удалось загрузить приглашения", true); }
  }, [toast]);
  useEffect(() => { loadInvites(); }, [loadInvites]);

  const fail = (err: unknown, fallback: string) => toast(err instanceof ApiError ? err.message : fallback, true);

  async function createInvite() {
    try {
      await api.createInvite();
      await loadInvites();
      toast("Приглашение создано");
    } catch (err) { fail(err, "Не удалось создать приглашение"); }
  }

  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      toast("Скопировано");
    } catch {
      toast("Не получилось скопировать — выделите и скопируйте вручную", true);
    }
  }

  async function runPending() {
    if (!pending) return;
    try {
      if (pending.kind === "remove") {
        await api.removeMember(pending.member.user_id);
        toast(`${pending.member.display_name || "Участник"} больше не в семье`);
        await onMembersChange();
      } else if (pending.kind === "transfer") {
        await api.transferOwnership(pending.member.user_id);
        onLostAccess(); // сессия бывшего владельца удалена на сервере
        return;
      } else {
        await api.revokeInvite(pending.invite.id);
        await loadInvites();
        toast("Приглашение отозвано");
      }
      setPending(null);
    } catch (err) {
      fail(err, "Не удалось выполнить действие");
      setPending(null);
    }
  }

  const name = (m: Member) => m.display_name || (m.username ? "@" + m.username : "Без имени");
  const initials = (m: Member) => name(m).replace("@", "").trim().slice(0, 1).toUpperCase() || "?";
  const expires = (iso: string) => new Date(iso).toLocaleDateString("ru-RU", { day: "numeric", month: "long" });

  return (
    <div className="stack">
      <section className="card" aria-labelledby="members-title">
        <div className="card-head">
          <div>
            <h2 className="card-title" id="members-title">Участники</h2>
            <p className="card-sub">Каждый видит в боте только свои траты, вы — траты всей семьи</p>
          </div>
        </div>
        {members.map((m) => (
          <div className="member" key={m.user_id}>
            <div className="avatar" aria-hidden="true">{initials(m)}</div>
            <div style={{ minWidth: 0 }}>
              <div className="member-name">
                {name(m)}
                {m.role === "owner" && <span className="badge badge-owner">👑 Глава семьи</span>}
                {m.relation && <span className="badge">{m.relation}</span>}
                {m.user_id === meID && <span className="badge">это вы</span>}
              </div>
              <div className="hint">в семье с {new Date(m.joined_at).toLocaleDateString("ru-RU", { day: "numeric", month: "long", year: "numeric" })}</div>
            </div>
            <div className="member-actions">
              <button className="btn btn-ghost btn-sm" onClick={() => setRelationFor(m)}>{m.relation ? "Изменить подпись" : "Добавить подпись"}</button>
              {m.user_id !== meID && (
                <>
                  <button className="btn btn-ghost btn-sm" onClick={() => setPending({ kind: "transfer", member: m })}>Сделать главой</button>
                  <button className="btn btn-danger btn-sm" onClick={() => setPending({ kind: "remove", member: m })}>Удалить</button>
                </>
              )}
            </div>
          </div>
        ))}
      </section>

      <section className="card" aria-labelledby="invites-title">
        <div className="card-head">
          <div>
            <h2 className="card-title" id="invites-title">Пригласить близких</h2>
            <p className="card-sub">Отправьте ссылку или код. Можно также написать боту «ссылка»</p>
          </div>
          <button className="btn btn-primary" onClick={createInvite}>＋ Новое приглашение</button>
        </div>
        {invites === null ? <Spinner /> : invites.length === 0 ? (
          <div className="empty-state"><span className="emoji" aria-hidden="true">✉️</span><p>Действующих приглашений нет.</p></div>
        ) : invites.map((inv) => (
          <div className="invite" key={inv.id}>
            <div className="invite-info">
              <div className="invite-code">{inv.code}</div>
              <div className="hint">до {expires(inv.expires_at)} · использовано {inv.used_count} из {inv.max_uses}</div>
            </div>
            {inv.link && <button className="btn btn-sm" onClick={() => copy(inv.link!)}>Скопировать ссылку</button>}
            <button className="btn btn-sm" onClick={() => copy(inv.code)}>Скопировать код</button>
            <button className="btn btn-danger btn-sm" onClick={() => setPending({ kind: "revoke", invite: inv })}>Отозвать</button>
          </div>
        ))}
      </section>

      <FamilySettings family={family} onSaved={onFamilyChange} />

      {relationFor && (
        <RelationDialog member={relationFor} onClose={() => setRelationFor(null)}
          onSaved={async () => { setRelationFor(null); await onMembersChange(); toast("Подпись сохранена"); }} />
      )}
      {pending?.kind === "remove" && (
        <Confirm title={`Удалить ${name(pending.member)} из семьи?`} confirm="Удалить" danger
          text="Траты этого человека останутся в истории семьи. Вернуться можно будет только по новому приглашению."
          onConfirm={runPending} onClose={() => setPending(null)} />
      )}
      {pending?.kind === "transfer" && (
        <Confirm title={`Передать главенство ${name(pending.member)}?`} confirm="Передать"
          text="Новый глава семьи получит доступ к панели и сможет управлять участниками. Вы останетесь участником, а эта панель для вас закроется."
          onConfirm={runPending} onClose={() => setPending(null)} />
      )}
      {pending?.kind === "revoke" && (
        <Confirm title="Отозвать приглашение?" confirm="Отозвать" danger
          text={`Код ${pending.invite.code} перестанет работать. Те, кто уже вступил, останутся в семье.`}
          onConfirm={runPending} onClose={() => setPending(null)} />
      )}
    </div>
  );
}

function RelationDialog({ member, onClose, onSaved }: { member: Member; onClose: () => void; onSaved: () => void }) {
  const [value, setValue] = useState(member.relation);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const suggestions = ["жена", "муж", "мама", "папа", "дочь", "сын", "бабушка", "дедушка"];
  return (
    <Modal title="Подпись участника" onClose={onClose}>
      <p className="hint" style={{ marginTop: 0 }}>Только для удобства: «жена», «сын». На права не влияет.</p>
      <form onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        try { await api.setRelation(member.user_id, value.trim()); onSaved(); }
        catch (err) { setError(err instanceof ApiError ? err.message : "Не удалось сохранить"); }
        finally { setBusy(false); }
      }}>
        <input className="input" value={value} maxLength={32} placeholder="например, жена" onChange={(e) => setValue(e.target.value)} />
        <div className="chips" style={{ marginTop: 10, marginBottom: 0 }}>
          {suggestions.map((s) => <button type="button" key={s} className="chip" aria-pressed={value === s} onClick={() => setValue(s)}>{s}</button>)}
        </div>
        {error && <p className="error-text" role="alert">{error}</p>}
        <div className="dialog-actions">
          <button type="button" className="btn" onClick={onClose}>Отмена</button>
          <button type="submit" className="btn btn-primary" disabled={busy}>Сохранить</button>
        </div>
      </form>
    </Modal>
  );
}

function FamilySettings({ family, onSaved }: { family: Family; onSaved: (f: Family) => void }) {
  const toast = useToast();
  const [name, setName] = useState(family.name);
  const [tz, setTZ] = useState(family.timezone);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const dirty = name !== family.name || tz !== family.timezone;
  return (
    <section className="card" aria-labelledby="settings-title">
      <div className="card-head">
        <div>
          <h2 className="card-title" id="settings-title">Настройки семьи</h2>
          <p className="card-sub">Часовой пояс семьи используется, если участник не указал свой</p>
        </div>
      </div>
      <form className="form-grid" onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError("");
        try { onSaved(await api.updateFamily(name.trim(), tz.trim())); toast("Настройки сохранены"); }
        catch (err) { setError(err instanceof ApiError ? err.message : "Не удалось сохранить"); }
        finally { setBusy(false); }
      }}>
        <label className="field">
          <span>Название</span>
          <input className="input" value={name} maxLength={64} placeholder="например, Ивановы" onChange={(e) => setName(e.target.value)} />
        </label>
        <label className="field">
          <span>Часовой пояс</span>
          <input className="input" list="tz-list" value={tz} onChange={(e) => setTZ(e.target.value)} />
          <datalist id="tz-list">{zones.map((z) => <option key={z} value={z} />)}</datalist>
        </label>
        {error && <p className="error-text full" role="alert" style={{ margin: 0 }}>{error}</p>}
        <div className="full" style={{ display: "flex", justifyContent: "flex-end" }}>
          <button className="btn btn-primary" type="submit" disabled={!dirty || busy}>Сохранить</button>
        </div>
      </form>
    </section>
  );
}
