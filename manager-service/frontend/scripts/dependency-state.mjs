const supports = (values, current) =>
  !values ||
  (!values.includes(`!${current}`) &&
    (!values.some((value) => !value.startsWith("!")) ||
      values.includes(current) ||
      values.includes("any")));

export function dependenciesMatch(expected, installed, platform, arch, libc) {
  return Boolean(
    installed?.packages &&
      Object.keys(installed.packages).every((location) =>
        Object.hasOwn(expected.packages, location),
      ) &&
      Object.entries(expected.packages).every(([location, pkg]) => {
        if (!location) return true;
        if (
          pkg.optional &&
          (!supports(pkg.os, platform) ||
            !supports(pkg.cpu, arch) ||
            !supports(pkg.libc, libc))
        )
          return true;
        const current = installed.packages[location];
        return (
          current &&
          ["version", "resolved", "integrity"].every(
            (key) => current[key] === pkg[key],
          )
        );
      }),
  );
}
