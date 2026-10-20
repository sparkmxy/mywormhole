import { AwsClient } from "aws4fetch";

interface Env {
  ROOMS: DurableObjectNamespace;
  FILES: R2Bucket;
  INVITE_TOKEN: string;
  DIRECT_R2: string;
  R2_ACCOUNT_ID?: string;
  R2_ACCESS_KEY_ID?: string;
  R2_SECRET_ACCESS_KEY?: string;
  R2_BUCKET_NAME: string;
  ROOM_TTL_SECONDS: string;
  MAX_FILE_BYTES: string;
}
type Role = "sender" | "receiver";
interface Room {
  room: string;
  session: string;
  expiresAt: number;
  senderHash: string;
  receiverHash?: string;
  fileSize: number;
  chunkSize: number;
  chunkCount: number;
  messages: Record<Role, Record<string, string>>;
  ready: number[];
  closed: boolean;
}

const json = (data: unknown, status = 200) => Response.json(data, {
  status, headers: { "cache-control": "no-store", "x-content-type-options": "nosniff" },
});
function fail(status: number, message: string): never { throw new HTTPError(status, message); }
class HTTPError extends Error { constructor(readonly status: number, message: string) { super(message); } }
const tokenOK = (value: unknown): value is string => typeof value === "string" && /^[A-Za-z0-9_-]{43}$/.test(value);
const bearer = (request: Request) => request.headers.get("Authorization")?.replace(/^Bearer /, "") ?? "";
async function digest(value: string) {
  return Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value))))
    .map(v => v.toString(16).padStart(2, "0")).join("");
}
async function equalSecrets(a: string, b: string) {
  const [x, y] = await Promise.all([digest(a), digest(b)]);
  let difference = 0;
  for (let i = 0; i < x.length; i++) difference |= x.charCodeAt(i) ^ y.charCodeAt(i);
  return difference === 0;
}
async function body(request: Request): Promise<Record<string, unknown>> {
  // Bound streamed bodies too: Content-Length alone is controlled by the caller.
  if (!request.body) return {};
  const reader = request.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > 32768) { await reader.cancel(); fail(413, "Control message too large"); }
    chunks.push(value);
  }
  const bytes = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
  try {
    const result: unknown = JSON.parse(new TextDecoder().decode(bytes));
    if (!result || typeof result !== "object" || Array.isArray(result)) fail(400, "Expected JSON object");
    return result as Record<string, unknown>;
  } catch { return fail(400, "Invalid JSON"); }
}
function configured(env: Env) {
  return Boolean(env.INVITE_TOKEN && env.INVITE_TOKEN.length >= 24 &&
    (env.DIRECT_R2 !== "true" || (env.R2_ACCOUNT_ID && env.R2_ACCESS_KEY_ID && env.R2_SECRET_ACCESS_KEY)));
}
function roomStub(env: Env, room: string) { return env.ROOMS.get(env.ROOMS.idFromName(room)); }
function chunkLength(room: Room, index: number) {
  return Math.min(room.chunkSize, room.fileSize - index * room.chunkSize) + 28; // GCM nonce + tag
}
function objectKey(room: Room, index: number) { return `transfers/${room.session}/${index}`; }

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    try {
      const url = new URL(request.url);
      if (url.pathname === "/health" && request.method === "GET") {
        return json({ service: "mywormhole", protocol: 1, ready: configured(env), directR2: env.DIRECT_R2 === "true" });
      }
      if (!configured(env)) fail(503, "Deployment configuration is incomplete; see deployment guide");
      if (url.pathname === "/v1/rooms" && request.method === "POST") {
        if (!await equalSecrets(bearer(request), env.INVITE_TOKEN)) fail(401, "Invitation token required to send");
        const input = await body(request);
        if (!tokenOK(input.token)) fail(400, "Invalid sender token");
        const { fileSize, chunkSize, chunkCount } = input;
        const maxBytes = Math.min(Number(env.MAX_FILE_BYTES) || 2147483648, 2147483648);
        if (!Number.isSafeInteger(fileSize) || (fileSize as number) < 0 || (fileSize as number) > maxBytes ||
            !Number.isSafeInteger(chunkSize) || (chunkSize as number) < 65536 || (chunkSize as number) > 33554432 ||
            !Number.isSafeInteger(chunkCount) || chunkCount !== Math.ceil((fileSize as number) / (chunkSize as number)) ||
            (chunkCount as number) > 2048) fail(400, "Invalid file size or chunk plan (maximum 2 GiB / 2048 chunks)");
        for (let attempt = 0; attempt < 12; attempt++) {
          // Rejection sampling avoids bias in the six-digit locator.
          let value: number;
          do { value = crypto.getRandomValues(new Uint32Array(1))[0]; } while (value >= 4294800000);
          const locator = String(100000 + value % 900000);
          const result = await roomStub(env, locator).fetch(new Request("https://room/internal/create", {
            method: "POST", body: JSON.stringify({ ...input, room: locator }),
          }));
          if (result.status !== 409) return result;
        }
        fail(503, "Could not allocate a room; try again");
      }
      const match = /^\/v1\/rooms\/(\d{6})(\/.*)?$/.exec(url.pathname);
      if (!match) return json({ error: "Not found" }, 404);
      const [, locator, suffix = ""] = match;
      const stub = roomStub(env, locator);
      const object = /^\/objects\/(\d+)$/.exec(suffix);
      if (object) {
        if (env.DIRECT_R2 === "true") fail(404, "Use direct R2 URLs in this deployment");
        const index = Number(object[1]);
        const authorization = await stub.fetch(new Request("https://room/internal/object", {
          method: "POST", headers: { authorization: request.headers.get("authorization") ?? "" },
          body: JSON.stringify({ index, method: request.method }),
        }));
        if (!authorization.ok) return authorization;
        const grant = await authorization.json<{ key: string; length: number }>();
        if (request.method === "PUT") {
          if (request.headers.get("content-length") !== String(grant.length)) fail(400, "Incorrect encrypted chunk length");
          if (!request.body) fail(400, "Missing chunk body");
          await env.FILES.put(grant.key, request.body);
          return json({ ok: true });
        }
        const value = await env.FILES.get(grant.key);
        if (!value) fail(404, "Chunk not uploaded yet");
        return new Response(value.body, { headers: {
          "content-type": "application/octet-stream", "content-length": String(value.size), "cache-control": "no-store",
        } });
      }
      // The stub cannot be invoked from the Internet; only explicitly mapped routes are forwarded.
      if (!/^(?:\/join|\/state|\/message|\/url|\/ready\/\d+|\/abort)?$/.test(suffix)) fail(404, "Not found");
      const forward = new URL(request.url);
      forward.pathname = suffix || "/";
      return await stub.fetch(new Request(forward, request));
    } catch (error) {
      if (error instanceof HTTPError) return json({ error: error.message }, error.status);
      return json({ error: "Internal service error" }, 500);
    }
  },
};

