import { SaveFile } from "../../wailsjs/go/app/Bridge";
import { useLanguage, useWords } from "../i18n/language";
import { Button } from "./controls";

export interface FileInfo {
  size: number;
  type: string;
  held: number;
  complete: boolean;
  picture: boolean;
  orientation: number;
  url?: string;
}

/*
The turns and mirrors of a picture's EXIF orientation, which was kept when its metadata was removed
(ADR 0036), so the picture still stands as it was taken.
*/
const turns: Record<number, string> = {
  2: "scaleX(-1)",
  3: "rotate(180deg)",
  4: "scaleY(-1)",
  5: "rotate(90deg) scaleX(-1)",
  6: "rotate(90deg)",
  7: "rotate(270deg) scaleX(-1)",
  8: "rotate(270deg)",
};

// Attachment is a file in a conversation: a picture shown, or anything else offered to be saved.
export function Attachment({
  id,
  name,
  file,
  mine,
  from,
  onError,
}: {
  id: string;
  name: string;
  file: FileInfo;
  mine: boolean;
  from: string;
  onError: (error: unknown) => void;
}) {
  const words = useWords();
  const { formatting } = useLanguage();
  const size = new Intl.NumberFormat(formatting, {
    style: "unit",
    unit: file.size >= 1 << 20 ? "megabyte" : "kilobyte",
    maximumFractionDigits: 1,
  }).format(file.size / (file.size >= 1 << 20 ? 1 << 20 : 1 << 10));

  const save = () => void SaveFile(id, name).catch(onError);

  if (file.picture && file.url) {
    return (
      <figure className="flex flex-col items-start gap-1">
        <img
          src={file.url}
          alt={name}
          className="max-h-80 max-w-sm rounded-sm object-contain"
          style={{
            transform: turns[file.orientation],
            imageOrientation: "none",
          }}
        />
        <Button size="small" onClick={save}>
          {words.files.save}
        </Button>
      </figure>
    );
  }

  const state = file.complete
    ? null
    : mine
      ? words.files.sending
      : file.held === 0
        ? words.files.waiting(from)
        : words.files.receiving(Math.floor((100 * file.held) / file.size));

  return (
    <div className="flex max-w-sm items-center gap-3 rounded-md border border-line bg-surface-raised p-3">
      <span aria-hidden="true" className="text-2xl">
        📄
      </span>
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-sm font-medium">{name}</span>
        <span className="text-[0.75rem] text-ink-faint">
          {size}
          {state && ` · ${state}`}
        </span>
        {!file.complete && !mine && file.held > 0 && (
          <progress
            className="mt-1 h-1 w-full"
            max={file.size}
            value={file.held}
            aria-label={words.files.progress(name)}
          />
        )}
      </div>
      {file.complete && (
        <Button size="small" onClick={save}>
          {words.files.save}
        </Button>
      )}
    </div>
  );
}
