// web/send/send.mjs — Direct Send: browser-to-browser file transfer with no
// server in the loop at all, not even for signaling. Two peers exchange a
// WebRTC offer/answer manually (as a link+QR / pasted-back code) and then
// stream the file straight over a data channel. iceServers is deliberately
// empty — only host candidates are gathered, so this only works when both
// devices can reach each other directly (typically: same LAN/Wi-Fi).
//
// This module is imported directly by send.test.mjs under plain Node to
// exercise the codec below, so every DOM/window/RTCPeerConnection access is
// confined to main(), which only runs when `document` exists (i.e. in a
// real browser tab).

import { createReceiverSession, createSenderSession } from "./transfer.mjs";
import { MAX_FILES, MAX_TOTAL_BYTES } from "./protocol.mjs";
import { createReceiveSink } from "./storage.mjs";

/* ------------------------------------------------------------- codec --- */

// Compressed SDP blobs are prefixed "c", the plain-base64url fallback (used
// when CompressionStream isn't available, e.g. old Safari) is prefixed "u".
// Both are safe to put after a URL "#" fragment and inside a QR code.

const B64_CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
export const MAX_SDP_BYTES = 256 * 1024;
export const MAX_IN_MEMORY_TRANSFER_BYTES = 256 * 1024 * 1024;

function newTransferId() {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return [...bytes].map((value) => value.toString(16).padStart(2, "0")).join("");
}

export class SenderResumeState {
  constructor(transferIdFactory = newTransferId) {
    if (typeof transferIdFactory !== "function") throw new TypeError("transfer id factory is required");
    this.transferIdFactory = transferIdFactory;
    this.current = null;
  }

  select(files) {
    const selected = Array.from(files || []);
    if (selected.length === 0) throw new Error("at least one file is required");
    const transferId = this.transferIdFactory();
    if (typeof transferId !== "string" || transferId.length < 1 || transferId.length > 128) {
      throw new Error("invalid transfer id");
    }
    this.current = Object.freeze({ files: Object.freeze(selected), transferId });
    return this.current;
  }

  resume() {
    return this.current;
  }

  clear() {
    this.current = null;
  }

  get canResume() {
    return this.current !== null;
  }
}

function bytesToBase64Url(bytes) {
  let out = "";
  for (let i = 0; i < bytes.length; i += 3) {
    const b0 = bytes[i];
    const b1 = i + 1 < bytes.length ? bytes[i + 1] : undefined;
    const b2 = i + 2 < bytes.length ? bytes[i + 2] : undefined;
    out += B64_CHARS[b0 >> 2];
    out += B64_CHARS[((b0 & 0x03) << 4) | (b1 === undefined ? 0 : b1 >> 4)];
    out += b1 === undefined ? "" : B64_CHARS[((b1 & 0x0f) << 2) | (b2 === undefined ? 0 : b2 >> 6)];
    out += b2 === undefined ? "" : B64_CHARS[b2 & 0x3f];
  }
  // base64 -> base64url, and no "=" padding is ever produced above.
  return out.replace(/\+/g, "-").replace(/\//g, "_");
}

function base64UrlToBytes(str) {
  const base64 = str.replace(/-/g, "+").replace(/_/g, "/");
  const byteLen = Math.floor((base64.length * 6) / 8);
  const out = new Uint8Array(byteLen);
  let bits = 0;
  let value = 0;
  let idx = 0;
  for (let i = 0; i < base64.length; i++) {
    const v = B64_CHARS.indexOf(base64[i]);
    if (v === -1) throw new Error("invalid code");
    value = (value << 6) | v;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out[idx++] = (value >> bits) & 0xff;
    }
  }
  return out;
}

function concatUint8Arrays(chunks) {
  let total = 0;
  for (const c of chunks) total += c.byteLength;
  const out = new Uint8Array(total);
  let offset = 0;
  for (const c of chunks) {
    out.set(c, offset);
    offset += c.byteLength;
  }
  return out;
}

// Pipes bytes through a TransformStream (Compression/DecompressionStream)
// and collects the output. Standard MDN idiom: write+close without waiting
// for them, then drain the readable side.
async function pipeBytes(bytes, transform, maxOutputBytes = Number.MAX_SAFE_INTEGER, limitMessage = "stream output is too large") {
  const writer = transform.writable.getWriter();
  const writing = writer.write(bytes).then(() => writer.close());
  const chunks = [];
  let total = 0;
  try {
    for await (const chunk of transform.readable) {
      if (!Number.isSafeInteger(total + chunk.byteLength) || total + chunk.byteLength > maxOutputBytes) {
        throw new Error(limitMessage);
      }
      chunks.push(chunk);
      total += chunk.byteLength;
    }
    await writing;
  } catch (error) {
    try { await writer.abort(error); } catch {}
    try { await writing; } catch {}
    throw error;
  }
  return concatUint8Arrays(chunks);
}