export class TransferRoom {
  constructor(private readonly ctx: DurableObjectState, private readonly env: Env) {}

  async fetch(request: Request): Promise<Response> {
    // Catch validation errors inside the concurrency guard: an exception escaping
    // blockConcurrencyWhile resets the object and erases the HTTP error type.
    return this.ctx.blockConcurrencyWhile(async () => {
      try { return await this.handle(request); }
      catch (error) {
        if (error instanceof HTTPError) return json({ error: error.message }, error.status);
        return json({ error: "Room operation failed" }, 500);
      }
    });
  }

  private async handle(request: Request): Promise<Response> {
    const path = new URL(request.url).pathname;
    let room = await this.ctx.storage.get<Room>("room");
    if (path === "/internal/create" && request.method === "POST") {
      if (room) fail(409, "Locator is reserved");
      const input = await body(request);
      const ttl = Math.min(Math.max(Number(this.env.ROOM_TTL_SECONDS) || 7200, 60), 86400);
      room = {
        room: String(input.room), session: crypto.randomUUID(), expiresAt: Date.now() + ttl * 1000,
        senderHash: await digest(String(input.token)), fileSize: Number(input.fileSize),
        chunkSize: Number(input.chunkSize), chunkCount: Number(input.chunkCount),
        messages: { sender: {}, receiver: {} }, ready: [], closed: false,
      };
      await this.ctx.storage.put("room", room);
      await this.ctx.storage.setAlarm(room.expiresAt);
      return json({ room: room.room, session: room.session, expiresAt: room.expiresAt }, 201);
    }
    if (!room) fail(404, "Room not found");
    if (Date.now() >= room.expiresAt) fail(410, "Room expired; start a new transfer");
    if (room.closed) fail(410, "Room closed; start a new transfer");
    if (path === "/join" && request.method === "POST") {
      const input = await body(request);
      if (!tokenOK(input.token)) fail(400, "Invalid receiver token");
      const hash = await digest(input.token);
      if (room.receiverHash && !await equalSecrets(room.receiverHash, hash)) {
        fail(409, "A receiver already claimed this code; ask the sender for a new code");
      }
      room.receiverHash = hash;
      await this.ctx.storage.put("room", room);
      return json({ room: room.room, session: room.session, expiresAt: room.expiresAt });
    }
    const token = bearer(request);
    if (!tokenOK(token)) fail(401, "Room authorization required");
    const hash = await digest(token);
    let role: Role;
    if (await equalSecrets(hash, room.senderHash)) role = "sender";
    else if (room.receiverHash && await equalSecrets(hash, room.receiverHash)) role = "receiver";
    else return fail(401, "Invalid room token");
    const peer: Role = role === "sender" ? "receiver" : "sender";
    if (path === "/state" && request.method === "GET") {
      return json({ room: room.room, session: room.session, expiresAt: room.expiresAt,
        own: room.messages[role], peer: room.messages[peer], ready: room.ready,
        fileSize: room.fileSize, chunkSize: room.chunkSize, chunkCount: room.chunkCount });
    }
    if (path === "/message" && request.method === "POST") {
      const { name, data } = await body(request);
      const allowed = role === "sender" ? ["spake", "confirm", "offer"] : ["spake", "confirm", "accept", "done"];
      if (typeof name !== "string" || !allowed.includes(name) || typeof data !== "string" ||
          data.length < 1 || data.length > 16384 || !/^[A-Za-z0-9+/]*={0,2}$/.test(data)) fail(400, "Invalid protocol message");
      const previous = room.messages[role][name];
      if (previous && previous !== data) fail(409, "Protocol messages are immutable; do not reuse a pairing attempt");
      room.messages[role][name] = data;
      await this.ctx.storage.put("room", room);
      return json({ ok: true });
    }
    if (path === "/internal/object" && request.method === "POST") {
      const input = await body(request);
      const grant = this.grant(room, role, input.index, input.method);
      return json(grant);
    }
    if (path === "/url" && request.method === "POST") {
      const input = await body(request);
      const grant = this.grant(room, role, input.index, input.method);
      const method = String(input.method);
      if (this.env.DIRECT_R2 !== "true") {
        return json({ url: `/v1/rooms/${room.room}/objects/${input.index}`, headers: { authorization: `Bearer ${token}` } });
      }
      const client = new AwsClient({ accessKeyId: this.env.R2_ACCESS_KEY_ID!, secretAccessKey: this.env.R2_SECRET_ACCESS_KEY!,
        service: "s3", region: "auto" });
      const endpoint = `https://${this.env.R2_ACCOUNT_ID}.r2.cloudflarestorage.com/${this.env.R2_BUCKET_NAME}/${grant.key}`;
      const signedURL = new URL(endpoint);
      signedURL.searchParams.set("X-Amz-Expires", "120");
      const headers: Record<string, string> = {};
      if (method === "PUT") {
        headers["content-type"] = "application/octet-stream";
        headers["content-length"] = String(grant.length);
      }
      const signed = await client.sign(signedURL.toString(), { method, headers,
        aws: { signQuery: true, allHeaders: true } });
      return json({ url: signed.url, headers });
    }
    const ready = /^\/ready\/(\d+)$/.exec(path);
    if (ready && request.method === "POST") {
      const index = Number(ready[1]);
      const grant = this.grant(room, role, index, "PUT");
      const object = await this.env.FILES.head(grant.key);
      if (!object || object.size !== grant.length) fail(409, "Chunk missing or incorrect size");
      if (!room.ready.includes(index)) {
        room.ready.push(index);
        await this.ctx.storage.put("room", room);
      }
      return json({ ok: true });
    }
    if ((path === "/" && request.method === "DELETE") || (path === "/abort" && request.method === "POST")) {
      if (path === "/" && role !== "sender") fail(403, "Sender performs final cleanup after the receipt");
      room.closed = true;
      await this.ctx.storage.put("room", room);
      await this.cleanup(room);
      // Clean again after outstanding signed URLs expire. R2 lifecycle rules are an additional backstop.
      await this.ctx.storage.setAlarm(Date.now() + 5 * 60 * 1000);
      return json({ ok: true });
    }
    return fail(405, "Unsupported room operation");
  }

