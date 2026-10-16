import { useCallback, useEffect, useState } from "react";
import { api, ApiError, setCSRF, type Me, type Member } from "./api";
import { ThemeMenu } from "./components/ThemeMenu";
import { Spinner, ToastProvider, useToast } from "./components/ui";
import { greeting } from "./format";
import { ExpensesPage } from "./pages/ExpensesPage";
import { FamilyPage } from "./pages/FamilyPage";
import { useTheme } from "./theme";

const botURL = "https://t.me/MyFamilyBudgetplus_Bot";

type State =
  | { kind: "loading"; text: string }
  | { kind: "login"; reason?: "expired" | "forbidden" | "lost" }
  | { kind: "ready"; me: Me };

export default function App() {
  const themeCtl = useTheme();
  return (
    <ToastProvider>
      <Root themeCtl={themeCtl} />
    </ToastProvider>
  );
}

function Root({ themeCtl }: { themeCtl: ReturnType<typeof useTheme> }) {
  const [state, setState] = useState<State>({ kind: "loading", text: "Открываю панель…" });

  useEffect(() => {
    (async () => {
      // Ссылка из бота: /auth?token=... Токен меняем на сессию POST-запросом, а не GET:
      // превью ссылок в Telegram не может его «съесть».
      const url = new URL(window.location.href);
      const token = url.searchParams.get("token");
      if (url.pathname === "/auth" && token) {
        window.history.replaceState(null, "", "/"); // токен не остаётся в истории и адресной строке
        try {
          const me = await api.exchange(token);
          setCSRF(me.csrf_token);
          setState({ kind: "ready", me });
        } catch (err) {
          setState({ kind: "login", reason: err instanceof ApiError && err.status === 403 ? "forbidden" : "expired" });
        }
        return;
      }
      try {
        const me = await api.me();
        setCSRF(me.csrf_token);
        setState({ kind: "ready", me });
      } catch (err) {
        setState({ kind: "login", reason: err instanceof ApiError && err.status === 403 ? "forbidden" : undefined });
      }
    })();
  }, []);

  // Стабильная ссылка: иначе каждая смена темы перерисовывает Root и заново грузит участников.
  const logout = useCallback((reason?: "expired" | "lost") => setState({ kind: "login", reason }), []);
  const top = <ThemeMenu settings={themeCtl.settings} theme={themeCtl.theme} onChange={themeCtl.setSettings} />;

  if (state.kind === "loading") return <div className="center-screen"><Spinner text={state.text} /></div>;
  if (state.kind === "login") return <Login reason={state.reason} themeButton={top} />;
  return <Panel me={state.me} themeButton={top} onLogout={logout} />;
}

function Login({ reason, themeButton }: { reason?: "expired" | "forbidden" | "lost"; themeButton: React.ReactNode }) {
  const title = reason === "lost" ? "Владение передано" : "Панель семейного бюджета";
  const text =
    reason === "expired" ? "Ссылка для входа устарела или уже использована. Ничего страшного — получите новую." :
    reason === "forbidden" ? "Панель доступна только главе семьи. Свои траты можно смотреть прямо в боте." :
    reason === "lost" ? "Теперь панелью управляет новый глава семьи. Ваши траты по-прежнему доступны в боте." :
    "Здесь видны траты всей семьи, участники и приглашения. Вход — по одноразовой ссылке из бота, без паролей.";
  return (
    <div className="center-screen">
      <div style={{ position: "fixed", top: 16, right: 16 }}>{themeButton}</div>
      <div className="welcome">
        <div className="brand-logo" aria-hidden="true">🏡</div>
        <h1>{title}</h1>
        <p>{text}</p>
        {reason !== "forbidden" && reason !== "lost" && (
          <ol className="steps">
            <li>Откройте бота в Telegram</li>
            <li>Напишите ему слово <b>панель</b></li>
            <li>Нажмите на ссылку из ответа — она действует 10 минут</li>
          </ol>
        )}
        <a className="btn btn-primary" href={botURL} target="_blank" rel="noopener noreferrer">Открыть бота в Telegram</a>
      </div>
    </div>
  );
}

function Panel({ me: initial, themeButton, onLogout }: {
  me: Me; themeButton: React.ReactNode; onLogout: (reason?: "expired" | "lost") => void;
}) {
  const toast = useToast();
  const [me, setMe] = useState(initial);
  const [tab, setTab] = useState<"expenses" | "family">("expenses");
  const [members, setMembers] = useState<Member[]>([]);

  const loadMembers = useCallback(async () => {
    try { setMembers(await api.members()); }
    catch (err) {
      if (err instanceof ApiError && err.status === 401) return onLogout("expired");
      toast(err instanceof ApiError ? err.message : "Не удалось загрузить участников", true);
    }
  }, [toast, onLogout]);
  useEffect(() => { loadMembers(); }, [loadMembers]);

  const owner = members.find((m) => m.user_id === me.user_id);
  const firstName = owner?.display_name.split(" ")[0];

  async function logout() {
    try { await api.logout(); } catch { /* сессия и так недействительна */ }
    onLogout();
  }

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">
          <div className="brand-logo" aria-hidden="true">🏡</div>
          <div style={{ minWidth: 0 }}>
            <div className="brand-name">{me.family.name ? `Семья «${me.family.name}»` : "Наша семья"}</div>
            <div className="brand-sub">Семейный бюджет</div>
          </div>
        </div>
        <div className="topbar-actions">
          {themeButton}
          {me.auth_mode === "magic_link" && <button className="btn btn-ghost btn-sm" onClick={logout}>Выйти</button>}
        </div>
      </header>

      <div className="hello">
        <h1>{greeting()}{firstName ? `, ${firstName}` : ""} 👋</h1>
        <p>{tab === "expenses" ? "Вот как расходуется семейный бюджет." : "Управляйте составом семьи и приглашениями."}</p>
      </div>

      <nav className="tabs" role="tablist" aria-label="Разделы">
        <button className="tab" role="tab" aria-selected={tab === "expenses"} onClick={() => setTab("expenses")}>💸 Траты</button>
        <button className="tab" role="tab" aria-selected={tab === "family"} onClick={() => setTab("family")}>👨‍👩‍👧 Семья</button>
      </nav>

      <main>
        {tab === "expenses" ? (
          <ExpensesPage members={members} meID={me.user_id} />
        ) : (
          <FamilyPage
            meID={me.user_id} family={me.family} members={members}
            onFamilyChange={(f) => setMe({ ...me, family: f })}
            onMembersChange={loadMembers}
            onLostAccess={() => onLogout("lost")}
          />
        )}
      </main>
    </div>
  );
}
