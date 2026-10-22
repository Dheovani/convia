import { type ReactNode, useEffect, useId, useRef } from "react";
import { useWords } from "../i18n/language";
import { Button } from "./controls";

// ConfirmDialog asks before something that cannot be undone, saying what it does, with any choice it offers.
export function ConfirmDialog({
  title,
  explanation,
  confirm,
  onConfirm,
  onClose,
  children,
}: {
  title: string;
  explanation: string;
  confirm: string;
  onConfirm: () => void;
  onClose: () => void;
  children?: ReactNode;
}) {
  const words = useWords();
  const titleId = useId();
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
      aria-labelledby={titleId}
      className="m-auto flex max-w-lg flex-col gap-3 rounded-md border border-line bg-surface-raised p-5 text-ink shadow-raised backdrop:bg-black/50"
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      <h2 id={titleId} className="font-display text-lg font-semibold">
        {title}
      </h2>
      <p className="text-sm text-ink-dim">{explanation}</p>
      {children}
      <div className="flex justify-end gap-2 whitespace-nowrap">
        <Button autoFocus onClick={onClose}>
          {words.spaces.cancel}
        </Button>
        <Button tone="primary" onClick={onConfirm}>
          {confirm}
        </Button>
      </div>
    </dialog>
  );
}