// encodeSdp(sdp) -> Promise<string>: deflate-raw + base64url ("c" prefix),
// or plain base64url ("u" prefix) if CompressionStream isn't available.
export async function encodeSdp(sdp) {
  if (typeof sdp !== "string" || sdp.length === 0) {
    throw new Error("cannot encode an empty SDP");
  }
  const utf8 = new TextEncoder().encode(sdp);
  if (utf8.byteLength > MAX_SDP_BYTES) throw new Error("SDP is too large");
  if (typeof CompressionStream === "function") {
    const compressed = await pipeBytes(utf8, new CompressionStream("deflate-raw"));
    return "c" + bytesToBase64Url(compressed);
  }
  return "u" + bytesToBase64Url(utf8);
}

// decodeSdp(code) -> Promise<string>: the reverse of encodeSdp.
export async function decodeSdp(code) {
  if (typeof code !== "string" || code.length < 2) {
    throw new Error("cannot decode an empty code");
  }
  const prefix = code.charAt(0);
  const payload = code.slice(1);
  if (payload.length > Math.ceil(MAX_SDP_BYTES * 2)) throw new Error("code is too large");
  if (prefix === "c") {
    if (typeof DecompressionStream !== "function") {
      throw new Error("this code needs a browser feature that isn't available here");
    }
    const compressed = base64UrlToBytes(payload);
    if (compressed.byteLength > MAX_SDP_BYTES) throw new Error("code is too large");
    const raw = await pipeBytes(
      compressed,
      new DecompressionStream("deflate-raw"),
      MAX_SDP_BYTES,
      "SDP is too large",
    );
    return new TextDecoder().decode(raw);
  }
  if (prefix === "u") {
    const raw = base64UrlToBytes(payload);
    if (raw.byteLength > MAX_SDP_BYTES) throw new Error("SDP is too large");
    return new TextDecoder().decode(raw);
  }
  throw new Error("invalid code");
}

export function validateTransferMetadata(meta) {
  if (!meta || typeof meta !== "object" || typeof meta.name !== "string" ||
      typeof meta.size !== "number" || !Number.isSafeInteger(meta.size) || meta.size < 0 ||
      meta.size > MAX_IN_MEMORY_TRANSFER_BYTES || typeof meta.type !== "string" ||
      new TextEncoder().encode(meta.name).byteLength > 255 || meta.type.length > 128) {
    throw new Error("invalid transfer metadata");
  }
  return { name: meta.name, size: meta.size, type: meta.type || "application/octet-stream" };
}

export class ReceiveState {
  constructor() { this.meta = null; this.chunks = []; this.received = 0; this.done = false; }
  metadata(value) {
    if (this.meta || this.received) throw new Error("metadata out of order");
    this.meta = validateTransferMetadata(value);
  }
  chunk(value) {
    if (!this.meta || this.done || !(value instanceof Uint8Array)) throw new Error("chunk out of order");
    if (this.received + value.byteLength > this.meta.size) throw new Error("chunk exceeds declared size");
    this.chunks.push(value); this.received += value.byteLength;
  }
  finish() {
    if (!this.meta || this.done || this.received !== this.meta.size) throw new Error("incomplete transfer");
    this.done = true;
    const blob = new Blob(this.chunks, { type: this.meta.type });
    this.chunks = [];
    return blob;
  }
}

// buildAnswerLink(origin, pathname, code, sid) -> the reply link shown to
// the receiver. Opening this link on the sending device's browser relays
// the answer code automatically (see startRelay, below), with the raw code
// kept as a manual-paste fallback for when that isn't possible (e.g. the
// link is opened on a different device/browser than the original sender).
// `sid` is the sending tab's session id (see genSessionId in main()) so the
// relay tab can scope its BroadcastChannel to the one sender tab that's
// actually waiting on this answer, instead of every open sender tab racing
// to consume it.
export function buildAnswerLink(origin, pathname, code, sid) {
  return origin + pathname + "#a=" + code + "." + sid;
}

// splitCodeSid("<code>.<sid>") -> { code, sid } | null. "." is a safe
// separator: encodeSdp's codes and genSessionId's ids are both drawn from
// alphabets that never contain ".". Anything after the first "." is taken
// as the sid verbatim (so it tolerates an accidental extra "." landing in
// the sid position without misparsing the code). Returns null when either
// side would be empty (e.g. a trailing "." with nothing after it).
function splitCodeSid(str) {
  const dot = str.indexOf(".");
  if (dot === -1) return { code: str, sid: null };
  const code = str.slice(0, dot);
  const sid = str.slice(dot + 1);
  if (!code || !sid) return null;
  return { code, sid };
}

