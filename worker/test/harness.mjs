import { build } from "esbuild";
import { Miniflare, convertV4MiniflareOptions } from "miniflare";
import { fileURLToPath } from "node:url";

export const invitation = "test-invitation-token-only-never-deploy";
export const sender = "s".repeat(43);
export const receiver = "r".repeat(43);

export async function harness(extra = {}) {
  const bundle = await build({ entryPoints: [fileURLToPath(new URL("../src/index.ts", import.meta.url))],
    bundle: true, write: false, format: "esm", platform: "neutral", target: "es2022" });
  const mf = new Miniflare(convertV4MiniflareOptions({ host: "127.0.0.1", port: 0,
    name: "mywormhole-test", modules: true, script: bundle.outputFiles[0].text,
    compatibilityDate: "2026-10-20",
    durableObjects: { ROOMS: { className: "TransferRoom", useSQLite: true } },
    r2Buckets: ["FILES"],
    bindings: { INVITE_TOKEN: invitation, DIRECT_R2: "false", R2_BUCKET_NAME: "mywormhole-transfers",
      ROOM_TTL_SECONDS: "7200", MAX_FILE_BYTES: "2147483648", ...extra },
  }));
  const origin = (await mf.ready).origin;
  const call = async (path, { method = "GET", token = sender, data, ...rest } = {}) => {
    const headers = { authorization: `Bearer ${token}`, ...rest.headers };
    if (data !== undefined) headers["content-type"] = "application/json";
    return fetch(origin + path, { ...rest, method, headers, body: data === undefined ? rest.body : JSON.stringify(data) });
  };
  const create = async (plan = {}) => {
    const response = await call("/v1/rooms", { method: "POST", token: invitation,
      data: { token: sender, fileSize: 65536, chunkSize: 65536, chunkCount: 1, ...plan } });
    if (response.status !== 201) throw new Error(await response.text());
    return response.json();
  };
  const paired = async (room) => {
    const root = `/v1/rooms/${room.room}`;
    let response = await call(root + "/join", { method: "POST", token: "", data: { token: receiver } });
    if (!response.ok) throw new Error(await response.text());
    for (const [token, names] of [[sender, ["spake", "confirm", "offer"]], [receiver, ["spake", "confirm", "accept"]]]) {
      for (const name of names) {
        response = await call(root + "/message", { method: "POST", token, data: { name, data: "dGVzdA==" } });
        if (!response.ok) throw new Error(await response.text());
      }
    }
  };
  return { mf, origin, call, create, paired, close: () => mf.dispose() };
}
