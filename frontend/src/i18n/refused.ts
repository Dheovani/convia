import type { Words } from "./en";

// refusalCode is the stable code the core sent with a refusal.
export function refusalCode(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

// refused says, in the interface's language, what the core refused.
export function refused(words: Words, error: unknown): string {
  return (
    (words.refusals as Record<string, string>)[refusalCode(error)] ??
    words.refusals.unknown
  );
}
