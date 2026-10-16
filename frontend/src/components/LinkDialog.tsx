import { useEffect, useId, useRef } from "react";
import { OpenLink } from "../../wailsjs/go/app/Bridge";
import { useWords } from "../i18n/language";
import { Button } from "./controls";

// LinkDialog asks before a link leaves Convia for the browser, showing the whole address (ADR 0030).
export function LinkDialog({
  url,
  onClose,
}: {
  url: string;
  onClose: () => void;
}) {
  const words = useWords();
  const title = useId();
  const dialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const d = dialog.current;
    if (!d || d.open) return;

    // Where showModal is missing, as in tests, the dialog opens all the same.
    if (typeof d.showModal === "function") d.showModal();
    else d.setAttribute("open", "");
  }, []);

  return (
    <dialog
      ref={dialog}
      aria-labelledby={title}
      className="m-auto flex max-w-md flex-col gap-3 rounded-md border border-line bg-surface-raised p-5 text-ink shadow-raised backdrop:bg-black/50"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <h2 id={title} className="font-display text-lg font-semibold">
        {words.link.title}
      </h2>
      <code className="rounded-sm bg-surface-sunken p-2 font-mono text-sm break-all">
        {url}
      </code>
      <p className="text-sm text-ink-dim">{words.link.explanation}</p>
      <div className="flex justify-end gap-2">
        <Button autoFocus onClick={onClose}>
          {words.link.cancel}
        </Button>
        <Button
          tone="primary"
          onClick={() => {
            void OpenLink(url);
            onClose();
          }}
        >
          {words.link.open}
        </Button>
      </div>
    </dialog>
  );
}
