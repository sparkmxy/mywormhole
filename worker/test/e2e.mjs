import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { createReadStream } from "node:fs";
import { mkdir, mkdtemp, open, readFile, stat, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as sleep } from "node:timers/promises";
import { harness, invitation } from "./harness.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
const executable = process.env.MYWORMHOLE_BIN || join(root, "dist", process.platform === "win32" ? "mywormhole.exe" : "mywormhole");
await stat(executable);
await mkdir(join(root, ".cache"), { recursive: true });
const folder = await mkdtemp(join(root, ".cache", "e2e-"));
const children = new Set();
const h = await harness();

function launch(args) {
  const child = spawn(executable, args, { cwd: folder, windowsHide: true,
    env: { ...process.env, MYWORMHOLE_SERVER: h.origin, MYWORMHOLE_INVITE_TOKEN: invitation } });
  children.add(child);
  const result = { child, log: "", exited: false };
  child.stderr.on("data", chunk => { result.log += chunk.toString(); });
  child.stdout.on("data", chunk => { result.log += chunk.toString(); });
  result.done = new Promise(resolve => {
    child.on("error", error => { result.log += error.message; result.exited = true; resolve({ code: -1, log: result.log }); });
    child.on("close", code => { children.delete(child); result.exited = true; resolve({ code, log: result.log }); });
  });
  return result;
}
async function until(predicate, label, timeout = 45000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const result = await predicate();
    if (result) return result;
    await sleep(25);
  }
  throw new Error(`Timed out waiting for ${label}`);
}
async function finish(process, expected = 0, timeout = 120000) {
  await until(() => process.exited, "CLI exit", timeout);
  const result = await process.done;
  assert.equal(result.code, expected, result.log);
  return result.log;
}
async function codeOf(process) {
  return until(() => {
    const code = /Receive code: (\d{6}(?:-[a-z]+){4})/.exec(process.log)?.[1];
    if (!code && process.exited) throw new Error(process.log);
    return code;
  }, "receive code");
}
async function makeFile(name, size) {
  const path = join(folder, name);
  const file = await open(path, "w");
  const block = randomBytes(256 * 1024);
  try { for (let offset = 0; offset < size; offset += block.length) await file.write(block.subarray(0, Math.min(block.length, size - offset))); }
  finally { await file.close(); }
  return path;
}
async function hash(path) {
  const result = createHash("sha256");
  for await (const chunk of createReadStream(path)) result.update(chunk);
  return result.digest("hex");
}
async function transfer(name, size) {
  const source = await makeFile(name, size);
  const destination = source + ".received";
  const send = launch(["send", "--state", source + ".send.json", source]);
  const code = await codeOf(send);
  const receive = launch(["receive", "--yes", "--state", source + ".receive.json", "--output", destination, code]);
  await finish(receive);
  const sendLog = await finish(send);
  assert.match(sendLog, /Cloud chunks deleted/);
  assert.equal(await hash(source), await hash(destination));
  assert.equal((await (await h.mf.getR2Bucket("FILES")).list()).objects.length, 0);
  console.log(`PASS roundtrip ${name}: ${size} bytes, SHA-256 matched and cloud objects removed`);
}

