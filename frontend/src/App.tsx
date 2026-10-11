import { Wordmark } from "./components/Mark";

/*
App is the whole interface, which today is the mark on an empty window. What
the window shows next is built feature by feature, once the decisions in
ROADMAP.md M02 say what two peers do.
*/
export default function App() {
  return (
    <main className="flex h-full items-center justify-center bg-surface">
      <Wordmark />
    </main>
  );
}
