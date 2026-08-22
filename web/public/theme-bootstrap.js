(function () {
  var value = localStorage.getItem("ch.theme");
  var theme = value === "light" || value === "dark" ? value : "system";
  var resolved = theme;
  if (theme === "system") {
    resolved = window.matchMedia("(prefers-color-scheme: dark)").matches
      ? "dark"
      : "light";
  }
  document.documentElement.dataset.theme = resolved;
  document.documentElement.style.colorScheme = resolved;
})();
