import { test } from "node:test";
import assert from "node:assert/strict";
import { harness, sender, receiver, invitation } from "./harness.mjs";

test("authorization, one receiver claim, immutable PAKE messages and plan limits", async () => {
  const h = await harness();
  try {
    assert.equal((await h.call("/health")).status, 200);
    assert.equal((await h.call("/v1/rooms", { method: "POST", token: "wrong", data: {} })).status, 401);
    assert.equal((await h.call("/v1/rooms", { method: "POST", token: invitation,
      data: { token: sender, fileSize: 10 * 1024 ** 3 + 1, chunkSize: 16 * 1024 ** 2, chunkCount: 641 } })).status, 400);
    assert.equal((await h.call("/v1/rooms", { method: "POST", token: invitation,
      data: { token: sender, fileSize: 1, chunkSize: 65536, chunkCount: 2 } })).status, 400);
    const room = await h.create();
    const root = `/v1/rooms/${room.room}`;
    assert.equal((await h.call(root + "/state", { token: "x".repeat(43) })).status, 401);
    assert.equal((await h.call(root + "/join", { method: "POST", data: { token: receiver } })).status, 200);
    assert.equal((await h.call(root + "/join", { method: "POST", data: { token: receiver } })).status, 200, "idempotent same-token join");
    assert.equal((await h.call(root + "/join", { method: "POST", data: { token: "x".repeat(43) } })).status, 409);
    const message = { name: "spake", data: "YQ==" };
    assert.equal((await h.call(root + "/message", { method: "POST", data: message })).status, 200);
    assert.equal((await h.call(root + "/message", { method: "POST", data: message })).status, 200);
    assert.equal((await h.call(root + "/message", { method: "POST", data: { ...message, data: "Yg==" } })).status, 409);
    assert.equal((await h.call(root + "/message", { method: "POST", data: { name: "done", data: "YQ==" } })).status, 400);
    assert.equal((await h.call(root + "/url", { method: "POST", data: { index: 0, method: "PUT" } })).status, 409);
    assert.equal((await h.call(`/v1/rooms/${room.room}/internal/create`, { method: "POST", data: {} })).status, 404);
  } finally { await h.close(); }
});

test("10 GiB boundary and a final chunk beyond the 32-bit offset range", async () => {
  const h = await harness();
  const limit = 10 * 1024 ** 3;
  const chunkSize = 16 * 1024 ** 2;
  try {
    assert.equal((await (await h.call("/health")).json()).maxFileBytes, limit);
    const room = await h.create({ fileSize: limit, chunkSize, chunkCount: 640 });
    await h.paired(room);
    const response = await h.call(`/v1/rooms/${room.room}/url`, { method: "POST", data: { index: 639, method: "PUT" } });
    assert.equal(response.status, 200);
    assert.equal((await h.call(`/v1/rooms/${room.room}/url`, { method: "POST", data: { index: 640, method: "PUT" } })).status, 400);
    assert.equal((await h.call("/v1/rooms", { method: "POST", token: invitation,
      data: { token: sender, fileSize: limit, chunkSize: 1024 ** 2, chunkCount: 10240 } })).status, 400);

    // Exercise the real object length/ready/read path at offset 10 GiB - 16 MiB,
    // without uploading the preceding 639 chunks or allocating a 10 GiB buffer.
    const tail = await h.create({ fileSize: limit - chunkSize + 3, chunkSize, chunkCount: 640 });
    await h.paired(tail);
    const root = `/v1/rooms/${tail.room}`;
    const payload = new Uint8Array(3 + 28).fill(71);
    assert.equal((await h.call(root + "/objects/639", { method: "PUT", body: payload })).status, 200);
    assert.equal((await h.call(root + "/ready/639", { method: "POST", data: {} })).status, 200);
    const state = await (await h.call(root + "/state")).json();
    assert.equal(state.fileSize, limit - chunkSize + 3);
    assert.deepEqual(state.ready, [639]);
    const downloaded = await h.call(root + "/objects/639", { token: receiver });
    assert.equal(downloaded.status, 200);
    assert.deepEqual(new Uint8Array(await downloaded.arrayBuffer()), payload);
    assert.equal((await h.call(root, { method: "DELETE" })).status, 200);
    assert.equal((await (await h.mf.getR2Bucket("FILES")).list()).objects.length, 0);
  } finally { await h.close(); }
});

