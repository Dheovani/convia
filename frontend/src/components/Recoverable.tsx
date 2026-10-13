import { Component, type ReactNode } from "react";
import { useWords } from "../i18n/language";
import { Button } from "./controls";

function Failed() {
  const words = useWords();
  return (
    <main
      role="alert"
      className="flex h-full flex-col items-center justify-center gap-4 bg-surface text-ink"
    >
      <p>{words.failure.message}</p>
      <Button tone="primary" onClick={() => window.location.reload()}>
        {words.failure.reload}
      </Button>
    </main>
  );
}

// Recoverable shows a way back instead of a blank window when what it wraps fails to render.
export class Recoverable extends Component<
  { children: ReactNode },
  { failed: boolean }
> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    return this.state.failed ? <Failed /> : this.props.children;
  }
}
