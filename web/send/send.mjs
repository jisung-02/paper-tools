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

/* ------------------------------------------------------------- codec --- */

// Compressed SDP blobs are prefixed "c", the plain-base64url fallback (used
// when CompressionStream isn't available, e.g. old Safari) is prefixed "u".
// Both are safe to put after a URL "#" fragment and inside a QR code.

const B64_CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

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
async function pipeBytes(bytes, transform) {
  const writer = transform.writable.getWriter();
  writer.write(bytes);
  writer.close();
  const chunks = [];
  for await (const chunk of transform.readable) {
    chunks.push(chunk);
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
  if (prefix === "c") {
    if (typeof DecompressionStream !== "function") {
      throw new Error("this code needs a browser feature that isn't available here");
    }
    const raw = await pipeBytes(base64UrlToBytes(payload), new DecompressionStream("deflate-raw"));
    return new TextDecoder().decode(raw);
  }
  if (prefix === "u") {
    return new TextDecoder().decode(base64UrlToBytes(payload));
  }
  throw new Error("invalid code");
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

/* ------------------------------------------------------- page wiring --- */
// Everything below touches the DOM/window/RTCPeerConnection and only runs
// in a real browser tab (guarded so send.test.mjs can import the codec
// above under plain Node without a `document` global).

if (typeof document !== "undefined") {
  main();
}

function main() {
  const CHUNK_SIZE = 64 * 1024;
  const BUFFERED_LOW_THRESHOLD = 1_000_000; // 1MB
  const BUFFERED_HIGH_WATERMARK = 8_000_000; // 8MB
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

  const recvStatus = document.getElementById("recvStatus");
  const replyLinkOut = document.getElementById("replyLinkOut");
  const copyReplyLinkBtn = document.getElementById("copyReplyLinkBtn");
  const replyCodeOut = document.getElementById("replyCodeOut");
  const copyCodeBtn = document.getElementById("copyCodeBtn");
  const qrReply = document.getElementById("qrReply");
  const recvProgressWrap = document.getElementById("recvProgressWrap");
  const recvProgressBar = document.getElementById("recvProgressBar");
  const recvProgressText = document.getElementById("recvProgressText");
  const downloadBtn = document.getElementById("downloadBtn");

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
  function wireConnectionState(pc, statusEl2, onFail) {
    let timeoutId = null;
    let failed = false;

    function clearTimer() {
      if (timeoutId) {
        clearTimeout(timeoutId);
        timeoutId = null;
      }
    }

    function fail() {
      if (failed) return;
      failed = true;
      clearTimer();
      onFail();
    }

    function update() {
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
          const state = pc.iceConnectionState;
          if (state !== "connected" && state !== "completed") fail();
        }, CONNECT_TIMEOUT_MS);
      },
    };
  }

  /* --------------------------------------------------------- sender --- */

  let senderPc = null;
  let senderChannel = null;

  function resetSender() {
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
    senderPanel.hidden = true;
    sendProgressWrap.hidden = true;
    sendProgressBar.value = 0;
    answerCodeInput.value = "";
    offerStatus.textContent = "";
  }

  async function startSender(file) {
    clearErr();
    resetSender();
    senderPanel.hidden = false;

    let pc;
    try {
      pc = new RTCPeerConnection({ iceServers: [] });
      senderPc = pc;
      const dc = pc.createDataChannel("file");
      wireSendChannel(dc, file);

      const conn = wireConnectionState(pc, offerStatus, showFailure);

      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      await waitIceGatheringComplete(pc);

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
        if (answered) return;
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
          showFailure();
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
      showFailure();
    }
  }

  function wireSendChannel(dc, file) {
    dc.binaryType = "arraybuffer";
    dc.bufferedAmountLowThreshold = BUFFERED_LOW_THRESHOLD;

    let sent = false;
    let closed = false;

    dc.onopen = async () => {
      try {
        sendProgressWrap.hidden = false;
        dc.send(JSON.stringify({ name: file.name, size: file.size, type: file.type || "application/octet-stream" }));

        let offset = 0;
        while (offset < file.size) {
          if (dc.bufferedAmount > BUFFERED_HIGH_WATERMARK) {
            await new Promise((resolve) => {
              dc.addEventListener("bufferedamountlow", resolve, { once: true });
              dc.addEventListener("close", resolve, { once: true });
            });
            if (closed) return;
          }
          const slice = file.slice(offset, offset + CHUNK_SIZE);
          const buf = await slice.arrayBuffer();
          dc.send(buf);
          offset += buf.byteLength;
          sendProgressBar.value = Math.floor((offset / file.size) * 100);
          sendProgressText.textContent = formatBytes(offset) + " / " + formatBytes(file.size);
        }

        dc.send("done");
        while (dc.bufferedAmount > 0) {
          if (closed) return;
          await new Promise((resolve) => setTimeout(resolve, 50));
        }
        sent = true;
        sendProgressBar.value = 100;
        sendProgressText.textContent = window.t("Sent", "전송 완료");
      } catch (e) {
        if (!closed) showFailure();
      }
    };
    dc.onerror = () => showFailure();
    dc.onclose = () => {
      closed = true;
      if (!sent) showFailure();
    };
  }

  document.getElementById("fileDrop").addEventListener("dz:files", (e) => {
    const files = e.detail.files;
    if (files.length === 0) return;
    if (files.length > 1) {
      pickHint.hidden = false;
      resetSender();
      return;
    }
    pickHint.hidden = true;
    startSender(files[0]);
  });

  /* ------------------------------------------------------- receiver --- */

  let receiverPc = null;
  let recvFinished = false;
  let recvObjectUrl = null;

  function revokeRecvUrl() {
    if (recvObjectUrl) {
      URL.revokeObjectURL(recvObjectUrl);
      recvObjectUrl = null;
    }
  }

  async function startReceiver(offerCode, sid) {
    senderView.hidden = true;
    receiverView.hidden = false;
    clearErr();

    let pc;
    try {
      // A "#r=" link with no sid is malformed (every offer link this page
      // generates includes one) — treat it the same as an invalid code.
      if (!sid) throw new Error("invalid code");
      const offerSdp = await decodeSdp(offerCode);
      pc = new RTCPeerConnection({ iceServers: [] });
      receiverPc = pc;

      const conn = wireConnectionState(pc, recvStatus, showFailure);

      pc.ondatachannel = (ev) => wireRecvChannel(ev.channel);

      await pc.setRemoteDescription({ type: "offer", sdp: offerSdp });
      const answer = await pc.createAnswer();
      await pc.setLocalDescription(answer);
      await waitIceGatheringComplete(pc);

      const code = await encodeSdp(pc.localDescription.sdp);
      const link = buildAnswerLink(location.origin, location.pathname, code, sid);
      replyLinkOut.value = link;
      // Kept as the bare code (no sid suffix) so the manual-paste path on
      // the sender side is unchanged — applyAnswer only ever takes a code.
      replyCodeOut.value = code;
      renderQr(qrReply, link);

      conn.startConnectTimeout();
    } catch (e) {
      showFailure();
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

  function wireRecvChannel(dc) {
    dc.binaryType = "arraybuffer";
    let meta = null;
    let received = 0;
    const chunks = [];

    recvProgressWrap.hidden = false;

    dc.onmessage = (ev) => {
      if (typeof ev.data === "string") {
        if (!meta) {
          try {
            meta = JSON.parse(ev.data);
          } catch (e) {
            // ignore a malformed metadata frame; a well-formed one may
            // still arrive, and the binary chunks are buffered regardless
          }
          return;
        }
        if (ev.data === "done") finalizeDownload(chunks, meta);
        return;
      }

      chunks.push(new Uint8Array(ev.data));
      received += ev.data.byteLength;
      const total = meta && meta.size ? meta.size : received;
      recvProgressBar.value = Math.floor((received / total) * 100);
      recvProgressText.textContent = formatBytes(received) + " / " + formatBytes(total);
      if (meta && received >= meta.size) finalizeDownload(chunks, meta);
    };
    dc.onerror = () => showFailure();
    dc.onclose = () => {
      if (!recvFinished) showFailure();
    };
  }

  function finalizeDownload(chunks, meta) {
    if (recvFinished) return;
    recvFinished = true;

    const blob = new Blob(chunks, { type: (meta && meta.type) || "application/octet-stream" });
    revokeRecvUrl();
    recvObjectUrl = URL.createObjectURL(blob);

    recvProgressBar.value = 100;
    recvProgressText.textContent = window.t("Received", "수신 완료");

    downloadBtn.hidden = false;
    downloadBtn.onclick = () => {
      const a = document.createElement("a");
      a.href = recvObjectUrl;
      a.download = (meta && meta.name) || "download";
      document.body.appendChild(a);
      a.click();
      a.remove();
    };
  }

  window.addEventListener("pagehide", () => {
    revokeRecvUrl();
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

  const parsed = parseHash(location.hash || "");
  if (parsed && parsed.kind === "r") {
    startReceiver(parsed.code, parsed.sid);
  } else if (parsed && parsed.kind === "a") {
    startRelay(parsed.code, parsed.sid);
  }
}
