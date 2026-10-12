export type Theme = "dark" | "light";

const KEY = "listing-tracker-theme";

/** The viewer's choice; dark when none was saved or storage is unavailable. */
export function savedTheme(): Theme {
  try {
    return localStorage.getItem(KEY) === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}

export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
  try {
    localStorage.setItem(KEY, theme);
  } catch {
    // The screen works without remembering the choice.
  }
}
