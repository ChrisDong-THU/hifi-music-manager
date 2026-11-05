import assert from "node:assert/strict";
import test from "node:test";
import { dependenciesMatch } from "../scripts/dependency-state.mjs";

const base = { version: "1", resolved: "registry/pkg", integrity: "hash" };
test("dependency validation rejects stale pins, missing packages and leftover packages", () => {
  const expected = { packages: { "": {}, "node_modules/pkg": base } };
  assert.equal(
    dependenciesMatch(
      expected,
      { packages: { "node_modules/pkg": { ...base } } },
      "win32",
      "x64",
    ),
    true,
  );
  assert.equal(
    dependenciesMatch(expected, { packages: {} }, "win32", "x64"),
    false,
  );
  assert.equal(
    dependenciesMatch(
      expected,
      { packages: { "node_modules/pkg": { ...base, integrity: "stale" } } },
      "win32",
      "x64",
    ),
    false,
  );
  assert.equal(
    dependenciesMatch(
      expected,
      { packages: { "node_modules/pkg": base, "node_modules/old": base } },
      "win32",
      "x64",
    ),
    false,
  );
});
test("optional native dependencies are checked for the build computer's OS, CPU and libc", () => {
  const expected = {
    packages: {
      "node_modules/native": {
        ...base,
        optional: true,
        os: ["linux"],
        cpu: ["x64"],
        libc: ["musl"],
      },
    },
  };
  assert.equal(
    dependenciesMatch(expected, { packages: {} }, "win32", "x64"),
    true,
  );
  assert.equal(
    dependenciesMatch(expected, { packages: {} }, "linux", "arm64", "musl"),
    true,
  );
  assert.equal(
    dependenciesMatch(expected, { packages: {} }, "linux", "x64", "glibc"),
    true,
  );
  assert.equal(
    dependenciesMatch(expected, { packages: {} }, "linux", "x64", "musl"),
    false,
  );
});
