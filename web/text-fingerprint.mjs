function hex32(value) {
  return (value >>> 0).toString(16).padStart(8, "0");
}

export function createTextFingerprint() {
  let first = 0x811c9dc5;
  let second = 0x9e3779b9;
  let length = 0;
  return {
    update(value) {
      const text = String(value ?? "");
      for (let index = 0; index < text.length; index++) {
        const code = text.charCodeAt(index);
        first = Math.imul(first ^ code, 0x01000193);
        second = Math.imul(second ^ code, 0x5bd1e995);
        second ^= second >>> 13;
      }
      length += text.length;
    },
    digest() {
      return `${hex32(first)}${hex32(second)}${hex32(length)}`;
    },
  };
}

export function fingerprintText(value) {
  const fingerprint = createTextFingerprint();
  fingerprint.update(value);
  return fingerprint.digest();
}

export function utf8ByteLength(value) {
  const text = String(value ?? "");
  let bytes = 0;
  for (let index = 0; index < text.length; index++) {
    const code = text.charCodeAt(index);
    if (code <= 0x7f) bytes++;
    else if (code <= 0x7ff) bytes += 2;
    else if (code >= 0xd800 && code <= 0xdbff && index + 1 < text.length &&
        text.charCodeAt(index + 1) >= 0xdc00 && text.charCodeAt(index + 1) <= 0xdfff) {
      bytes += 4;
      index++;
    } else bytes += 3;
  }
  return bytes;
}
