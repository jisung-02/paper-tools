// Service worker for offline support. Bump CACHE to invalidate old entries
// on the next deploy.
const CACHE = "pt-v5";

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

// Deploy-hashed immutable names (e.g. /pdf-4ba2f15599b0.mjs), tesseract
// traineddata, gzipped wasm, and vendored libraries never change without
// also changing their URL, so they're safe to serve cache-first.
function isImmutableAsset(pathname) {
  return /-[0-9a-f]{12}\./.test(pathname)
    || pathname.endsWith(".traineddata")
    || pathname.endsWith(".wasm.gz")
    || pathname.startsWith("/vendor/");
}

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

  // Immutable assets: cache-first with background revalidation. These are
  // multi-MB binaries (Typst's ~10 MB wasm.gz, tesseract traineddata,
  // vendored pdf.js) that never change under a given URL, so re-validating
  // them over the network on every use — as the network-first path below
  // does — just re-downloads them for no benefit. Respond from the cache
  // immediately when present, and refresh the cache in the background so a
  // future deploy under a new hashed name still gets picked up eventually.
  if (isImmutableAsset(new URL(req.url).pathname)) {
    event.respondWith(
      caches.match(req).then((cached) => {
        const revalidate = fetch(req)
          .then((res) => {
            if (res.ok) {
              const copy = res.clone();
              event.waitUntil(caches.open(CACHE).then((cache) => cache.put(req, copy)).catch(() => {}));
            }
            return res;
          })
          .catch(() => cached);
        if (cached) {
          event.waitUntil(revalidate.catch(() => {}));
          return cached;
        }
        return revalidate;
      }),
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
