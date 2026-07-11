import assert from "node:assert/strict";
import { test } from "node:test";
import * as artifactModule from "./artifact.mjs";

const { createArtifact, artifactCacheKey, ArtifactURL } = artifactModule;

test("artifact keeps immutable blob metadata and revision", () => {
  const blob = new Blob(["pdf"], { type: "application/pdf" });
  const artifact = createArtifact(blob, { name: "a.pdf", kind: "pdf", revision: 2, id: "a" });
  assert.deepEqual({ id: artifact.id, name: artifact.name, kind: artifact.kind, size: artifact.size, revision: artifact.revision },
    { id: "a", name: "a.pdf", kind: "pdf", size: 3, revision: 2 });
  assert.equal(Object.isFrozen(artifact), true);
  assert.throws(() => { artifact.name = "changed"; }, TypeError);
});

test("artifact cache key changes with revision and params", () => {
  const a = createArtifact(new Blob(["a"]), { id: "x", name: "a.pdf", kind: "pdf", revision: 1 });
  const b = createArtifact(new Blob(["a"]), { id: "x", name: "a.pdf", kind: "pdf", revision: 2 });
  assert.notEqual(artifactCacheKey([a], { quality: 1 }), artifactCacheKey([b], { quality: 1 }));
  assert.notEqual(artifactCacheKey([a], { quality: 1 }), artifactCacheKey([a], { quality: 2 }));
});

test("artifact cache key distinguishes content with identical revision metadata", () => {
  const a = createArtifact(new Blob(["a"]), {
    id: "input-0", name: "input.pdf", kind: "pdf", revision: 1, contentIdentity: "sha256:first",
  });
  const b = createArtifact(new Blob(["b"]), {
    id: "input-0", name: "input.pdf", kind: "pdf", revision: 1, contentIdentity: "sha256:second",
  });

  assert.notEqual(artifactCacheKey([a]), artifactCacheKey([b]));
});

test("content identity cache keys still honor logical artifact revisions", () => {
  const a = createArtifact(new Blob(["same"]), {
    id: "input-0", name: "first.pdf", kind: "pdf", revision: 10, contentIdentity: "sha256:same",
  });
  const b = createArtifact(new Blob(["same"]), {
    id: "input-0", name: "second.pdf", kind: "pdf", revision: 20, contentIdentity: "sha256:same",
  });

  assert.notEqual(artifactCacheKey([a]), artifactCacheKey([b]));
  assert.throws(() => { a.contentIdentity = "sha256:changed"; }, TypeError);
});

test("blob content identity memoizes a chunked SHA-256 identity per input", async () => {
  assert.equal(typeof artifactModule.contentIdentityForBlob, "function");
  let calls = 0;
  const subtle = {
    async digest(algorithm, bytes) {
      calls++;
      assert.equal(algorithm, "SHA-256");
      if (calls === 1) {
        assert.deepEqual([...new Uint8Array(bytes)], [112, 100, 102]);
        return new Uint8Array(32).fill(0xab).buffer;
      }
      assert.ok(bytes.byteLength > 32);
      return Uint8Array.from([0xcd, 0x02]).buffer;
    },
  };
  const blob = new Blob(["pdf"]);

  const [first, second] = await Promise.all([
    artifactModule.contentIdentityForBlob(blob, subtle),
    artifactModule.contentIdentityForBlob(blob, subtle),
  ]);

  assert.equal(first, "sha256-tree-v1:cd02");
  assert.equal(second, first);
  assert.equal(calls, 2);
});

test("blob content identity reads fixed-size slices instead of the full Blob", async () => {
  const chunkSize = 1024 * 1024;
  class TrackedBlob extends Blob {
    fullReads = 0;
    chunkReads = [];

    async arrayBuffer() {
      this.fullReads++;
      return super.arrayBuffer();
    }

    slice(start, end, type) {
      const chunk = super.slice(start, end, type);
      const read = chunk.arrayBuffer.bind(chunk);
      Object.defineProperty(chunk, "arrayBuffer", {
        value: async () => {
          this.chunkReads.push(chunk.size);
          return read();
        },
      });
      return chunk;
    }
  }
  const blob = new TrackedBlob([new Uint8Array(chunkSize * 2 + 17)]);
  let digestCalls = 0;
  const subtle = {
    async digest() {
      digestCalls++;
      return new Uint8Array(32).fill(digestCalls).buffer;
    },
  };

  const identity = await artifactModule.contentIdentityForBlob(blob, subtle);

  assert.equal(blob.fullReads, 0);
  assert.deepEqual(blob.chunkReads, [chunkSize, chunkSize, 17]);
  assert.equal(digestCalls, 4);
  assert.equal(identity, `sha256-tree-v1:${"04".repeat(32)}`);
});

test("artifact cache key is deterministic for params and named sidecars", () => {
  const input = createArtifact(new Blob(["pdf"]), { name: "input.pdf", kind: "pdf", contentIdentity: "sha256:input" });
  const image = createArtifact(new Blob(["png"]), { name: "image.png", kind: "image", contentIdentity: "sha256:image" });
  const font = createArtifact(new Blob(["ttf"]), { name: "font.ttf", kind: "font", contentIdentity: "sha256:font" });

  const first = artifactCacheKey([input], { opacity: 0.5, position: "center" }, { image, font });
  const second = artifactCacheKey([input], { position: "center", opacity: 0.5 }, { font, image });

  assert.equal(first, second);
});

test("ArtifactURL revokes an object URL exactly once", () => {
  const calls = [];
  const urls = new ArtifactURL(new Blob(["x"]), {
    createObjectURL: () => "blob:test",
    revokeObjectURL: (url) => calls.push(url),
  });
  assert.equal(urls.url, "blob:test");
  urls.dispose();
  urls.dispose();
  assert.deepEqual(calls, ["blob:test"]);
});
