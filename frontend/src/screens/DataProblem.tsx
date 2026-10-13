import { useState } from "react";
import { StartOver } from "../../wailsjs/go/app/Bridge";
import { Button } from "../components/controls";
import { useWords } from "../i18n/language";

export type Problem = "newer" | "damaged" | "key-unreadable" | "other";

// DataProblem stops Convia when its data cannot be opened, and lets the person choose what to do (ADR 0017).
export function DataProblem({
  problem,
  onResolved,
}: {
  problem: Problem;
  onResolved: () => void;
}) {
  const words = useWords().dataProblem;
  const [failed, setFailed] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function startOver() {
    setBusy(true);
    setFailed(null);
    try {
      await StartOver();
      onResolved();
    } catch (error) {
      setFailed(error instanceof Error ? error.message : String(error));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main
      role="alert"
      className="flex h-full flex-col items-center justify-center gap-4 bg-surface px-8 text-center text-ink"
    >
      <h1 className="font-display text-xl font-semibold">
        {words.title[problem]}
      </h1>
      <p className="max-w-prose text-ink-dim">{words.explanation[problem]}</p>
      {problem !== "newer" && (
        <>
          <p className="max-w-prose text-ink-dim">{words.keptAside}</p>
          <Button tone="primary" disabled={busy} onClick={startOver}>
            {words.startOver}
          </Button>
        </>
      )}
      {failed && <p className="text-danger">{failed}</p>}
    </main>
  );
}