// parseHash(hash) -> { kind: "r" | "a", code, sid } | null. Pure parser for
// the URL fragment used by both the "#r=" offer link and the "#a=" reply
// link, each of the form "<code>.<sid>" (sid is null when it's missing,
// e.g. a malformed/old-style link with no "."). Returns null when the hash
// doesn't match either shape at all.
export function parseHash(hash) {
  const rMatch = /^#r=(.+)$/.exec(hash);
  if (rMatch) {
    const parsed = splitCodeSid(rMatch[1]);
    return parsed ? { kind: "r", code: parsed.code, sid: parsed.sid } : null;
  }
  const aMatch = /^#a=(.+)$/.exec(hash);
  if (aMatch) {
    const parsed = splitCodeSid(aMatch[1]);
    return parsed ? { kind: "a", code: parsed.code, sid: parsed.sid } : null;
  }
  return null;
}

export function parseResumeOfferInput(value) {
  if (typeof value !== "string" || !value.trim()) return null;
  const input = value.trim();
  let hash;
  if (input.startsWith("#")) hash = input;
  else if (input.startsWith("r=")) hash = `#${input}`;
  else if (input.includes("#")) {
    try { hash = new URL(input, "https://local.invalid/").hash; }
    catch { return null; }
  } else hash = `#r=${input}`;
  const parsed = parseHash(hash);
  return parsed?.kind === "r" && parsed.sid ? parsed : null;
}

/* ------------------------------------------------------- page wiring --- */
// Everything below touches the DOM/window/RTCPeerConnection and only runs
// in a real browser tab (guarded so send.test.mjs can import the codec
// above under plain Node without a `document` global).

if (typeof document !== "undefined") {
  main();
}

