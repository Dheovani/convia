import { useCallback, useEffect, useState } from "react";
import { Me as readMe, StorageProblem } from "../wailsjs/go/app/Bridge";
import { DataProblem, type Problem } from "./screens/DataProblem";
import { Workspace } from "./screens/Workspace";
import type { Identity } from "./screens/Me";
import { Welcome } from "./screens/Welcome";

type State =
  | { kind: "loading" }
  | { kind: "problem"; problem: Problem }
  | { kind: "welcome" }
  | { kind: "ready"; me: Identity };

async function load(): Promise<State> {
  try {
    const problem = await StorageProblem();
    if (problem !== "") {
      return { kind: "problem", problem: problem as Problem };
    }
    const me = await readMe();
    return me ? { kind: "ready", me } : { kind: "welcome" };
  } catch {
    return { kind: "problem", problem: "other" };
  }
}

export default function App() {
  const [state, setState] = useState<State>({ kind: "loading" });
  const refresh = useCallback(() => load().then(setState), []);

  useEffect(() => {
    load().then(setState);
  }, []);

  switch (state.kind) {
    case "loading":
      return null;
    case "problem":
      return <DataProblem problem={state.problem} onResolved={refresh} />;
    case "welcome":
      return <Welcome onReady={refresh} />;
    case "ready":
      return <Workspace me={state.me} onChanged={refresh} />;
  }
}
