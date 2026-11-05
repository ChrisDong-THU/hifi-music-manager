import { readFile, readdir, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = fileURLToPath(new URL("../", import.meta.url));
const lock = JSON.parse(
  await readFile(path.join(root, "package-lock.json"), "utf8"),
);
const notices = [
  "Frontend dependency notices\n\nGenerated from the locked production dependency tree.\n",
];
for (const [location, pkg] of Object.entries(lock.packages)) {
  if (!location || pkg.dev) continue;
  const directory = path.join(root, location);
  const manifest = JSON.parse(
    await readFile(path.join(directory, "package.json"), "utf8"),
  );
  const names = (await readdir(directory)).filter((name) =>
    /^(licen[cs]e|copying|notice)([._-]|$)/i.test(name),
  );
  notices.push(
    `\n${"=".repeat(72)}\n${manifest.name} — ${manifest.license || "see license below"}\n`,
  );
  if (!names.length)
    throw new Error(`Missing license text for ${manifest.name}`);
  for (const name of names)
    notices.push(await readFile(path.join(directory, name), "utf8"));
}
await writeFile(
  path.join(root, "../licenses/frontend.txt"),
  notices.join("\n"),
);

// Distribute the exact adapted MPL sources embedded in this build. Keeping the
// source offer generated here prevents later Go edits from shipping a stale copy.
const manager = path.resolve(root, "..");
const analysisSources = ["bitdepth.go", "spectrum.go", "analysis.go"];
const analysisNotice = [
  "FlacCompagnon-derived Go analysis source; MPL-2.0. Upstream commit 7a382761a2054e07d3572736101e64560ef09057.\n",
];
for (const file of analysisSources) {
  const relative = `internal/audioquality/${file}`;
  analysisNotice.push(
    `\n===== ${relative} =====\n`,
    (await readFile(path.join(manager, relative), "utf8")).trimEnd() + "\n",
  );
}
await writeFile(
  path.join(manager, "licenses/flaccompagnon-source.txt"),
  analysisNotice.join(""),
);
