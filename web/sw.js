// Service worker for offline support. Bump CACHE to invalidate old entries
// on the next deploy.
const CACHE = "pt-v4";

self.addEventListener("install", (event) => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))),
    ).then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET" || new URL(req.url).origin !== self.location.origin) return;

  // Navigations: network-first, so users always get the latest HTML when
  // online, falling back to the cache when offline.
  if (req.mode === "navigate") {
    event.respondWith(
      fetch(req)
        .then((res) => {
          const copy = res.clone();
          event.waitUntil(caches.open(CACHE).then((cache) => cache.put(req, copy)).catch(() => {}));
          return res;
        })
        .catch(() => caches.match(req)),
    );
    return;
  }

  // Everything else (wasm, js, css, fonts, images): network-first, so
  // online users always get the latest asset, falling back to the cache
  // when offline. `cache: "no-cache"` forces a conditional revalidation
  // (ETag/304) with the origin, bypassing any still-fresh HTTP cache entry
  // stored under older (longer-lived) cache-control headers.
  event.respondWith(
    fetch(req, { cache: "no-cache" })
      .then((res) => {
        if (res.ok) {
          const copy = res.clone();
          event.waitUntil(caches.open(CACHE).then((cache) => cache.put(req, copy)).catch(() => {}));
        }
        return res;
      })
      .catch(() => caches.match(req)),
  );
});
