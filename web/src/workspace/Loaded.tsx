// Loading one thing from the daemon (WorkspacePanel.swift's Loaded and
// EmptyNote): it, a spinner, or what went wrong with Try again.

import { useEffect, useState, type ReactNode } from "react";

export function Spinner() {
  return <span className="spinner" aria-label="Loading" />;
}

export function Loaded<T>({ load, deps, children }: { load: () => Promise<T>; deps: unknown[]; children: (value: T) => ReactNode }) {
  const [value, setValue] = useState<{ v: T } | null>(null);
  const [problem, setProblem] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let live = true;
    setValue(null);
    setProblem(null);
    load().then(
      (v) => live && setValue({ v }),
      (err) => live && setProblem(err instanceof Error ? err.message : String(err)),
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [attempt, ...deps]);

  if (value) return <>{children(value.v)}</>;
  if (problem !== null) {
    return (
      <div className="loaded-problem">
        <span>{problem}</span>
        <button className="push-button small" onClick={() => setAttempt((n) => n + 1)}>Try again</button>
      </div>
    );
  }
  return <div className="loaded-wait"><Spinner /></div>;
}

export function EmptyNote({ text }: { text: string }) {
  return <div className="empty-note">{text}</div>;
}
