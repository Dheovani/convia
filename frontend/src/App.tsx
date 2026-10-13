import { useEffect, useState } from "react";
import { StorageProblem } from "../wailsjs/go/app/Bridge";
import { Wordmark } from "./components/Mark";
import { DataProblem, type Problem } from "./screens/DataProblem";

export default function App() {
  const [problem, setProblem] = useState<Problem | "" | null>(null);

  useEffect(() => {
    StorageProblem().then(
      (found) => setProblem(found as Problem | ""),
      () => setProblem("other"),
    );
  }, []);

  if (problem === null) {
    return null;
  }

  if (problem !== "") {
    return <DataProblem problem={problem} onResolved={() => setProblem("")} />;
  }

  return (
    <main className="flex h-full items-center justify-center bg-surface">
      <Wordmark />
    </main>
  );
}
