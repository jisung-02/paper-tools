// web/send/send.test.mjs — unit tests for the encodeSdp/decodeSdp codec used
// by the Direct Send tool (web/send/send.mjs) to pack an SDP offer/answer
// into a URL-fragment-safe / QR-safe string. Run with:
//   node --test web/send/send.test.mjs
//
// send.mjs guards all DOM/window/RTCPeerConnection access behind
// `typeof document !== "undefined"`, so importing it here under plain Node
// only runs the pure codec functions below.
import assert from "node:assert/strict";
import { test } from "node:test";
import { buildAnswerLink, decodeSdp, encodeSdp, parseHash } from "./send.mjs";

const SAMPLE_SDP =
  "v=0\r\no=- 4611731400430051336 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n" +
  "a=group:BUNDLE 0\r\na=msid-semantic: WMS\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\n" +
  "c=IN IP4 0.0.0.0\r\na=ice-ufrag:abcd\r\na=ice-pwd:0123456789abcdef0123456789\r\n" +
  "a=fingerprint:sha-256 AA:BB:CC:DD\r\na=setup:actpass\r\na=mid:0\r\na=sctp-port:5000\r\n";

test("encodeSdp/decodeSdp: round-trips a typical SDP through the compressed path", async () => {
  const code = await encodeSdp(SAMPLE_SDP);
  assert.equal(code.charAt(0), "c", "should use the compressed prefix when CompressionStream exists");
  const decoded = await decodeSdp(code);
  assert.equal(decoded, SAMPLE_SDP);
});

test("encodeSdp/decodeSdp: round-trips unicode text", async () => {
  const text = "다른 기기로 파일 보내기 — 안녕하세요 🎉 (unicode SDP-ish text)";
  const code = await encodeSdp(text);
  const decoded = await decodeSdp(code);
  assert.equal(decoded, text);
});

test("encodeSdp/decodeSdp: round-trips text with every byte value density (short/long)", async () => {
  const short = "a";
  const codeShort = await encodeSdp(short);
  assert.equal(await decodeSdp(codeShort), short);

  const long = SAMPLE_SDP.repeat(50);
  const codeLong = await encodeSdp(long);
  assert.equal(await decodeSdp(codeLong), long);
});

test("encodeSdp/decodeSdp: falls back to plain base64url when Compression/DecompressionStream are unavailable", async () => {
  const originalC = globalThis.CompressionStream;
  const originalD = globalThis.DecompressionStream;
  // @ts-ignore — simulate old Safari, which has neither.
  delete globalThis.CompressionStream;
  // @ts-ignore
  delete globalThis.DecompressionStream;
  try {
    const code = await encodeSdp(SAMPLE_SDP);
    assert.equal(code.charAt(0), "u", "should use the uncompressed-fallback prefix");
    const decoded = await decodeSdp(code);
    assert.equal(decoded, SAMPLE_SDP);
  } finally {
    globalThis.CompressionStream = originalC;
    globalThis.DecompressionStream = originalD;
  }
});

test("encodeSdp: rejects an empty string", async () => {
  await assert.rejects(() => encodeSdp(""));
});

test("encodeSdp: rejects non-string input", async () => {
  await assert.rejects(() => encodeSdp(null));
  await assert.rejects(() => encodeSdp(undefined));
});

test("decodeSdp: rejects an empty or too-short code", async () => {
  await assert.rejects(() => decodeSdp(""));
  await assert.rejects(() => decodeSdp("c"));
});

test("decodeSdp: rejects an unrecognized prefix", async () => {
  await assert.rejects(() => decodeSdp("xSGVsbG8"));
});

test("decodeSdp: rejects invalid base64url characters", async () => {
  await assert.rejects(() => decodeSdp("u not-valid-base64!!"));
});

test("codes produced are URL-fragment-safe (no +, /, or = characters)", async () => {
  const code = await encodeSdp(SAMPLE_SDP);
  assert.equal(/[+/=]/.test(code), false);
});

test("buildAnswerLink: joins origin, pathname, code, and sid behind an '#a=' fragment", () => {
  const link = buildAnswerLink("https://papertools.dev", "/send/", "cAbC123", "deadbeef");
  assert.equal(link, "https://papertools.dev/send/#a=cAbC123.deadbeef");
});

test("parseHash: parses an offer link's code and session id", () => {
  assert.deepEqual(parseHash("#r=cAbC123.deadbeef"), { kind: "r", code: "cAbC123", sid: "deadbeef" });
});

test("parseHash: parses a reply link's code and session id", () => {
  assert.deepEqual(parseHash("#a=cAbC123.deadbeef"), { kind: "a", code: "cAbC123", sid: "deadbeef" });
});

test("parseHash: sid is null for a malformed offer/reply link with no session id", () => {
  assert.deepEqual(parseHash("#r=cAbC123"), { kind: "r", code: "cAbC123", sid: null });
  assert.deepEqual(parseHash("#a=cAbC123"), { kind: "a", code: "cAbC123", sid: null });
});

test("parseHash: an extra '.' is folded into the sid rather than misparsing the code", () => {
  assert.deepEqual(parseHash("#r=cAbC123.dead.beef"), { kind: "r", code: "cAbC123", sid: "dead.beef" });
});

test("parseHash: returns null for a trailing '.' with nothing after it, or a hash matching neither shape", () => {
  assert.equal(parseHash("#r=cAbC123."), null);
  assert.equal(parseHash("#a=cAbC123."), null);
  assert.equal(parseHash(""), null);
  assert.equal(parseHash("#foo=bar"), null);
});
