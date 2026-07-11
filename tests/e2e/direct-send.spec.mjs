import { expect, test } from "@playwright/test";

test("Direct Send exposes mutable status and limit messages as live regions", async ({ page }) => {
  await page.goto("/send/");
  for (const id of [
    "status",
    "pickHint",
    "offerStatus",
    "sendProgressText",
    "recvStatus",
    "receiveDestination",
    "recvProgressText",
    "relayStatus",
  ]) {
    const element = page.locator(`#${id}`);
    await expect(element).toHaveAttribute("role", "status");
    await expect(element).toHaveAttribute("aria-live", "polite");
  }
  await expect(page.locator("#err")).toHaveAttribute("role", "alert");
  await expect(page.locator("#err")).toHaveAttribute("aria-live", "assertive");
});

test("Direct Send OPFS storage worker writes and releases a real file", async ({ page, browserName }) => {
  test.skip(browserName === "webkit", "WebKit does not provide reliable native OPFS handles");
  await page.goto("/send/");
  const result = await page.evaluate(async () => {
    const { createReceiveSink } = await import("/send/storage.mjs");
    const sink = await createReceiveSink({
      storage: navigator.storage,
      storageWorkerTimeoutMs: 5000,
    });
    const file = {
      id: "worker-e2e",
      name: `worker-${crypto.randomUUID()}.txt`,
      size: 6,
      type: "text/plain",
    };
    await sink.prepare([file]);
    await sink.write(file, 0, new TextEncoder().encode("worker"));
    const handle = await sink.finish(file);
    const text = await (await handle.getFile()).text();
    await sink.release(file);
    return { kind: sink.kind, workerBacked: sink.workerBacked, text };
  });

  expect(result).toEqual({ kind: "opfs", workerBacked: true, text: "worker" });
});

test("sender retry controls recreate an offer and keep the same selected transfer", async ({ page }) => {
  await page.goto("/send/");
  await page.locator("#fileDrop input[type=file]").setInputFiles({
    name: "resume.txt",
    mimeType: "text/plain",
    buffer: Buffer.from("resume me"),
  });
  await expect(page.locator("#senderPanel")).toBeVisible();
  await expect(page.locator("#offerLink")).not.toHaveValue("");
  const firstOffer = await page.locator("#offerLink").inputValue();
  await expect(page.locator("#resumeSendBtn")).toBeHidden();

  await page.locator("#answerCode").fill("uZm9v");
  await page.locator("#connectBtn").click();
  await expect(page.locator("#resumeSendBtn")).toBeVisible();
  await expect(page.locator("#resumeSendHint")).toContainText("same receiving tab");
  await page.locator("#resumeSendBtn").click();

  await expect.poll(() => page.locator("#offerLink").inputValue()).not.toBe(firstOffer);
  await expect(page.locator("#resumeSendBtn")).toBeHidden();
  await expect(page.locator("#cancelSendBtn")).toBeVisible();
  await page.locator("#cancelSendBtn").click();
  await expect(page.locator("#senderPanel")).toBeHidden();
});

