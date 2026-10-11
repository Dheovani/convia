import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./index.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("the page has no #root to render Convia into");
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
