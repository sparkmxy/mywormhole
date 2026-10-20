import { test } from "node:test";
import assert from "node:assert/strict";
import { harness, sender, receiver, invitation } from "./harness.mjs";

test("authorization, one receiver claim, immutable PAKE messages and plan limits", async () => {
  const h = await harness();
  try {
    assert.equal((await h.call("/health")).status, 200);
    assert.equal((await h.call("/v1/rooms", { method: "POST", token: "wrong", data: {} })).status, 401);
    assert.equal((await h.call("/v1/rooms", { method: "POST", token: invitation,
      data: { token: sender, fileSize: 3 * 1024 ** 3, chunkSize: 16 * 1024 ** 2, chunkCount: 192 } })).status, 400);
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
    const room = await h.create(); await h.paired(room);
    const response = await h.call(`/v1/rooms/${room.room}/url`, { method: "POST", data: { index: 0, method: "PUT" } });
    assert.equal(response.status, 200);
    const grant = await response.json();
    const url = new URL(grant.url);
    assert.equal(grant.headers["content-length"], "65564");
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
