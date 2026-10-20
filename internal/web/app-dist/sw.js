const CACHE = "beamfall-placeholder-v1";
const SHELL = [
  "/web/",
  "/web/index.html",
  "/web/assets/placeholder.css",
  "/web/assets/placeholder.js",
  "/web/favicon.svg",
  "/web/manifest-icon.svg",
  "/web/manifest.webmanifest",
];

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(SHELL)));
});

self.addEventListener("fetch", () => {});
