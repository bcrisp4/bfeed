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
