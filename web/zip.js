"use strict";

/* web/zip.js — dependency-free STORE-mode (uncompressed) ZIP writer, shared
   by every classic tool page that needs to bundle multiple outputs into one
   download (Compress, Image Convert). Loaded via a plain <script> tag, so
   this file stays free of import/export syntax; it works equally as a
   classic script, as an ES module side-effect import, or as a CommonJS
   require — see web/pdf2img/zip.mjs, which re-exports this same
   implementation for pdf2img.mjs and its tests. */

(function (global) {
  const encoder = new TextEncoder();
  const crcTable = makeCrcTable();

  function zipStore(files) {
    // ZIP32 (no Zip64) hard limits: 16-bit entry count and 32-bit offsets/sizes.
    // Silently truncating/wrapping these produces a corrupt archive, so fail loudly instead.
    if (files.length > 0xffff) {
      throw new Error(`zipStore: ${files.length} files exceeds ZIP32 limit of 65535 entries`);
    }

    const localParts = [];
    const centralParts = [];
    let offset = 0;

    for (const file of files) {
      const name = encoder.encode(file.name);
      const data = file.data instanceof Uint8Array ? file.data : new Uint8Array(file.data);
      if (data.length >= 0x100000000) {
        throw new Error(`zipStore: entry "${file.name}" is ${data.length} bytes, exceeds ZIP32 4GiB limit`);
      }
      const crc = crc32(data);
      const { time, date } = dosDateTime(new Date());

      const local = new Uint8Array(30 + name.length);
      writeU32(local, 0, 0x04034b50);
      writeU16(local, 4, 20);
      writeU16(local, 6, 0x0800);
      writeU16(local, 8, 0);
      writeU16(local, 10, time);
      writeU16(local, 12, date);
      writeU32(local, 14, crc);
      writeU32(local, 18, data.length);
      writeU32(local, 22, data.length);
      writeU16(local, 26, name.length);
      local.set(name, 30);
      localParts.push(local, data);

      const central = new Uint8Array(46 + name.length);
      writeU32(central, 0, 0x02014b50);
      writeU16(central, 4, 20);
      writeU16(central, 6, 20);
      writeU16(central, 8, 0x0800);
      writeU16(central, 10, 0);
      writeU16(central, 12, time);
      writeU16(central, 14, date);
      writeU32(central, 16, crc);
      writeU32(central, 20, data.length);
      writeU32(central, 24, data.length);
      writeU16(central, 28, name.length);
      writeU32(central, 42, offset);
      central.set(name, 46);
      centralParts.push(central);

      offset += local.length + data.length;
      if (offset >= 0x100000000) {
        throw new Error(`zipStore: archive offset ${offset} exceeds ZIP32 4GiB limit`);
      }
    }

    const centralSize = byteLength(centralParts);
    const end = new Uint8Array(22);
    writeU32(end, 0, 0x06054b50);
    writeU16(end, 8, files.length);
    writeU16(end, 10, files.length);
    writeU32(end, 12, centralSize);
    writeU32(end, 16, offset);

    return concat([...localParts, ...centralParts, end]);
  }

  async function zipStoreStream(files, sink) {
    const rawWrite = typeof sink === "function"
      ? sink
      : sink && typeof sink.write === "function"
        ? (part) => sink.write(part)
        : null;
    if (!rawWrite) throw new TypeError("zipStoreStream requires an async sink");
    const signal = typeof sink === "function" ? null : sink.signal;
    const isCurrent = typeof sink === "function" || typeof sink.isCurrent !== "function"
      ? null
      : sink.isCurrent;
    const assertActive = () => {
      if (signal?.aborted || (isCurrent && !isCurrent())) {
        throw signal?.reason instanceof Error
          ? signal.reason
          : new DOMException("ZIP streaming aborted", "AbortError");
      }
    };
    const write = async (part) => {
      assertActive();
      await rawWrite(part);
      assertActive();
    };
    const centralParts = [];
    let offset = 0;
    let count = 0;
    for await (const file of files) {
      if (count >= 0xffff) throw new Error("zipStore: entry count exceeds ZIP32 limit of 65535 entries");
      if (!file || typeof file !== "object") throw new TypeError("zipStore: invalid entry");
      const fileName = String(file.name || "");
      const name = encoder.encode(fileName);
      if (!name.length || name.length > 0xffff) throw new Error("zipStore: invalid entry name");
      const { time, date } = dosDateTime(new Date());
      const local = new Uint8Array(30 + name.length);
      writeU32(local, 0, 0x04034b50); writeU16(local, 4, 20); writeU16(local, 6, 0x0808);
      writeU16(local, 10, time); writeU16(local, 12, date); writeU16(local, 26, name.length); local.set(name, 30);
      const localOffset = offset;
      await write(local);
      offset = checkedOffset(offset, local.length);

      let crcState = 0xffffffff;
      let size = 0;
      for await (const data of payloadChunks(file.data)) {
        if (!data.length) continue;
        size = checkedSize(fileName, size, data.length);
        crcState = crc32Update(crcState, data);
        await write(data);
        offset = checkedOffset(offset, data.length);
      }
      const crc = (crcState ^ 0xffffffff) >>> 0;
      const descriptor = new Uint8Array(16);
      writeU32(descriptor, 0, 0x08074b50); writeU32(descriptor, 4, crc);
      writeU32(descriptor, 8, size); writeU32(descriptor, 12, size);
      await write(descriptor);
      offset = checkedOffset(offset, descriptor.length);

      const central = new Uint8Array(46 + name.length);
      writeU32(central, 0, 0x02014b50); writeU16(central, 4, 20); writeU16(central, 6, 20); writeU16(central, 8, 0x0808);
      writeU16(central, 12, time); writeU16(central, 14, date); writeU32(central, 16, crc);
      writeU32(central, 20, size); writeU32(central, 24, size); writeU16(central, 28, name.length); writeU32(central, 42, localOffset); central.set(name, 46);
      centralParts.push(central);
      count++;
    }
    const centralOffset = offset;
    const centralSize = byteLength(centralParts);
    if (centralSize >= 0x100000000) throw new Error("zipStore: central directory exceeds ZIP32 4GiB limit");
    const end = new Uint8Array(22); writeU32(end, 0, 0x06054b50); writeU16(end, 8, count); writeU16(end, 10, count);
    writeU32(end, 12, centralSize); writeU32(end, 16, centralOffset);
    for (const part of centralParts) {
      await write(part);
      offset = checkedOffset(offset, part.length);
    }
    await write(end);
    offset = checkedOffset(offset, end.length);
    return { entries: count, bytesWritten: offset };
  }

  async function* payloadChunks(data) {
    if (data instanceof Uint8Array) {
      yield data;
      return;
    }
    if (data instanceof ArrayBuffer) {
      yield new Uint8Array(data);
      return;
    }
    if (ArrayBuffer.isView(data)) {
      yield new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
      return;
    }
    if (typeof Blob !== "undefined" && data instanceof Blob) {
      if (typeof data.stream === "function") {
        yield* readableChunks(data.stream());
      } else {
        yield new Uint8Array(await data.arrayBuffer());
      }
      return;
    }
    if (data && typeof data.getReader === "function") {
      yield* readableChunks(data);
      return;
    }
    if (data && typeof data[Symbol.asyncIterator] === "function") {
      for await (const chunk of data) yield payloadChunk(chunk);
      return;
    }
    if (Array.isArray(data) && data.every((value) => Number.isInteger(value))) {
      yield new Uint8Array(data);
      return;
    }
    if (data && typeof data[Symbol.iterator] === "function") {
      for (const chunk of data) yield payloadChunk(chunk);
      return;
    }
    throw new TypeError("zipStore: entry data must be bytes or an iterable of byte chunks");
  }

  async function* readableChunks(stream) {
    const reader = stream.getReader();
    let completed = false;
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) {
          completed = true;
          return;
        }
        yield payloadChunk(value);
      }
    } finally {
      if (!completed) {
        try { await reader.cancel(new Error("ZIP payload consumption stopped")); } catch {}
      }
      reader.releaseLock?.();
    }
  }

  function payloadChunk(value) {
    if (value instanceof Uint8Array) return value;
    if (value instanceof ArrayBuffer) return new Uint8Array(value);
    if (ArrayBuffer.isView(value)) return new Uint8Array(value.buffer, value.byteOffset, value.byteLength);
    if (Array.isArray(value)) return new Uint8Array(value);
    throw new TypeError("zipStore: payload chunks must be byte arrays");
  }

  function checkedSize(name, size, added) {
    const next = size + added;
    if (!Number.isSafeInteger(next) || next >= 0x100000000) {
      throw new Error(`zipStore: entry "${name}" exceeds ZIP32 4GiB limit`);
    }
    return next;
  }

  function checkedOffset(offset, added) {
    const next = offset + added;
    if (!Number.isSafeInteger(next) || next >= 0x100000000) {
      throw new Error(`zipStore: archive offset ${next} exceeds ZIP32 4GiB limit`);
    }
    return next;
  }

  function concat(parts) {
    const out = new Uint8Array(byteLength(parts));
    let pos = 0;
    for (const part of parts) {
      out.set(part, pos);
      pos += part.length;
    }
    return out;
  }

  function byteLength(parts) {
    return parts.reduce((n, part) => n + part.length, 0);
  }

  function writeU16(out, offset, value) {
    out[offset] = value & 0xff;
    out[offset + 1] = (value >>> 8) & 0xff;
  }

  function writeU32(out, offset, value) {
    out[offset] = value & 0xff;
    out[offset + 1] = (value >>> 8) & 0xff;
    out[offset + 2] = (value >>> 16) & 0xff;
    out[offset + 3] = (value >>> 24) & 0xff;
  }

  function dosDateTime(d) {
    const year = Math.max(1980, d.getFullYear());
    return {
      time: (d.getHours() << 11) | (d.getMinutes() << 5) | Math.floor(d.getSeconds() / 2),
      date: ((year - 1980) << 9) | ((d.getMonth() + 1) << 5) | d.getDate(),
    };
  }

  function makeCrcTable() {
    const table = new Uint32Array(256);
    for (let i = 0; i < table.length; i++) {
      let c = i;
      for (let j = 0; j < 8; j++) {
        c = (c & 1) ? (0xedb88320 ^ (c >>> 1)) : (c >>> 1);
      }
      table[i] = c >>> 0;
    }
    return table;
  }

  function crc32(data) {
    const crc = crc32Update(0xffffffff, data);
    return (crc ^ 0xffffffff) >>> 0;
  }

  function crc32Update(crc, data) {
    for (const b of data) crc = crcTable[(crc ^ b) & 0xff] ^ (crc >>> 8);
    return crc;
  }

  global.zipStore = zipStore;
  global.zipStoreStream = zipStoreStream;
})(typeof window !== "undefined" ? window : globalThis);
