import { useEffect, useRef, useState } from "react";
import { isDarkAt, type Mode, type ThemeSettings, type Theme } from "../theme";

const modes: { id: Mode; icon: string; label: string }[] = [
  { id: "light", icon: "☀️", label: "Светлая" },
  { id: "dark", icon: "🌙", label: "Тёмная" },
  { id: "auto", icon: "🕐", label: "Авто" },
];

// Меню темы: светлая / тёмная / авто, а для авто - часы переключения.
export function ThemeMenu({ settings, theme, onChange }: {
  settings: ThemeSettings; theme: Theme; onChange: (s: ThemeSettings) => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && setOpen(false);
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const icon = settings.mode === "auto" ? "🕐" : theme === "dark" ? "🌙" : "☀️";
  const sameTime = settings.darkFrom === settings.lightFrom;

  return (
    <div style={{ position: "relative" }} ref={ref}>
      <button
        className="btn btn-ghost btn-icon"
        aria-label="Тема оформления"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        title="Тема оформления"
      >
        <span aria-hidden="true">{icon}</span>
      </button>
      {open && (
        <div className="popover" role="dialog" aria-label="Тема оформления">
          <h3>Оформление</h3>
          <div className="segmented" role="group" aria-label="Режим темы">
            {modes.map((m) => (
              <button key={m.id} aria-pressed={settings.mode === m.id} onClick={() => onChange({ ...settings, mode: m.id })}>
                <span aria-hidden="true">{m.icon}</span> {m.label}
              </button>
            ))}
          </div>
          {settings.mode === "auto" && (
            <>
              <div className="schedule">
                <label className="field">
                  <span>🌙 Тёмная с</span>
                  <input
                    className="input" type="time" value={settings.darkFrom} required
                    onChange={(e) => e.target.value && onChange({ ...settings, darkFrom: e.target.value })}
                  />
                </label>
                <label className="field">
                  <span>☀️ Светлая с</span>
                  <input
                    className="input" type="time" value={settings.lightFrom} required
                    onChange={(e) => e.target.value && onChange({ ...settings, lightFrom: e.target.value })}
                  />
                </label>
              </div>
              <p className="hint" style={{ margin: "10px 0 0" }}>
                {sameTime
                  ? "Время совпадает — тема всегда светлая."
                  : `Сейчас ${isDarkAt(settings, new Date()) ? "тёмная" : "светлая"}. Переключается сама по часам этого устройства.`}
              </p>
              {(settings.darkFrom !== "18:00" || settings.lightFrom !== "08:00") && (
                <button className="btn btn-ghost btn-sm" style={{ marginTop: 8 }}
                  onClick={() => onChange({ ...settings, darkFrom: "18:00", lightFrom: "08:00" })}>
                  Вернуть 18:00 – 08:00
                </button>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}
