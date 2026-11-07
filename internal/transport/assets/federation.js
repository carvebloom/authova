// Navigation begins after a successful, same-origin, CSRF-protected POST response.
// A link remains available when JavaScript is disabled.
const destination = document.getElementById('identity-provider-link')
if (destination instanceof HTMLAnchorElement) window.location.replace(destination.href)
