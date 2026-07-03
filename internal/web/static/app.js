// Reload on bfcache restore (Safari) so an opened-then-read entry isn't shown
// as still-unread when the user navigates Back. Paired with Cache-Control:
// no-store on dynamic HTML. Kept as an external file so the page CSP can use
// script-src 'self' with no inline-script allowance.
addEventListener("pageshow", function (e) {
  if (e.persisted) location.reload();
});
