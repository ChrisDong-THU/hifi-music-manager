import { readFile, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { dependenciesMatch } from "./dependency-state.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const raw = await readFile(path.join(root, "package-lock.json"));
const hash = createHash("sha256").update(raw).digest("hex");
const stamp = path.join(root, "node_modules/.hifi-lock-sha256");
const saved = await readFile(stamp, "utf8").catch(() => "");
let installed;
try {
  installed = JSON.parse(
    await readFile(path.join(root, "node_modules/.package-lock.json"), "utf8"),
  );
} catch {
  /* A missing or interrupted install needs npm ci. */
}
const expected = JSON.parse(raw);
const libc =
  process.platform === "linux"
    ? process.report.getReport().header.glibcVersionRuntime
      ? "glibc"
      : "musl"
    : undefined;
const matches = dependenciesMatch(
  expected,
  installed,
  process.platform,
  process.arch,
  libc,
);
if (!matches || (saved && saved !== hash)) {
  if (!process.env.npm_execpath)
    throw new Error("Run through npm run deps:check");
  const args = [
    process.env.npm_execpath,
    "ci",
    "--prefix",
    root,
    "--no-audit",
    "--no-fund",
  ];
  if (process.argv[2]) args.push("--cache", process.argv[2]);
  execFileSync(process.execPath, args, { stdio: "inherit", windowsHide: true });
}
if (saved !== hash) await writeFile(stamp, hash);
