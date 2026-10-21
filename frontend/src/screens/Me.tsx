import { useState } from "react";
import {
  DeleteIdentity,
  ExportIdentity,
  RenameIdentity,
} from "../../wailsjs/go/app/Bridge";
import { Button, Field } from "../components/controls";
import { ImportIdentity } from "../components/ImportIdentity";
import { useWords } from "../i18n/language";
import { refused } from "../i18n/refused";

export interface Identity {
  name: string;
  publicKey: string;
}

// grouped writes a key in groups of four, so a person can read it aloud or compare it.
export function grouped(key: string): string {
  return key.match(/.{1,4}/g)?.join(" ") ?? key;
}

// Me is the person's own identity: their name, their key, and what they can do with them.
export function Me({ me, onChanged }: { me: Identity; onChanged: () => void }) {
  const words = useWords();
  const [name, setName] = useState(me.name);
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [understood, setUnderstood] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  async function attempt(work: () => Promise<unknown>) {
    setNotice(null);
    try {
      await work();
      onChanged();
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  async function exportIdentity() {
    if (password !== again) {
      setNotice(words.me.mismatch);
      return;
    }

    setNotice(null);

    try {
      if (await ExportIdentity(password)) {
        setNotice(words.me.exported);
        setPassword("");
        setAgain("");
      }
    } catch (error) {
      setNotice(refused(words, error));
    }
  }

  return (
    <div className="flex w-full flex-col items-center gap-8">
      <section className="flex w-full max-w-lg flex-col gap-3">
        <div className="flex items-end gap-3">
          <div className="flex-1">
            <Field
              label={words.me.name}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <Button
            disabled={name.trim() === me.name}
            onClick={() => attempt(() => RenameIdentity(name))}
          >
            {words.me.save}
          </Button>
        </div>
        <h2 className="text-sm font-medium text-ink-dim">
          {words.me.publicKey}
        </h2>
        <code className="rounded-md bg-surface-raised p-3 font-mono text-sm break-words">
          {grouped(me.publicKey)}
        </code>
        <p className="text-xs text-ink-faint">{words.me.publicKeyHint}</p>
      </section>

      <section className="flex w-full max-w-lg flex-col gap-3">
        <h2 className="font-display text-lg font-semibold">
          {words.me.exportTitle}
        </h2>
        <p className="text-sm text-ink-dim">{words.me.exportExplanation}</p>
        <Field
          label={words.me.password}
          type="password"
          value={password}
          onChange={(event) => setPassword(event.target.value)}
        />
        <Field
          label={words.me.confirmPassword}
          type="password"
          value={again}
          onChange={(event) => setAgain(event.target.value)}
        />
        <Button tone="primary" disabled={!password} onClick={exportIdentity}>
          {words.me.export}
        </Button>
      </section>

      <ImportIdentity onImported={onChanged} />

      <section className="flex w-full max-w-lg flex-col gap-3">
        <h2 className="font-display text-lg font-semibold">
          {words.me.deleteTitle}
        </h2>
        <p className="text-sm text-ink-dim">{words.me.deleteExplanation}</p>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={understood}
            onChange={(event) => setUnderstood(event.target.checked)}
          />
          {words.me.deleteConfirm}
        </label>
        <Button disabled={!understood} onClick={() => attempt(DeleteIdentity)}>
          {words.me.delete}
        </Button>
      </section>

      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
    </div>
  );
}
