"use strict";
/* theme.js — must run before first paint (loaded in <head> before the
   stylesheet). If the visitor has made an explicit choice, stamp it onto
   <html> so style.css's `:root[data-theme]` rules apply immediately and
   there's no flash of the wrong theme. Nothing stored means no opinion:
   the `prefers-color-scheme` media query in style.css decides instead. */
(function () {
  try {
    var theme = localStorage.getItem("theme");
    if (theme === "light" || theme === "dark") {
      document.documentElement.setAttribute("data-theme", theme);
    }
  } catch (e) {
    /* localStorage unavailable (e.g. private mode) — fall back to OS pref */
  }
})();