  private grant(room: Room, role: Role, index: unknown, method: unknown) {
    if (!Number.isSafeInteger(index) || (index as number) < 0 || (index as number) >= room.chunkCount) fail(400, "Invalid chunk index");
    if ((method !== "PUT" || role !== "sender") && (method !== "GET" || role !== "receiver")) fail(403, "Wrong role for object operation");
    if (!room.messages.sender.confirm || !room.messages.receiver.confirm ||
        !room.messages.sender.offer || !room.messages.receiver.accept) fail(409, "Pairing and acceptance must finish first");
    if (method === "GET" && !room.ready.includes(index as number)) fail(404, "Chunk not ready");
    return { key: objectKey(room, index as number), length: chunkLength(room, index as number) };
  }

  private async cleanup(room: Room) {
    let cursor: string | undefined;
    do {
      const batch = await this.env.FILES.list({ prefix: `transfers/${room.session}/`, cursor, limit: 1000 });
      if (batch.objects.length) await this.env.FILES.delete(batch.objects.map(object => object.key));
      cursor = batch.truncated ? batch.cursor : undefined;
    } while (cursor);
  }

  async alarm() {
    const room = await this.ctx.storage.get<Room>("room");
    if (!room) return;
    await this.cleanup(room);
    if (Date.now() >= room.expiresAt) await this.ctx.storage.deleteAll();
    else await this.ctx.storage.setAlarm(room.expiresAt);
  }
}
