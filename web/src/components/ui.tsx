import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";

// ---------- уведомления ----------

type Toast = { text: string; error?: boolean };
const ToastCtx = createContext<(text: string, error?: boolean) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null);
  const timer = useRef<number>(0);
  const show = useCallback((text: string, error = false) => {
    setToast({ text, error });
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setToast(null), error ? 5000 : 2800);
  }, []);
  return (
    <ToastCtx.Provider value={show}>
      {children}
      {toast && (
        <div className={"toast" + (toast.error ? " error" : "")} role={toast.error ? "alert" : "status"}>
          {toast.text}
        </div>
      )}
    </ToastCtx.Provider>
  );
}

export const useToast = () => useContext(ToastCtx);

// ---------- модальное окно ----------

export function Modal({ title, onClose, children, labelledBy = "dialog-title" }: {
  title: string; onClose: () => void; children: ReactNode; labelledBy?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    const first = ref.current?.querySelector<HTMLElement>("input, select, textarea, button");
    first?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
      prev?.focus();
    };
  }, [onClose]);
  return (
    <div className="backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="dialog" role="dialog" aria-modal="true" aria-labelledby={labelledBy} ref={ref}>
        <h2 id={labelledBy}>{title}</h2>
        {children}
      </div>
    </div>
  );
}

// ---------- подтверждение ----------

export function Confirm({ title, text, confirm, danger, onConfirm, onClose }: {
  title: string; text: ReactNode; confirm: string; danger?: boolean;
  onConfirm: () => Promise<void> | void; onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  return (
    <Modal title={title} onClose={onClose} labelledBy="confirm-title">
      <p style={{ color: "var(--text-2)", margin: 0 }}>{text}</p>
      <div className="dialog-actions">
        <button className="btn" onClick={onClose} disabled={busy}>Отмена</button>
        <button
          className={"btn " + (danger ? "btn-danger-solid" : "btn-primary")}
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try { await onConfirm(); } finally { setBusy(false); }
          }}
        >
          {confirm}
        </button>
      </div>
    </Modal>
  );
}

export function Spinner({ text }: { text?: string }) {
  return (
    <div className="empty-state" role="status">
      <div className="spinner" aria-hidden="true" />
      {text && <p>{text}</p>}
    </div>
  );
}
