// Service worker for offline support. Bump CACHE to invalidate old entries
// on the next deploy.
const CACHE = "pt-v1";

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
          caches.open(CACHE).then((cache) => cache.put(req, copy));
          return res;
        })
        .catch(() => caches.match(req)),
    );
    return;
  }

  // Everything else (wasm, js, css, fonts, images): stale-while-revalidate,
  // so repeat visits are instant while the cache still refreshes in the
  // background.
  event.respondWith(
    caches.match(req).then((cached) => {
      const fetchAndUpdate = fetch(req).then((res) => {
        if (res.ok) caches.open(CACHE).then((cache) => cache.put(req, res.clone()));
        return res;
      });
      if (cached) {
        event.waitUntil(fetchAndUpdate);
        return cached;
      }
      return fetchAndUpdate;
    }),
  );
});
