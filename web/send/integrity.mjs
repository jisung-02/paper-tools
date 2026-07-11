function hex(bytes) {
  return [...bytes].map((value) => value.toString(16).padStart(2, "0")).join("");
}

function fromHex(value) {
  if (typeof value !== "string" || !/^[0-9a-f]{64}$/i.test(value)) throw new Error("invalid SHA-256 digest");
  const out = new Uint8Array(32);
  for (let i = 0; i < out.length; i++) out[i] = Number.parseInt(value.slice(i * 2, i * 2 + 2), 16);
  return out;
}

export async function sha256Hex(value) {
  const bytes = value instanceof Uint8Array ? value : new Uint8Array(value);
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return hex(new Uint8Array(digest));
}

export async function transferChecksum(chunkDigests) {
  if (!Array.isArray(chunkDigests)) throw new TypeError("chunk digests must be an array");
  const bytes = new Uint8Array(chunkDigests.length * 32);
  chunkDigests.forEach((digest, index) => bytes.set(fromHex(digest), index * 32));
  return `PT-SHA256-v1:${await sha256Hex(bytes)}`;
}
