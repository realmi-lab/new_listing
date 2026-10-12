import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "pretendard/dist/web/variable/pretendardvariable-dynamic-subset.css";
import "./theme.css";
import "./listing.css";
import App from "./App";
import { applyTheme, savedTheme } from "./theme";

applyTheme(savedTheme());

createRoot(document.getElementById("root")!).render(<StrictMode><App /></StrictMode>);
