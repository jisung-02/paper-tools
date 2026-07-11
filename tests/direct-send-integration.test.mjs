import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";

test("Direct Send page exposes multi-file and receive-sink controls", async () => {
  const [html, source] = await Promise.all([
    readFile(new URL("../web/send/index.html", import.meta.url), "utf8"),
    readFile(new URL("../web/send/send.mjs", import.meta.url), "utf8"),
  ]);
  assert.match(html, /<input[^>]+type="file"[^>]+multiple/);
  assert.match(html, /id="chooseFolderBtn"/);
  assert.match(html, /id="receivedFilesList"/);
  assert.match(source, /createSenderSession/);
  assert.match(source, /createReceiverSession/);
  assert.match(source, /createReceiveSink/);
});