test("configured file limit is honored, defaults to 10 GiB and cannot exceed it", async () => {
  const limit = 10 * 1024 ** 3;
  for (const [setting, expected] of [[undefined, limit], [String(20 * 1024 ** 3), limit], [String(1024 ** 3), 1024 ** 3]]) {
    const h = await harness({ MAX_FILE_BYTES: setting });
    try {
      assert.equal((await (await h.call("/health")).json()).maxFileBytes, expected);
      const chunkSize = 16 * 1024 ** 2;
      await h.create({ fileSize: expected, chunkSize, chunkCount: Math.ceil(expected / chunkSize) });
      const response = await h.call("/v1/rooms", { method: "POST", token: invitation,
        data: { token: sender, fileSize: expected + 1, chunkSize, chunkCount: Math.ceil((expected + 1) / chunkSize) } });
      assert.equal(response.status, 400);
      assert.match((await response.json()).error, new RegExp(`maximum ${expected} bytes`));
    } finally { await h.close(); }
  }
});

test("streaming chunk grant, ready checks, receiver reads and cleanup", async () => {
  const h = await harness();
  try {
    const room = await h.create(); await h.paired(room);
    const root = `/v1/rooms/${room.room}`;
    assert.equal((await h.call(root + "/url", { method: "POST", token: receiver, data: { index: 0, method: "PUT" } })).status, 403);
    assert.equal((await h.call(root + "/url", { method: "POST", data: { index: 1, method: "PUT" } })).status, 400);
    assert.equal((await h.call(root + "/ready/0", { method: "POST", data: {} })).status, 409);
    const object = root + "/objects/0";
    assert.equal((await h.call(object, { method: "PUT", body: new Uint8Array(10) })).status, 400);
    const payload = new Uint8Array(65536 + 28).fill(71);
    assert.equal((await h.call(object, { method: "PUT", body: payload })).status, 200);
    assert.equal((await h.call(root + "/ready/0", { method: "POST", data: {} })).status, 200);
    const downloaded = await h.call(object, { token: receiver });
    assert.equal(downloaded.status, 200);
    assert.deepEqual(new Uint8Array(await downloaded.arrayBuffer()), payload);
    const bucket = await h.mf.getR2Bucket("FILES");
    assert.equal((await bucket.list()).objects.length, 1);
    assert.equal((await h.call(root, { method: "DELETE", token: receiver })).status, 403);
    assert.equal((await h.call(root, { method: "DELETE" })).status, 200);
    assert.equal((await bucket.list()).objects.length, 0);
    assert.equal((await h.call(root + "/join", { method: "POST", data: { token: receiver } })).status, 410);
  } finally { await h.close(); }
});

test("direct R2 upload URLs sign both content length and content type", async () => {
  const h = await harness({ DIRECT_R2: "true", R2_ACCOUNT_ID: "a".repeat(32), R2_ACCESS_KEY_ID: "test-key", R2_SECRET_ACCESS_KEY: "test-secret" });
  try {
    const room = await h.create({ fileSize: 10 * 1024 ** 3, chunkSize: 16 * 1024 ** 2, chunkCount: 640 }); await h.paired(room);
    const response = await h.call(`/v1/rooms/${room.room}/url`, { method: "POST", data: { index: 639, method: "PUT" } });
    assert.equal(response.status, 200);
    const grant = await response.json();
    const url = new URL(grant.url);
    assert.equal(grant.headers["content-length"], String(16 * 1024 ** 2 + 28));
    assert.equal(url.searchParams.get("X-Amz-Expires"), "120");
    assert.match(url.searchParams.get("X-Amz-SignedHeaders"), /content-length/);
    assert.match(url.searchParams.get("X-Amz-SignedHeaders"), /content-type/);
    assert.equal((await h.call(`/v1/rooms/${room.room}/objects/0`, { method: "PUT", body: new Uint8Array(65564) })).status, 404);
  } finally { await h.close(); }
});

test("incomplete production configuration fails closed", async () => {
  const h = await harness({ DIRECT_R2: "true" });
  try {
    assert.equal((await (await h.call("/health")).json()).ready, false);
    assert.equal((await h.call("/v1/rooms", { method: "POST", data: {} })).status, 503);
  } finally { await h.close(); }
});