try {
  await transfer("文件-多块.bin", 32 * 1024 * 1024 + 137);
  await transfer("empty.bin", 0);

  const wrongSource = await makeFile("wrong-code.bin", 1024);
  const wrongSend = launch(["send", "--state", wrongSource + ".send.json", wrongSource]);
  const correct = await codeOf(wrongSend);
  const parts = correct.split("-");
  parts[1] = parts[1] === "amber" ? "apple" : "amber";
  const wrongReceive = launch(["receive", "--yes", "--output", wrongSource + ".received", parts.join("-")]);
  await until(() => wrongReceive.exited && wrongSend.exited, "wrong-code rejection");
  assert.notEqual((await wrongReceive.done).code, 0);
  assert.notEqual((await wrongSend.done).code, 0);
  assert.match(wrongReceive.log + wrongSend.log, /confirmation failed|Room closed/);
  assert.equal((await (await h.mf.getR2Bucket("FILES")).list()).objects.length, 0);
  console.log("PASS wrong valid-word code rejected before uploading any file data");

  const protectedSource = await makeFile("protected.bin", 1024);
  const protectedOutput = protectedSource + ".received";
  await writeFile(protectedOutput, "keep existing contents");
  const protectedSend = launch(["send", "--state", protectedSource + ".send.json", protectedSource]);
  const protectedCode = await codeOf(protectedSend);
  const protectedReceive = launch(["receive", "--yes", "--output", protectedOutput, protectedCode]);
  await until(() => protectedReceive.exited && protectedSend.exited, "existing-output rejection");
  assert.notEqual((await protectedReceive.done).code, 0);
  assert.equal(await readFile(protectedOutput, "utf8"), "keep existing contents");
  console.log("PASS existing output is preserved");

  const resumeSource = await makeFile("resume.bin", 64 * 1024 * 1024 + 13);
  const resumeOutput = resumeSource + ".received";
  const resumeState = resumeSource + ".receive.json";
  const resumeSend = launch(["send", "--chunk-mib", "1", "--parallel", "2", "--state", resumeSource + ".send.json", resumeSource]);
  const resumeCode = await codeOf(resumeSend);
  const firstReceive = launch(["receive", "--yes", "--parallel", "1", "--state", resumeState, "--output", resumeOutput, resumeCode]);
  await until(() => /Received /.test(firstReceive.log), "first persisted chunk");
  firstReceive.child.kill();
  await until(() => firstReceive.exited, "interrupted receiver");
  const state = JSON.parse(await readFile(resumeState, "utf8"));
  assert.ok(Object.keys(state.done).length > 0 && Object.keys(state.done).length < state.file.chunkCount);
  // Verify resume re-checks its local bitmap, instead of trusting altered partial bytes.
  const partial = await open(state.partial, "r+");
  await partial.write(Buffer.from([0xff, 0x00, 0xff]), 0, 3, 0);
  await partial.close();
  const secondReceive = launch(["resume", "--parallel", "2", resumeState]);
  await finish(secondReceive);
  await finish(resumeSend);
  assert.equal(await hash(resumeSource), await hash(resumeOutput));
  console.log("PASS receiver process restart, partial-file corruption detection and resume");

  const senderResumeSource = await makeFile("sender-resume.bin", 48 * 1024 * 1024 + 31);
  const senderResumeOutput = senderResumeSource + ".received";
  const senderResumeState = senderResumeSource + ".send.json";
  const firstSend = launch(["send", "--chunk-mib", "1", "--parallel", "1", "--state", senderResumeState, senderResumeSource]);
  const senderResumeCode = await codeOf(firstSend);
  const continuousReceive = launch(["receive", "--yes", "--parallel", "2", "--state", senderResumeSource + ".receive.json", "--output", senderResumeOutput, senderResumeCode]);
  await until(() => /Uploaded /.test(firstSend.log), "first sender upload");
  firstSend.child.kill();
  await until(() => firstSend.exited, "interrupted sender");
  const secondSend = launch(["resume", "--parallel", "2", senderResumeState]);
  await finish(continuousReceive);
  await finish(secondSend);
  assert.equal(await hash(senderResumeSource), await hash(senderResumeOutput));
  console.log("PASS sender process restart and resume using its original PAKE session");

  if (process.env.MYWORMHOLE_LARGE_TEST === "1") {
    const started = Date.now();
    await transfer("one-gib.bin", 1024 ** 3);
    console.log(`Local 1 GiB end-to-end time: ${((Date.now() - started) / 1000).toFixed(1)} s (not a cross-border benchmark)`);
  }
  console.log(`All end-to-end tests passed. Inspectable test files: ${folder}`);
} finally {
  for (const child of children) child.kill();
  await h.close();
}