test("receiver applies a replacement offer through hashchange in the same tab", async ({ page }) => {
  await page.goto("/send/#r=uZm9v.deadbeef");
  await expect(page.locator("#receiveResumePanel")).toBeVisible();
  await expect(page.locator("#receiveResumeHint")).toContainText("same receiving tab");
  await page.evaluate(() => {
    const item = document.createElement("li");
    item.id = "retainedReceivedItem";
    item.textContent = "already received";
    document.getElementById("receivedFilesList").appendChild(item);
    document.getElementById("receivedFiles").hidden = false;
  });
  const replacementLink = await page.evaluate(async () => {
    const { encodeSdp } = await import("/send/send.mjs");
    const pc = new RTCPeerConnection({ iceServers: [] });
    pc.createDataChannel("replacement");
    await pc.setLocalDescription(await pc.createOffer());
    if (pc.iceGatheringState !== "complete") {
      await new Promise((resolve) => {
        const onChange = () => {
          if (pc.iceGatheringState !== "complete") return;
          pc.removeEventListener("icegatheringstatechange", onChange);
          resolve();
        };
        pc.addEventListener("icegatheringstatechange", onChange);
      });
    }
    window.__replacementOfferPeer = pc;
    const code = await encodeSdp(pc.localDescription.sdp);
    return `${location.origin}${location.pathname}#r=${code}.cafebabe`;
  });

  await page.locator("#resumeOfferInput").fill(replacementLink);
  await page.locator("#applyResumeOfferBtn").click();
  await expect(page).toHaveURL(/#r=.+\.cafebabe$/);
  await expect(page.locator("#replyCodeOut")).not.toHaveValue("");
  await expect(page.locator("#retainedReceivedItem")).toHaveText("already received");
  await expect(page.locator("#receiveResumePanel")).toBeHidden();
  await expect(page.locator("#abortReceiveBtn")).toBeVisible();
});

test("Direct Send v3 transfers multiple files over a real data channel", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/send/");
  const result = await page.evaluate(async () => {
    const { createReceiverSession, createSenderSession } = await import("/send/transfer.mjs");
    const { MemoryReceiveSink } = await import("/send/storage.mjs");
    const senderPC = new RTCPeerConnection({ iceServers: [] });
    const receiverPC = new RTCPeerConnection({ iceServers: [] });
    senderPC.onicecandidate = ({ candidate }) => { if (candidate) receiverPC.addIceCandidate(candidate); };
    receiverPC.onicecandidate = ({ candidate }) => { if (candidate) senderPC.addIceCandidate(candidate); };
    const channel = senderPC.createDataChannel("files");
    const outputs = [];
    let receiver;
    receiverPC.ondatachannel = ({ channel: incoming }) => {
      receiver = createReceiverSession(incoming, {
        sinkFactory: async () => new MemoryReceiveSink({ maxBytes: 1024 }),
        onFile: async (file, value) => outputs.push([file.name, await value.text()]),
      });
    };
    const offer = await senderPC.createOffer();
    await senderPC.setLocalDescription(offer);
    await receiverPC.setRemoteDescription(offer);
    const answer = await receiverPC.createAnswer();
    await receiverPC.setLocalDescription(answer);
    await senderPC.setRemoteDescription(answer);
    const sender = createSenderSession(channel, [
      new File(["alpha"], "a.txt", { type: "text/plain" }),
      new File(["beta"], "b.txt", { type: "text/plain" }),
    ], { transferId: "browser-e2e", chunkSize: 3, negotiationTimeoutMs: 2000 });
    const sent = await sender.start();
    while (!receiver) await new Promise((resolve) => setTimeout(resolve, 0));
    const received = await receiver.done;
    sender.dispose();
    receiver.dispose();
    senderPC.close();
    receiverPC.close();
    return { sent, received, outputs };
  });

  expect(result.sent).toEqual({ transferId: "browser-e2e", files: 2, bytes: 9 });
  expect(result.received).toEqual({ transferId: "browser-e2e", files: 2, bytes: 9 });
  expect(result.outputs).toEqual([["a.txt", "alpha"], ["b.txt", "beta"]]);
  expect(errors).toEqual([]);
});

