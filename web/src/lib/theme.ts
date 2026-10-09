/**
 * Light and dark. The theme is a `dark` class on <html>, which is what the
 * `dark:` variant in globals.css and the shadcn components key on.
 *
 * A visitor who has never chosen follows the system, live; one click on the
 * header toggle pins a choice in localStorage. The script below runs in <head>
 * before the first paint — the export is static, so the server cannot know the
 * preference, and setting the class from an effect would flash white first.
 */
export const THEME_KEY = "theme";

export const THEME_SCRIPT = `(function(){try{
var d=document.documentElement,m=window.matchMedia("(prefers-color-scheme: dark)");
function apply(){var t=localStorage.getItem(${JSON.stringify(THEME_KEY)});
d.classList.toggle("dark",t?t==="dark":m.matches);d.style.colorScheme=d.classList.contains("dark")?"dark":"light"}
apply();m.addEventListener("change",apply);
}catch(e){}})()`;

/** Flip the theme and remember the choice. */
export function toggleTheme() {
  const d = document.documentElement;
  const dark = !d.classList.contains("dark");
  d.classList.toggle("dark", dark);
  d.style.colorScheme = dark ? "dark" : "light";
  try {
    localStorage.setItem(THEME_KEY, dark ? "dark" : "light");
  } catch {
    // storage blocked: the switch still works for this page view
  }
}
