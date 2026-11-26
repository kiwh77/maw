const THEMES = ["system", "light", "dark"];
function readTheme() {
  try {
    const value = localStorage.getItem("maw-theme");
    return THEMES.includes(value) ? value : "system";
  } catch (_) { return "system"; }
}
function setTheme(value) {
  const theme = THEMES.includes(value) ? value : "system";
  document.documentElement.dataset.theme = theme;
  try { localStorage.setItem("maw-theme", theme); } catch (_) {}
}
// Theme bootstrap: apply before the first paint, without persisting a default.
document.documentElement.dataset.theme = readTheme();
