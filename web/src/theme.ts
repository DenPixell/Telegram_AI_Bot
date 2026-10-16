import { useCallback, useEffect, useState } from "react";

// Темы: светлая, тёмная или авто по часам зрителя (по умолчанию тёмная с 18:00 до 08:00).
// Выбор хранится в браузере; без localStorage (приватный режим) всё работает по умолчанию.
// Логика resolve повторяется во встроенном скрипте index.html, чтобы не было вспышки.

export type Mode = "light" | "dark" | "auto";
export type Theme = "light" | "dark";

export interface ThemeSettings {
  mode: Mode;
  darkFrom: string; // "HH:MM" - с какого времени тёмная
  lightFrom: string; // "HH:MM" - с какого времени светлая
}

export const defaultSettings: ThemeSettings = { mode: "auto", darkFrom: "18:00", lightFrom: "08:00" };

const storageKey = "panel.theme";
const timeRe = /^([01]\d|2[0-3]):[0-5]\d$/;

export function loadSettings(): ThemeSettings {
  try {
    const raw = JSON.parse(localStorage.getItem(storageKey) ?? "{}") as Partial<ThemeSettings>;
    return {
      mode: raw.mode === "light" || raw.mode === "dark" || raw.mode === "auto" ? raw.mode : defaultSettings.mode,
      darkFrom: raw.darkFrom && timeRe.test(raw.darkFrom) ? raw.darkFrom : defaultSettings.darkFrom,
      lightFrom: raw.lightFrom && timeRe.test(raw.lightFrom) ? raw.lightFrom : defaultSettings.lightFrom,
    };
  } catch {
    return defaultSettings;
  }
}

function saveSettings(s: ThemeSettings) {
  try {
    localStorage.setItem(storageKey, JSON.stringify(s));
  } catch {
    // нет доступа к хранилищу - выбор живёт до перезагрузки
  }
}

const minutes = (t: string) => {
  const [h, m] = t.split(":").map(Number);
  return h * 60 + m;
};

// isDarkAt: тёмный интервал [darkFrom, lightFrom) может переходить через полночь.
export function isDarkAt(s: ThemeSettings, at: Date): boolean {
  const t = at.getHours() * 60 + at.getMinutes();
  const d = minutes(s.darkFrom);
  const l = minutes(s.lightFrom);
  if (d === l) return false;
  return d > l ? t >= d || t < l : t >= d && t < l;
}

export function resolveTheme(s: ThemeSettings, at = new Date()): Theme {
  if (s.mode !== "auto") return s.mode;
  return isDarkAt(s, at) ? "dark" : "light";
}

function applyTheme(t: Theme) {
  document.documentElement.dataset.theme = t;
  const bg = getComputedStyle(document.documentElement).getPropertyValue("--bg").trim();
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", bg || (t === "dark" ? "#121211" : "#f6f5f1"));
}

export function useTheme() {
  const [settings, setSettingsState] = useState<ThemeSettings>(loadSettings);
  const [theme, setTheme] = useState<Theme>(() => resolveTheme(settings));

  const setSettings = useCallback((s: ThemeSettings) => {
    setSettingsState(s);
    saveSettings(s);
  }, []);

  useEffect(() => {
    const update = () => setTheme(resolveTheme(settings));
    update();
    // Авто-режим переключается сам на границе, без перезагрузки страницы.
    const timer = window.setInterval(update, 30_000);
    document.addEventListener("visibilitychange", update);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", update);
    };
  }, [settings]);

  useEffect(() => applyTheme(theme), [theme]);

  return { settings, setSettings, theme };
}
