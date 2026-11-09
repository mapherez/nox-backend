import { useEffect, useState } from "react";
import { requestJSON } from "./MutationForm";

// Admission and CSRF are enforced by the same administrator adapter as the
// existing dashboard. This component never receives protocol credentials.
export function BackendPublication({ actorId }: { actorId: string }) {
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState(true);
  useEffect(() => {
    let active = true;
    setBusy(true);
    setError("");
    void requestJSON<{ enabled: boolean }>("/api/admin/suite/backend/publish", {
      method: "POST",
      headers: { "X-Nox-Request": "1" },
    })
      .catch((failure: unknown) => {
        if (active)
          setError(failure instanceof Error ? failure.message : "Backend tool publication failed.");
      })
      .finally(() => {
        if (active) setBusy(false);
      });
    return () => {
      active = false;
    };
  }, [actorId, attempt]);
  if (!error) return null;
  return (
    <aside className="suite-notice" aria-label="Backend tool publication">
      <p role="alert">Backend tools could not be published to Suite: {error}</p>
      <button type="button" disabled={busy} onClick={() => setAttempt((value) => value + 1)}>
        Retry Backend tool publication
      </button>
    </aside>
  );
}