test("Direct Send reconnects real data channels and resumes verified bytes", async ({ page }) => {
  await page.goto("/send/");
  const result = await page.evaluate(async () => {
    const {
      createReceiverSession,
      createSenderSession,
      ReceiveTransferStore,
    } = await import("/send/transfer.mjs");
    const { MemoryReceiveSink } = await import("/send/storage.mjs");
    const store = new ReceiveTransferStore({ expiryMs: 5000 });
    const sink = new MemoryReceiveSink({ maxBytes: 32 });
    const source = new File(["abcdefgh"], "resume.bin", { type: "application/octet-stream" });
    const outputs = [];

    async function waitForIce(pc) {
      if (pc.iceGatheringState === "complete") return;
      await new Promise((resolve) => {
        const onChange = () => {
          if (pc.iceGatheringState !== "complete") return;
          pc.removeEventListener("icegatheringstatechange", onChange);
          resolve();
        };
        pc.addEventListener("icegatheringstatechange", onChange);
      });
    }

    // v3 has no per-chunk ack to hang a "disconnect partway through the
    // file" test off of — the sender just streams chunks back-to-back. To
    // deterministically interrupt after exactly one verified chunk, the
    // second binary chunk send is intercepted and dropped outright (so it
    // never reaches the wire at all), and the actual disconnect waits for
    // the receiver's onProgress to confirm the first chunk was durably
    // written before tearing the connections down.
    async function openConnection({ disconnectAfterFirstChunk, sinkFactory, onFile }) {
      const senderPC = new RTCPeerConnection({ iceServers: [] });
      const receiverPC = new RTCPeerConnection({ iceServers: [] });
      const channel = senderPC.createDataChannel("resume");
      const observed = { resumeOffset: null, fileStartOffsets: [] };
      let receiver = null;
      let firstChunkWritten = null;
      const firstChunkWrittenPromise = disconnectAfterFirstChunk
        ? new Promise((resolve) => { firstChunkWritten = resolve; })
        : null;

      if (disconnectAfterFirstChunk) {
        const originalSend = channel.send.bind(channel);
        let binarySent = 0;
        channel.send = (value) => {
          if (value instanceof ArrayBuffer && ++binarySent === 2) return;
          originalSend(value);
        };
      }

      channel.addEventListener("message", ({ data }) => {
        if (typeof data !== "string") return;
        const message = JSON.parse(data);
        if (message.type === "resume") observed.resumeOffset = message.offsets.f0;
      });
      const sender = createSenderSession(channel, [source], {
        chunkSize: 4,
        transferId: "browser-real-resume",
        negotiationTimeoutMs: 2000,
      });
      receiverPC.ondatachannel = ({ channel: incoming }) => {
        incoming.addEventListener("message", ({ data }) => {
          if (typeof data !== "string") return;
          const message = JSON.parse(data);
          if (message.type === "file-start") observed.fileStartOffsets.push(message.offset);
        });
        receiver = createReceiverSession(incoming, {
          transferStore: store,
          sinkFactory,
          onFile,
          onProgress: disconnectAfterFirstChunk ? () => firstChunkWritten?.() : undefined,
        });
      };

      const offer = await senderPC.createOffer();
      await senderPC.setLocalDescription(offer);
      await waitForIce(senderPC);
      await receiverPC.setRemoteDescription(senderPC.localDescription);
      const answer = await receiverPC.createAnswer();
      await receiverPC.setLocalDescription(answer);
      await waitForIce(receiverPC);
      await senderPC.setRemoteDescription(receiverPC.localDescription);
      while (!receiver) await new Promise((resolve) => setTimeout(resolve, 0));
      return { senderPC, receiverPC, sender, get receiver() { return receiver; }, observed, firstChunkWrittenPromise };
    }

    const first = await openConnection({
      disconnectAfterFirstChunk: true,
      sinkFactory: async () => sink,
    });
    const firstSendPromise = first.sender.start().then(
      () => ({ ok: true }),
      (error) => ({ ok: false, error: error.message }),
    );
    const firstReceivePromise = first.receiver.done.then(
      () => ({ ok: true }),
      (error) => ({ ok: false, error: error.message }),
    );
    await first.firstChunkWrittenPromise;
    first.senderPC.close();
    first.receiverPC.close();
    const firstSend = await firstSendPromise;
    const firstReceive = await firstReceivePromise;
    first.sender.dispose();
    first.receiver.dispose();

    const second = await openConnection({
      disconnectAfterFirstChunk: false,
      sinkFactory: async () => { throw new Error("resume created a second sink"); },
      onFile: async (file, value) => outputs.push([file.name, await value.text()]),
    });
    const sent = await second.sender.start();
    const received = await second.receiver.done;
    second.sender.dispose();
    second.receiver.dispose();
    second.senderPC.close();
    second.receiverPC.close();

    return {
      firstSend,
      firstReceive,
      resumeOffset: second.observed.resumeOffset,
      fileStartOffsets: second.observed.fileStartOffsets,
      sent,
      received,
      outputs,
    };
  });

  expect(result.firstSend.ok).toBe(false);
  expect(result.firstReceive.ok).toBe(false);
  expect(result.resumeOffset).toBe(4);
  expect(result.fileStartOffsets).toEqual([4]);
  expect(result.sent.bytes).toBe(8);
  expect(result.received.bytes).toBe(8);
  expect(result.outputs).toEqual([["resume.bin", "abcdefgh"]]);
});
