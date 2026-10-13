import { useState } from "react";
import {
  ChooseIdentityFile,
  ImportIdentity as importIdentity,
} from "../../wailsjs/go/app/Bridge";
import { useWords } from "../i18n/language";
import { refusalCode, refused } from "../i18n/refused";
import { Button, Field } from "./controls";

export function ImportIdentity({
  onImported,
  onCancel,
}: {
  onImported: () => void;
  onCancel?: () => void;
}) {
  const words = useWords();
  const [path, setPath] = useState("");
  const [password, setPassword] = useState("");
  const [needsConsent, setNeedsConsent] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function choose() {
    const chosen = await ChooseIdentityFile();
    if (chosen) {
      setPath(chosen);
    }
  }

  async function submit(replace: boolean) {
    setBusy(true);
    setProblem(null);
    try {
      await importIdentity(path, password, replace);
      onImported();
    } catch (error) {
      if (refusalCode(error) === "needs-consent") {
        setNeedsConsent(true);
      } else {
        setProblem(refused(words, error));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="flex w-full max-w-lg flex-col gap-3 text-left">
      <h2 className="font-display text-lg font-semibold">
        {words.importing.title}
      </h2>
      <div className="flex items-center gap-3">
        <Button onClick={choose}>{words.importing.chooseFile}</Button>
        <span className="truncate text-sm text-ink-dim">
          {path || words.importing.noFile}
        </span>
      </div>
      <Field
        label={words.importing.password}
        type="password"
        value={password}
        onChange={(event) => setPassword(event.target.value)}
      />
      {needsConsent ? (
        <>
          <p className="text-sm text-ink-dim">
            {words.importing.replaceWarning}
          </p>
          <Button tone="primary" disabled={busy} onClick={() => submit(true)}>
            {words.importing.replace}
          </Button>
        </>
      ) : (
        <Button
          tone="primary"
          disabled={busy || !path || !password}
          onClick={() => submit(false)}
        >
          {words.importing.import}
        </Button>
      )}
      {onCancel && <Button onClick={onCancel}>{words.importing.cancel}</Button>}
      {problem && <p className="text-sm text-danger">{problem}</p>}
    </section>
  );
}
