import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { Speaking } from "./i18n/language";
import { applyTheme, rememberedTheme } from "./state/preferences";
import "./styles/theme.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("the window has no #root to render Convia into");
}

// The theme is put on the document before anything is drawn, so nothing flashes in the other one.
applyTheme(rememberedTheme());

createRoot(root).render(
  <StrictMode>
    <Speaking>
      <App />
    </Speaking>
  </StrictMode>,
);
