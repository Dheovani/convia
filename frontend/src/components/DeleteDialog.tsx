import { useEffect, useId, useRef } from "react";
import { useWords } from "../i18n/language";
import { Button } from "./controls";

/*
DeleteDialog asks how a message is deleted (ADR 0034): one's own for both, or only here once it was
delivered; somebody else's only here.
*/
export function DeleteDialog({
  mine,
  delivered,
  onBoth,
  onHere,
  onClose,
}: {
  mine: boolean;
  delivered: boolean;
  onBoth: () => void;
  onHere: () => void;
  onClose: () => void;
}) {
  const words = useWords();
  const title = useId();
  const dialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const d = dialog.current;
    if (!d || d.open)
      return;

    // Where showModal is missing, as in tests, the dialog opens all the same.
    if (typeof d.showModal === "function")
      d.showModal();
    else
      d.setAttribute("open", "");
  }, []);

  const explanation = !mine
    ? words.deleting.theirs
    : delivered
      ? words.deleting.mine
      : words.deleting.waiting;

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
        {words.deleting.title}
      </h2>
      <p className="text-sm text-ink-dim">{explanation}</p>
      <div className="flex flex-wrap justify-end gap-2">
        <Button autoFocus onClick={onClose}>
          {words.deleting.cancel}
        </Button>
        {(!mine || delivered) && (
          <Button onClick={onHere}>{words.deleting.here}</Button>
        )}
        {mine && (
          <Button tone="primary" onClick={onBoth}>
            {words.deleting.both}
          </Button>
        )}
      </div>
    </dialog>
  );
}
