import assert from "node:assert/strict";
import { test } from "node:test";

test("starting a latest-only action aborts and invalidates the previous action", async () => {
  const module = await import("./visual-state.mjs").catch(() => ({}));
  assert.equal(typeof module.createLatestActionController, "function");
  const actions = module.createLatestActionController();
  const first = actions.begin();
  const second = actions.begin();

  assert.equal(first.signal.aborted, true);
  assert.equal(first.isCurrent(), false);
  assert.equal(second.isCurrent(), true);
  assert.equal(actions.finish(first), false);
  assert.equal(actions.finish(second), true);
});

test("cancelling invalidates an action immediately but completion waits for its finally block", async () => {
  const module = await import("./visual-state.mjs").catch(() => ({}));
  const actions = module.createLatestActionController();
  const token = actions.begin();
  let completed = false;
  token.completion.then(() => { completed = true; });

  assert.equal(actions.cancel(), true);
  await Promise.resolve();
  assert.equal(token.signal.aborted, true);
  assert.equal(completed, false);

  assert.equal(actions.finish(token), false);
  await token.completion;
  assert.equal(completed, true);
});

test("changed-page collection handles zero, first and last changes", async () => {
  const module = await import("./visual-state.mjs").catch(() => ({}));
  assert.equal(typeof module.collectChangedPages, "function");
  assert.equal(typeof module.changedPageNavigation, "function");

  const changed = await module.collectChangedPages(4, async (index) => ({
    changedPixels: index === 0 || index === 3 ? 1 : 0,
    pageSizeChanged: false,
  }));
  assert.deepEqual(changed, [0, 3]);
  assert.deepEqual(module.changedPageNavigation(changed, 0), { previous: null, next: 3 });
  assert.deepEqual(module.changedPageNavigation(changed, 3), { previous: 0, next: null });
  assert.deepEqual(module.changedPageNavigation([], 0), { previous: null, next: null });
});

test("changed-page collection stops when a settings toggle aborts it", async () => {
  const module = await import("./visual-state.mjs").catch(() => ({}));
  assert.equal(typeof module.collectChangedPages, "function");
  const controller = new AbortController();
  let calls = 0;
  const pending = module.collectChangedPages(5, async () => {
    calls++;
    controller.abort();
    return { changedPixels: 0, pageSizeChanged: false };
  }, { signal: controller.signal });

  await assert.rejects(pending, (error) => error?.name === "AbortError");
  assert.equal(calls, 1);
});

test("a closed export commits only for the latest token and cleans stale output", async () => {
  const module = await import("./visual-state.mjs").catch(() => ({}));
  assert.equal(typeof module.commitLatestClosedOutput, "function");
  const actions = module.createLatestActionController();
  const stale = actions.begin();
  const latest = actions.begin();
  const events = [];
  const sink = { async cleanup() { events.push("cleanup"); } };

  assert.equal(await module.commitLatestClosedOutput({
    token: stale,
    sink,
    output: new Blob(["stale"]),
    commit() { events.push("stale-commit"); },
  }), false);
  assert.deepEqual(events, ["cleanup"]);

  assert.equal(await module.commitLatestClosedOutput({
    token: latest,
    sink,
    output: new Blob(["latest"]),
    commit(output) { events.push(`commit:${output.size}`); },
  }), true);
  assert.deepEqual(events, ["cleanup", "commit:6"]);
});

test("input revision snapshots detect either dropzone replacement", async () => {
  const module = await import("./visual-state.mjs").catch(() => ({}));
  assert.equal(typeof module.snapshotInputRevisions, "function");
  assert.equal(typeof module.inputRevisionsMatch, "function");
  const dropzones = [{ __paperRevision: 3 }, { __paperRevision: 7 }];
  const snapshot = module.snapshotInputRevisions(dropzones);

  assert.deepEqual(snapshot, [3, 7]);
  assert.equal(module.inputRevisionsMatch(snapshot, dropzones), true);
  dropzones[1].__paperRevision++;
  assert.equal(module.inputRevisionsMatch(snapshot, dropzones), false);
  assert.throws(() => module.snapshotInputRevisions([{ __paperRevision: -1 }]), /revision/i);
});

test("download cleanup shares one timer/pagehide promise and isolates later generations", async () => {
  const module = await import("./visual-state.mjs").catch(() => ({}));
  assert.equal(typeof module.createDelayedCleanupRegistry, "function");
  const timers = new Map();
  const cleared = [];
  const revoked = [];
  let nextTimer = 1;
  const registry = module.createDelayedCleanupRegistry({
    setTimer(callback) {
      const id = nextTimer++;
      timers.set(id, callback);
      return id;
    },
    clearTimer(id) {
      cleared.push(id);
      timers.delete(id);
    },
    revokeObjectURL(url) { revoked.push(url); },
  });
  const removes = [];
  const firstCleanup = registry.schedule({
    url: "blob:first",
    cleanup: async () => { removes.push("first"); },
  });
  const firstTimer = timers.get(1);

  const pagehideCleanup = registry.cleanupAll();
  firstTimer();
  await Promise.all([pagehideCleanup, firstCleanup(), firstCleanup()]);
  assert.deepEqual(revoked, ["blob:first"]);
  assert.deepEqual(removes, ["first"]);
  assert.deepEqual(cleared, [1]);
  assert.equal(registry.pendingCount, 0);

  const secondCleanup = registry.schedule({
    url: "blob:second",
    cleanup: async () => { removes.push("second"); },
  });
  const secondTimer = timers.get(2);
  firstTimer();
  await Promise.resolve();
  assert.equal(registry.pendingCount, 1);
  assert.deepEqual(revoked, ["blob:first"]);
  assert.deepEqual(removes, ["first"]);

  await Promise.all([secondCleanup(), secondCleanup()]);
  secondTimer();
  assert.deepEqual(revoked, ["blob:first", "blob:second"]);
  assert.deepEqual(removes, ["first", "second"]);
  assert.deepEqual(cleared, [1, 2]);
  assert.equal(registry.pendingCount, 0);
});
