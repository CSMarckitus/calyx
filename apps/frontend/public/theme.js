// Sets the theme before the first paint. A file of its own, because the CSP blocks inline scripts.
try {
  const theme = localStorage.getItem("calyx-theme");
  if (theme === "light" || theme === "dark") {
    document.documentElement.dataset.theme = theme;
  }
} catch {
  // Storage is blocked: the theme of the system stays.
}
