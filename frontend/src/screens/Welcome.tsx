import { useState } from "react";
import { CreateIdentity } from "../../wailsjs/go/app/Bridge";
import { Button, Field } from "../components/controls";
import { ImportIdentity } from "../components/ImportIdentity";
import { Wordmark } from "../components/Mark";
import { useWords } from "../i18n/language";
import { refused } from "../i18n/refused";

// Welcome is the first run: making an identity, or bringing one exported elsewhere.
export function Welcome({ onReady }: { onReady: () => void }) {
  const words = useWords();
  const [name, setName] = useState("");
  const [importing, setImporting] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  async function create() {
    setProblem(null);
    try {
      await CreateIdentity(name);
      onReady();
    } catch (error) {
      setProblem(refused(words, error));
    }
  }

  return (
    <main className="flex h-full flex-col items-center justify-center gap-6 bg-surface px-8 text-ink">
      <Wordmark />
      {importing ? (
        <ImportIdentity
          onImported={onReady}
          onCancel={() => setImporting(false)}
        />
      ) : (
        <section className="flex w-full max-w-sm flex-col gap-3">
          <h1 className="font-display text-xl font-semibold">
            {words.welcome.title}
          </h1>
          <p className="text-ink-dim">{words.welcome.explanation}</p>
          <Field
            label={words.welcome.name}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
          <p className="text-xs text-ink-faint">{words.welcome.nameHint}</p>
          <Button tone="primary" disabled={!name.trim()} onClick={create}>
            {words.welcome.create}
          </Button>
          <Button onClick={() => setImporting(true)}>
            {words.welcome.haveOne}
          </Button>
          {problem && <p className="text-sm text-danger">{problem}</p>}
        </section>
      )}
    </main>
  );
}
