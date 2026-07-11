let nextArtifactId = 1;
const contentIdentities = new WeakMap();
const contentIdentityChunkSize = 1024 * 1024;

function stable(value) {
  if (Array.isArray(value)) return `[${value.map(stable).join(",")}]`;
  if (value && typeof value === "object") {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${stable(value[key])}`).join(",")}}`;
  }
  return JSON.stringify(value);
}

function cacheIdentity({ id, revision, contentIdentity, size, kind }) {
  if (contentIdentity) return { contentIdentity, revision, size, kind };
  return { id, revision, size, kind };
}

export function contentIdentityForBlob(blob, subtle = globalThis.crypto?.subtle) {
  if (!(blob instanceof Blob)) throw new TypeError("content identity source must be a Blob");
  if (!subtle || typeof subtle.digest !== "function") throw new Error("Web Crypto SHA-256 is unavailable");
  let identity = contentIdentities.get(blob);
  if (!identity) {
    identity = (async () => {
      const chunks = [];
      for (let offset = 0; offset < blob.size; offset += contentIdentityChunkSize) {
        const bytes = await blob.slice(offset, offset + contentIdentityChunkSize).arrayBuffer();
        chunks.push(new Uint8Array(await subtle.digest("SHA-256", bytes)));
      }
      const header = new TextEncoder().encode(
        `paper-tools-blob-sha256-tree-v1\nsize:${blob.size}\nchunk:${contentIdentityChunkSize}\ncount:${chunks.length}\n`,
      );
      const manifestSize = chunks.reduce((size, chunk) => size + 4 + chunk.byteLength, header.byteLength);
      const manifest = new Uint8Array(manifestSize);
      const view = new DataView(manifest.buffer);
      manifest.set(header);
      let offset = header.byteLength;
      for (const chunk of chunks) {
        view.setUint32(offset, chunk.byteLength);
        offset += 4;
        manifest.set(chunk, offset);
        offset += chunk.byteLength;
      }
      const digest = await subtle.digest("SHA-256", manifest);
      const hex = [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
      return `sha256-tree-v1:${hex}`;
    })();
    contentIdentities.set(blob, identity);
  }
  return identity;
}

export function createArtifact(blob, options = {}) {
  if (!(blob instanceof Blob)) throw new TypeError("artifact data must be a Blob");
  const name = String(options.name || "output.bin");
  const kind = String(options.kind || "binary");
  const revision = options.revision ?? 0;
  if (!Number.isSafeInteger(revision) || revision < 0) throw new RangeError("invalid artifact revision");
  const contentIdentity = options.contentIdentity == null ? "" : String(options.contentIdentity);
  return Object.freeze({
    id: String(options.id || `artifact-${nextArtifactId++}`),
    revision,
    contentIdentity,
    name,
    kind,
    mime: String(options.mime || blob.type || "application/octet-stream"),
    size: blob.size,
    blob,
    metadata: Object.freeze({ ...(options.metadata || {}) }),
  });
}

export function artifactCacheKey(artifacts, params = {}, sidecars = {}) {
  if (!Array.isArray(artifacts)) throw new TypeError("artifacts must be an array");
  return stable({
    inputs: artifacts.map(cacheIdentity),
    params,
    sidecars: Object.fromEntries(Object.entries(sidecars).map(([name, value]) => [
      name,
      (Array.isArray(value) ? value : [value]).map(cacheIdentity),
    ])),
  });
}

export class ArtifactURL {
  constructor(blob, urlAPI = URL) {
    if (!(blob instanceof Blob)) throw new TypeError("object URL source must be a Blob");
    this.api = urlAPI;
    this.url = urlAPI.createObjectURL(blob);
    this.disposed = false;
  }

  dispose() {
    if (this.disposed) return;
    this.disposed = true;
    this.api.revokeObjectURL(this.url);
  }
}
