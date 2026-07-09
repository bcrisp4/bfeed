// Reload on bfcache restore (Safari) so an opened-then-read entry isn't shown
// as still-unread when the user navigates Back. Paired with Cache-Control:
// no-store on dynamic HTML. Kept as an external file so the page CSP can use
// script-src 'self' with no inline-script allowance.
addEventListener("pageshow", function (e) {
  if (e.persisted) location.reload();
});

// Rewrite server-rendered timestamps into the viewer's local timezone with
// locale-aware formatting. Timestamps are stored and rendered as UTC; each
// <time> carries a machine-readable RFC3339 `datetime` attribute, so this is
// purely additive — a viewer with no JS still sees the (UTC) server render.
//
// Mirrors the server's humanizeSince: relative for the last 24h ("just now",
// "5m ago", "3h ago"), an absolute date beyond that — but computed in local
// time. The `title` tooltip always becomes the full local date+time.
function localizeTime(el) {
  if (el.dataset.tzLocalized) return; // idempotent across re-runs / htmx swaps
  var iso = el.getAttribute("datetime");
  if (!iso) return;
  var t = new Date(iso);
  if (isNaN(t.getTime())) return;
  el.dataset.tzLocalized = "1";

  el.title = t.toLocaleString(undefined, {
    day: "numeric",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });

  var d = Date.now() - t.getTime();
  var min = 60000,
    hour = 60 * min,
    day = 24 * hour;
  var text;
  if (d < min) {
    text = "just now";
  } else if (d < hour) {
    text = Math.floor(d / min) + "m ago";
  } else if (d < day) {
    text = Math.floor(d / hour) + "h ago";
  } else {
    text = t.toLocaleDateString(undefined, {
      day: "numeric",
      month: "short",
      year: "numeric",
    });
  }
  el.textContent = text;
}

function localizeTimes(root) {
  root = root || document;
  // querySelectorAll matches descendants only, so also localize the root itself
  // when an htmx swap makes a bare <time> the top-level node (document has no
  // .matches, so guard it).
  if (root.matches && root.matches("time[datetime]")) localizeTime(root);
  root.querySelectorAll("time[datetime]").forEach(localizeTime);
}

addEventListener("DOMContentLoaded", function () {
  localizeTimes(document);
});
// htmx fires htmx:load on every node it swaps in (initial + "load more" rows,
// row fragments, oob swaps), so newly-rendered times localize too.
addEventListener("htmx:load", function (e) {
  localizeTimes(e.target);
});

// Pull-to-refresh for the installed PWA (issue #112). iOS standalone mode
// freezes the page (no way to load newly-polled entries) and disables Safari's
// own pull-to-refresh, so we supply a custom gesture: pull down from the top,
// release past a threshold, and reload. Dynamic HTML is served no-store, so a
// reload always refetches the latest list.
//
// Gated to standalone display so we never double up with the browser's native
// pull-to-refresh in a normal mobile tab.
(function () {
  var standalone =
    (window.matchMedia &&
      window.matchMedia("(display-mode: standalone)").matches) ||
    navigator.standalone === true;
  if (!standalone) return;

  var THRESHOLD = 70; // px of pull (post-damping) needed to trigger a reload
  var MAX = 90; // px cap on how far the indicator travels
  var DAMP = 0.5; // resistance: raw finger travel is halved

  var reduceMotion =
    window.matchMedia &&
    window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  var indicator = null;
  function ensureIndicator() {
    if (indicator) return indicator;
    indicator = document.createElement("div");
    indicator.className = "ptr";
    indicator.setAttribute("aria-hidden", "true");
    var spinner = document.createElement("div");
    spinner.className = "ptr-spinner";
    indicator.appendChild(spinner);
    document.body.appendChild(indicator);
    return indicator;
  }

  var startY = 0;
  var armed = false; // touch began at the top of the page
  var pulling = false; // we've committed to a pull and are moving the indicator
  var refreshing = false; // reload triggered; ignore further input

  function reset() {
    armed = false;
    pulling = false;
    if (indicator) {
      indicator.classList.remove("ptr-pulling", "ptr-ready");
      indicator.style.transform = "";
    }
  }

  addEventListener(
    "touchstart",
    function (e) {
      if (refreshing || e.touches.length !== 1) {
        armed = false;
        return;
      }
      // Only arm at the very top; otherwise this is a normal scroll.
      armed = window.scrollY <= 0;
      startY = e.touches[0].clientY;
      pulling = false;
    },
    { passive: true }
  );

  addEventListener(
    "touchmove",
    function (e) {
      if (!armed || refreshing) return;
      var delta = e.touches[0].clientY - startY;
      // A scroll started at the top but going up, or the page scrolled away
      // from the top mid-gesture, cancels the pull.
      if (delta <= 0 || window.scrollY > 0) {
        if (pulling) reset();
        armed = false;
        return;
      }
      var d = Math.min(delta * DAMP, MAX);
      if (!pulling && d < 6) return; // tiny move: leave native scroll alone
      pulling = true;
      // We own this gesture now — stop the page from scrolling/rubber-banding.
      e.preventDefault();
      var el = ensureIndicator();
      el.classList.add("ptr-pulling");
      if (!reduceMotion) el.style.transform = "translateY(" + d + "px)";
      el.classList.toggle("ptr-ready", d >= THRESHOLD);
    },
    { passive: false }
  );

  function end() {
    if (refreshing || !pulling) {
      reset();
      return;
    }
    var el = ensureIndicator();
    if (el.classList.contains("ptr-ready")) {
      refreshing = true;
      el.classList.remove("ptr-pulling");
      el.classList.add("ptr-active");
      location.reload();
    } else {
      reset();
    }
  }
  addEventListener("touchend", end, { passive: true });
  addEventListener("touchcancel", end, { passive: true });
})();