function main() {
  const ICE_GATHERING_TIMEOUT_MS = 3000;
  const CONNECT_TIMEOUT_MS = 30000;

  const statusEl = document.getElementById("status");
  if (statusEl) statusEl.hidden = true;

  const senderView = document.getElementById("senderView");
  const receiverView = document.getElementById("receiverView");
  const errEl = document.getElementById("err");

  const fileDz = window.dropzone("fileDrop", { multiple: true });
  const pickHint = document.getElementById("pickHint");

  const senderPanel = document.getElementById("senderPanel");
  const offerStatus = document.getElementById("offerStatus");
  const qrOffer = document.getElementById("qrOffer");
  const offerLinkInput = document.getElementById("offerLink");
  const copyLinkBtn = document.getElementById("copyLinkBtn");
  const answerCodeInput = document.getElementById("answerCode");
  const connectBtn = document.getElementById("connectBtn");
  const sendProgressWrap = document.getElementById("sendProgressWrap");
  const sendProgressBar = document.getElementById("sendProgressBar");
  const sendProgressText = document.getElementById("sendProgressText");
  const sendResumePanel = document.getElementById("sendResumePanel");
  const resumeSendBtn = document.getElementById("resumeSendBtn");
  const cancelSendBtn = document.getElementById("cancelSendBtn");

  const recvStatus = document.getElementById("recvStatus");
  const replyLinkOut = document.getElementById("replyLinkOut");
  const copyReplyLinkBtn = document.getElementById("copyReplyLinkBtn");
  const replyCodeOut = document.getElementById("replyCodeOut");
  const copyCodeBtn = document.getElementById("copyCodeBtn");
  const qrReply = document.getElementById("qrReply");
  const recvProgressWrap = document.getElementById("recvProgressWrap");
  const recvProgressBar = document.getElementById("recvProgressBar");
  const recvProgressText = document.getElementById("recvProgressText");
  let chooseFolderBtn = document.getElementById("chooseFolderBtn");
  let receiveDestination = document.getElementById("receiveDestination");
  let receivedFiles = document.getElementById("receivedFiles");
  let receivedFilesList = document.getElementById("receivedFilesList");
  const receiveResumePanel = document.getElementById("receiveResumePanel");
  const resumeOfferInput = document.getElementById("resumeOfferInput");
  const applyResumeOfferBtn = document.getElementById("applyResumeOfferBtn");
  const abortReceiveBtn = document.getElementById("abortReceiveBtn");
  // Keep a newly deployed module usable with an older cached/localized page.
  if (!chooseFolderBtn) {
    chooseFolderBtn = document.createElement("button");
    chooseFolderBtn.id = "chooseFolderBtn";
    chooseFolderBtn.type = "button";
    chooseFolderBtn.className = "secondary";
    chooseFolderBtn.hidden = true;
    chooseFolderBtn.textContent = window.t("Save directly to a folder", "폴더에 바로 저장");
    receiverView.prepend(chooseFolderBtn);
  }
  if (!receiveDestination) {
    receiveDestination = document.createElement("p");
    receiveDestination.id = "receiveDestination";
    receiveDestination.className = "hint";
    receiveDestination.setAttribute("role", "status");
    receiveDestination.setAttribute("aria-live", "polite");
    receiveDestination.textContent = window.t(
      "Without a selected folder, files use private browser storage when available.",
      "폴더를 선택하지 않으면 가능한 경우 브라우저 전용 저장소를 사용합니다.",
    );
    chooseFolderBtn.after(receiveDestination);
  }
  if (!receivedFiles || !receivedFilesList) {
    receivedFiles = document.createElement("section");
    receivedFiles.id = "receivedFiles";
    receivedFiles.hidden = true;
    const heading = document.createElement("h2");
    heading.textContent = window.t("Received files", "받은 파일");
    receivedFilesList = document.createElement("ul");
    receivedFilesList.id = "receivedFilesList";
    receivedFiles.append(heading, receivedFilesList);
    receiverView.appendChild(receivedFiles);
  }
  const oldDownloadBtn = document.getElementById("downloadBtn");
  if (oldDownloadBtn) oldDownloadBtn.hidden = true;

  const relayView = document.getElementById("relayView");
  const relayStatus = document.getElementById("relayStatus");
  const relayFallback = document.getElementById("relayFallback");
  const relayCodeOut = document.getElementById("relayCodeOut");
  const copyRelayCodeBtn = document.getElementById("copyRelayCodeBtn");

  // "pt-send-<sid>" carries the reply link's answer code from a relay tab
  // (opened from the "#a=" reply link) back to the original sender tab, so
  // the sender doesn't have to paste it by hand. Purely a same-browser
  // convenience: it never crosses devices, so the paste path always works
  // as a fallback. Guarded for browsers without BroadcastChannel.
  //
  // Each sender tab mints its own session id and scopes its channel name to
  // it (see startSender/startRelay below) so that with two sender tabs open
  // at once, a relay tab's answer only ever reaches the one sender tab it
  // actually belongs to — not whichever sender tab happens to still be
  // waiting.
  const RELAY_ACK_TIMEOUT_MS = 1500;
  const hasBroadcastChannel = typeof BroadcastChannel === "function";

  // genSessionId() -> an ~8-char lowercase-hex session id, unique enough to
  // scope one sender tab's BroadcastChannel from any other's. Uses
  // crypto.getRandomValues (not Math.random) since collisions here would
  // silently misroute an answer, same as the bug this id exists to fix.
  function genSessionId() {
    const bytes = new Uint8Array(4);
    crypto.getRandomValues(bytes);
    let out = "";
    for (const b of bytes) out += b.toString(16).padStart(2, "0");
    return out;
  }

  function clearErr() {
    if (errEl) errEl.textContent = "";
  }

  // Resolved at call time (not cached) so it reflects the language active
  // when the failure actually happens, not the one active at page load.
  function showFailure() {
    window.showErr(
      errEl,
      window.t(
        "Connection failed. Make sure both devices are on the same network and try again.",
        "연결에 실패했습니다. 두 기기가 같은 네트워크에 있는지 확인한 뒤 다시 시도해 주세요."
      )
    );
  }

  function formatBytes(n) {
    if (n < 1024) return n + " B";
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
    return (n / (1024 * 1024)).toFixed(1) + " MB";
  }

  /* --------------------------------------------------- QR rendering --- */

  function renderQr(container, text) {
    container.innerHTML = "";
    try {
      const qr = window.qrcode(0, "M");
      qr.addData(text);
      qr.make();
      const svg = qr.createSvgTag({ scalable: true });
      container.innerHTML = svg;
      const svgEl = container.querySelector("svg");
      if (svgEl) {
        svgEl.style.width = "100%";
        svgEl.style.maxWidth = "260px";
        svgEl.style.height = "auto";
        svgEl.style.display = "block";
      }
    } catch (e) {
      // QR generation is a convenience, not load-bearing — the link/code
      // text field still works if it fails (e.g. text too long for a QR).
    }
  }

  /* -------------------------------------------- ICE / connection state --- */

  function waitIceGatheringComplete(pc) {
    if (pc.iceGatheringState === "complete") return Promise.resolve();
    return new Promise((resolve) => {
      let done = false;
      const finish = () => {
        if (done) return;
        done = true;
        pc.removeEventListener("icegatheringstatechange", onChange);
        resolve();
      };
      const onChange = () => {
        if (pc.iceGatheringState === "complete") finish();
      };
      pc.addEventListener("icegatheringstatechange", onChange);
      setTimeout(finish, ICE_GATHERING_TIMEOUT_MS);
    });
  }

  // Wires oniceconnectionstatechange to a status paragraph, in the active
  // language, and returns a helper to arm a 30s "still not connected"
  // failure timeout (started once this side's answer has been applied).
  function wireConnectionState(pc, statusEl2, onFail, isCurrent = () => true) {
    let timeoutId = null;
    let failed = false;

    function clearTimer() {
      if (timeoutId) {
        clearTimeout(timeoutId);
        timeoutId = null;
      }
    }

    function fail() {
      if (failed || !isCurrent()) return;
      failed = true;
      clearTimer();
      onFail();
    }

    function update() {
      if (!isCurrent()) {
        clearTimer();
        return;
      }
      const state = pc.iceConnectionState;
      if (state === "checking" || state === "new") {
        if (statusEl2) statusEl2.textContent = window.t("Connecting…", "연결 중…");
      } else if (state === "connected" || state === "completed") {
        clearTimer();
        if (statusEl2) statusEl2.textContent = window.t("Connected", "연결됨");
      } else if (state === "failed" || state === "disconnected" || state === "closed") {
        fail();
      }
    }

    pc.addEventListener("iceconnectionstatechange", update);
    update();

    return {
      startConnectTimeout() {
        clearTimer();
        timeoutId = setTimeout(() => {
          if (!isCurrent()) return;
          const state = pc.iceConnectionState;
          if (state !== "connected" && state !== "completed") fail();
        }, CONNECT_TIMEOUT_MS);
      },
    };
  }

  /* --------------------------------------------------------- sender --- */

  let senderPc = null;
  let senderChannel = null;
  let senderTransfer = null;
  let senderGeneration = 0;
  const senderResumeState = new SenderResumeState();

  function showSenderResume(generation, error = null) {
    if (generation !== senderGeneration || !senderResumeState.canResume) return;
    if (error) window.showErr(errEl, error?.message || String(error));
    else showFailure();
    sendResumePanel.hidden = false;
  }

  function resetSender({ clearIdentity = false } = {}) {
    senderGeneration++;
    if (senderPc) {
      try {
        senderPc.close();
      } catch (e) {
        // ignore
      }
      senderPc = null;
    }
    if (senderChannel) {
      try {
        senderChannel.close();
      } catch (e) {
        // ignore
      }
      senderChannel = null;
    }
    if (senderTransfer) {
      senderTransfer.dispose();
      senderTransfer = null;
    }
    if (clearIdentity) senderResumeState.clear();
    senderPanel.hidden = true;
    sendResumePanel.hidden = true;
    sendProgressWrap.hidden = true;
    sendProgressBar.value = 0;
    answerCodeInput.value = "";
    offerStatus.textContent = "";
  }

  async function startSender(selection) {
    clearErr();
    resetSender();
    const generation = senderGeneration;
    const { files, transferId } = selection;
    senderPanel.hidden = false;

    let pc;
    try {
      pc = new RTCPeerConnection({ iceServers: [] });
      senderPc = pc;
      const dc = pc.createDataChannel("file");
      wireSendChannel(dc, files, transferId, generation);

      const conn = wireConnectionState(
        pc,
        offerStatus,
        () => showSenderResume(generation),
        () => generation === senderGeneration,
      );

      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      await waitIceGatheringComplete(pc);
      if (generation !== senderGeneration) return;

      const sid = genSessionId();
      const code = await encodeSdp(pc.localDescription.sdp);
      const link = location.origin + location.pathname + "#r=" + code + "." + sid;
      offerLinkInput.value = link;
      renderQr(qrOffer, link);

      let answered = false;

      // Shared by the paste+Connect path and the pt-answer relay path
      // below, so both apply an answer code identically and hit the same
      // failure path (showFailure) on a malformed code or a
      // setRemoteDescription rejection.
      async function applyAnswer(answerCode) {
        if (answered || generation !== senderGeneration) return;
        try {
          const answerSdp = await decodeSdp(answerCode);
          await pc.setRemoteDescription({ type: "answer", sdp: answerSdp });
          answered = true;
          conn.startConnectTimeout();
          if (senderChannel) {
            try {
              senderChannel.postMessage({ type: "pt-ack" });
            } catch (e) {
              // ignore — the relay tab just won't see the ack and will
              // fall back to showing the code for manual paste
            }
          }
        } catch (e) {
          showSenderResume(generation);
        }
      }

      connectBtn.onclick = async () => {
        clearErr();
        const answerCode = answerCodeInput.value.trim();
        if (!answerCode) return;
        await applyAnswer(answerCode);
      };

      if (hasBroadcastChannel) {
        senderChannel = new BroadcastChannel("pt-send-" + sid);
        senderChannel.onmessage = (ev) => {
          const data = ev.data;
          if (answered || !data || data.type !== "pt-answer" || typeof data.code !== "string") return;
          applyAnswer(data.code);
        };
      }
    } catch (e) {
      showSenderResume(generation, e);
    }
  }

  function wireSendChannel(dc, files, transferId, generation) {
    dc.binaryType = "arraybuffer";

    let sent = false;
    let closed = false;
    const transfer = createSenderSession(dc, files, {
      transferId,
      onProgress(progress) {
        sendProgressWrap.hidden = false;
        const total = progress.total || 0;
        sendProgressBar.value = total === 0 ? 100 : Math.floor((progress.sent / total) * 100);
        sendProgressText.textContent = formatBytes(progress.sent) + " / " + formatBytes(total);
      },
    });
    senderTransfer = transfer;

    dc.onopen = async () => {
      try {
        sendProgressWrap.hidden = false;
        const result = await transfer.start();
        if (closed) return;
        sent = true;
        senderResumeState.clear();
        sendResumePanel.hidden = true;
        sendProgressBar.value = 100;
        sendProgressText.textContent = window.t(
          result.files === 1 ? "Sent 1 file" : `Sent ${result.files} files`,
          result.files === 1 ? "파일 1개 전송 완료" : `파일 ${result.files}개 전송 완료`,
        );
      } catch (e) {
        showSenderResume(generation, e);
      }
    };
    dc.onerror = () => showSenderResume(generation);
    dc.onclose = () => {
      closed = true;
      if (!sent) showSenderResume(generation);
    };
  }

  resumeSendBtn.addEventListener("click", () => {
    const selection = senderResumeState.resume();
    if (selection) startSender(selection);
  });

  cancelSendBtn.addEventListener("click", () => {
    clearErr();
    resetSender({ clearIdentity: true });
  });

  document.getElementById("fileDrop").addEventListener("dz:files", (e) => {
    const files = Array.from(e.detail.files || []);
    if (files.length === 0) return;
    const total = files.reduce((sum, file) => sum + file.size, 0);
    if (files.length > MAX_FILES || !Number.isSafeInteger(total) || total > MAX_TOTAL_BYTES) {
      pickHint.hidden = false;
      resetSender({ clearIdentity: true });
      return;
    }
    pickHint.hidden = true;
    startSender(senderResumeState.select(files));
  });

  /* ------------------------------------------------------- receiver --- */

  let receiverPc = null;
  let receiverDataChannel = null;
  let recvFinished = false;
  let receiverTransfer = null;
  let receiverGeneration = 0;
  let receiverHasContext = false;
  let receiveDirectory = null;
  const recvObjectUrls = new Set();
  const recvTempFiles = [];

  function cleanupReceivedFiles() {
    for (const url of recvObjectUrls) URL.revokeObjectURL(url);
    recvObjectUrls.clear();
    for (const { sink, file } of recvTempFiles.splice(0)) sink.release(file).catch(() => {});
  }

  function showReceiverResume(generation, error = null) {
    if (generation !== receiverGeneration || recvFinished) return;
    if (error) window.showErr(errEl, error?.message || String(error));
    else showFailure();
    receiveResumePanel.hidden = false;
  }

  if (typeof window.showDirectoryPicker === "function") {
    chooseFolderBtn.hidden = false;
    chooseFolderBtn.addEventListener("click", async () => {
      clearErr();
      try {
        receiveDirectory = await window.showDirectoryPicker({ mode: "readwrite" });
        receiveDestination.textContent = window.t(
          "Files will be saved directly to the selected folder.",
          "파일을 선택한 폴더에 바로 저장합니다.",
        );
      } catch (error) {
        if (error?.name !== "AbortError") window.showErr(errEl, error?.message || String(error));
      }
    });
  }

  async function addReceivedFile(file, value, sink) {
    receivedFiles.hidden = false;
    const item = document.createElement("li");
    if (sink?.kind === "directory") {
      item.textContent = file.name + " — " + window.t("Saved", "저장됨");
      receivedFilesList.appendChild(item);
      return;
    }
    const blob = value instanceof Blob ? value : await value.getFile();
    if (sink?.kind === "opfs" && typeof sink.release === "function") recvTempFiles.push({ sink, file });
    const url = URL.createObjectURL(blob);
    recvObjectUrls.add(url);
    const button = document.createElement("button");
    button.type = "button";
    button.className = "secondary";
    button.textContent = window.t("Download ", "다운로드: ") + file.name;
    button.addEventListener("click", () => {
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = file.name;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
    });
    item.appendChild(button);
    receivedFilesList.appendChild(item);
  }

  async function startReceiver(offerCode, sid, { preserveTransfer = false } = {}) {
    const generation = ++receiverGeneration;
    const previousPc = receiverPc;
    const previousChannel = receiverDataChannel;
    const previousTransfer = receiverTransfer;
    receiverPc = null;
    receiverDataChannel = null;
    receiverTransfer = null;
    try { previousChannel?.close(); } catch {}
    try { previousPc?.close(); } catch {}
    if (previousTransfer) {
      await previousTransfer.done.catch(() => {});
      previousTransfer.dispose();
    }
    if (generation !== receiverGeneration) return;

    senderView.hidden = true;
    receiverView.hidden = false;
    relayView.hidden = true;
    clearErr();
    recvFinished = false;
    if (!preserveTransfer) {
      cleanupReceivedFiles();
      receivedFiles.hidden = true;
      receivedFilesList.innerHTML = "";
      chooseFolderBtn.disabled = false;
    }

    let pc;
    try {
      // A "#r=" link with no sid is malformed (every offer link this page
      // generates includes one) — treat it the same as an invalid code.
      if (!sid) throw new Error("invalid code");
      const offerSdp = await decodeSdp(offerCode);
      if (generation !== receiverGeneration) return;
      pc = new RTCPeerConnection({ iceServers: [] });
      receiverPc = pc;

      const conn = wireConnectionState(
        pc,
        recvStatus,
        () => showReceiverResume(generation),
        () => generation === receiverGeneration,
      );

      pc.ondatachannel = (ev) => wireRecvChannel(ev.channel, generation);

      await pc.setRemoteDescription({ type: "offer", sdp: offerSdp });
      const answer = await pc.createAnswer();
      await pc.setLocalDescription(answer);
      await waitIceGatheringComplete(pc);
      if (generation !== receiverGeneration) {
        pc.close();
        return;
      }

      const code = await encodeSdp(pc.localDescription.sdp);
      const link = buildAnswerLink(location.origin, location.pathname, code, sid);
      replyLinkOut.value = link;
      // Kept as the bare code (no sid suffix) so the manual-paste path on
      // the sender side is unchanged — applyAnswer only ever takes a code.
      replyCodeOut.value = code;
      renderQr(qrReply, link);
      receiveResumePanel.hidden = true;
      resumeOfferInput.value = "";

      conn.startConnectTimeout();
    } catch (e) {
      showReceiverResume(generation, e);
    }
  }

  /* ---------------------------------------------------------- relay --- */
  // Handles the reply link's "#a=" fragment: this tab isn't a sender or a
  // receiver, it's just relaying the answer code back to the original
  // sender tab (see the "pt-send" BroadcastChannel wiring in startSender,
  // above). That only works when this tab and the original sender tab
  // share a browser — typically true when the link is opened back on the
  // sending device — so it falls back to a manual-paste code display when
  // no ack shows up in time (different device/browser, or the sender tab
  // was closed).

  function showRelayFallback(answerCode) {
    relayStatus.textContent = "";
    relayFallback.hidden = false;
    relayCodeOut.value = answerCode;
  }

  function startRelay(answerCode, sid) {
    senderView.hidden = true;
    receiverView.hidden = true;
    relayView.hidden = false;
    clearErr();

    // No BroadcastChannel support, or a "#a=" link with no sid (malformed —
    // every reply link this page generates includes one, or the sid was
    // stripped): skip the relay handshake entirely and go straight to the
    // manual-code fallback.
    if (!hasBroadcastChannel || !sid) {
      showRelayFallback(answerCode);
      return;
    }

    const channel = new BroadcastChannel("pt-send-" + sid);
    let acked = false;

    channel.onmessage = (ev) => {
      const data = ev.data;
      if (!data || data.type !== "pt-ack") return;
      acked = true;
      channel.close();
      relayStatus.textContent = window.t(
        "Connected — you can close this tab.",
        "연결되었습니다 — 이 탭은 닫아도 됩니다."
      );
    };

    relayStatus.textContent = window.t("Connecting…", "연결 중…");
    channel.postMessage({ type: "pt-answer", code: answerCode });

    setTimeout(() => {
      if (acked) return;
      channel.close();
      showRelayFallback(answerCode);
    }, RELAY_ACK_TIMEOUT_MS);
  }

  function wireRecvChannel(dc, generation) {
    if (generation !== receiverGeneration) {
      dc.close();
      return;
    }
    dc.binaryType = "arraybuffer";
    receiverDataChannel = dc;
    recvProgressWrap.hidden = false;
    const transfer = createReceiverSession(dc, {
      async sinkFactory(manifest) {
        return createReceiveSink({
          directory: receiveDirectory,
          storage: navigator.storage,
          maxMemoryBytes: Math.min(MAX_IN_MEMORY_TRANSFER_BYTES, manifest.totalSize),
        });
      },
      onSink(sink) {
        if (generation !== receiverGeneration) return;
        chooseFolderBtn.disabled = true;
        receiveDestination.textContent = sink.kind === "directory"
          ? window.t("Saving directly to the selected folder.", "선택한 폴더에 바로 저장 중입니다.")
          : sink.kind === "opfs"
            ? window.t("Receiving to private browser storage.", "브라우저 전용 저장소로 수신 중입니다.")
            : window.t("Receiving in memory.", "메모리로 수신 중입니다.");
      },
      onProgress(progress) {
        if (generation !== receiverGeneration) return;
        const total = progress.total || 0;
        recvProgressBar.value = total === 0 ? 100 : Math.floor((progress.received / total) * 100);
        recvProgressText.textContent = formatBytes(progress.received) + " / " + formatBytes(total);
      },
      async onFile(file, value, sink) {
        if (generation === receiverGeneration) await addReceivedFile(file, value, sink);
      },
      onComplete(result) {
        if (generation !== receiverGeneration) return;
        recvFinished = true;
        receiveResumePanel.hidden = true;
        recvProgressBar.value = 100;
        recvProgressText.textContent = window.t(
          result.files === 1 ? "Received 1 file" : `Received ${result.files} files`,
          result.files === 1 ? "파일 1개 수신 완료" : `파일 ${result.files}개 수신 완료`,
        );
      },
      onError(error) {
        showReceiverResume(generation, error);
      },
    });
    receiverTransfer = transfer;
    transfer.done.catch(() => {});
    dc.onerror = () => showReceiverResume(generation);
  }

  function applyReceiverOffer(parsed) {
    const preserveTransfer = receiverHasContext;
    receiverHasContext = true;
    startReceiver(parsed.code, parsed.sid, { preserveTransfer });
  }

  applyResumeOfferBtn.addEventListener("click", () => {
    clearErr();
    const parsed = parseResumeOfferInput(resumeOfferInput.value);
    if (!parsed) {
      window.showErr(errEl, window.t("Paste a valid sender link or offer code.", "올바른 보내는 쪽 링크나 제안 코드를 붙여 넣으세요."));
      return;
    }
    const nextHash = `#r=${parsed.code}.${parsed.sid}`;
    if (location.hash === nextHash) applyReceiverOffer(parsed);
    else location.hash = nextHash;
  });

  abortReceiveBtn.addEventListener("click", async () => {
    const transfer = receiverTransfer;
    const channel = receiverDataChannel;
    const pc = receiverPc;
    receiverGeneration++;
    receiverTransfer = null;
    receiverDataChannel = null;
    receiverPc = null;
    try { await transfer?.abort(); } catch {}
    transfer?.dispose();
    try { channel?.close(); } catch {}
    try { pc?.close(); } catch {}
    receiverHasContext = false;
    recvFinished = false;
    receiveResumePanel.hidden = true;
    cleanupReceivedFiles();
    receivedFiles.hidden = true;
    receivedFilesList.innerHTML = "";
    recvProgressWrap.hidden = true;
    replyLinkOut.value = "";
    replyCodeOut.value = "";
    chooseFolderBtn.disabled = false;
    recvStatus.textContent = window.t("Transfer cancelled", "전송 취소됨");
    clearErr();
  });

  window.addEventListener("pagehide", () => {
    cleanupReceivedFiles();
    senderTransfer?.dispose();
    if (!recvFinished) receiverTransfer?.abort().catch(() => {});
    receiverTransfer?.dispose();
    if (senderPc) {
      try {
        senderPc.close();
      } catch (e) {
        // ignore
      }
    }
    if (senderChannel) {
      try {
        senderChannel.close();
      } catch (e) {
        // ignore
      }
    }
    if (receiverPc) {
      try {
        receiverPc.close();
      } catch (e) {
        // ignore
      }
    }
  });

  /* -------------------------------------------------------- clipboard --- */

  async function copyText(text) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch (e) {
      return false;
    }
  }

  copyLinkBtn.addEventListener("click", () => {
    offerLinkInput.select();
    copyText(offerLinkInput.value);
  });
  copyReplyLinkBtn.addEventListener("click", () => {
    replyLinkOut.select();
    copyText(replyLinkOut.value);
  });
  copyCodeBtn.addEventListener("click", () => {
    replyCodeOut.select();
    copyText(replyCodeOut.value);
  });
  copyRelayCodeBtn.addEventListener("click", () => {
    relayCodeOut.select();
    copyText(relayCodeOut.value);
  });

  /* ------------------------------------------------------------- boot --- */

  function handleHashChange() {
    const parsed = parseHash(location.hash || "");
    if (parsed && parsed.kind === "r") {
      applyReceiverOffer(parsed);
    } else if (parsed && parsed.kind === "a") {
      startRelay(parsed.code, parsed.sid);
    }
  }
  window.addEventListener("hashchange", handleHashChange);
  handleHashChange();
}
