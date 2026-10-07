/**
 * The app's version is semantic: MAJOR.MINOR.PATCH.
 *
 *   patch  fixes only
 *   minor  something new, and nothing that stops an older Mac working with it
 *   major  a change people must act on, such as needing a newer agentman
 *
 * node scripts/semver.mjs <patch|minor|major> <version> prints the next one.
 */
export function bump(version, part) {
  const match = /^(\d+)\.(\d+)\.(\d+)$/.exec(String(version ?? "").trim());
  if (!match) throw new Error(`not a MAJOR.MINOR.PATCH version: ${version}`);
  const [major, minor, patch] = match.slice(1).map(Number);
  switch (part) {
    case "major":
      return `${major + 1}.0.0`;
    case "minor":
      return `${major}.${minor + 1}.0`;
    case "patch":
      return `${major}.${minor}.${patch + 1}`;
  }
  throw new Error(`the bump must be patch, minor or major, not ${part}`);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  try {
    process.stdout.write(bump(process.argv[3], process.argv[2]));
  } catch (error) {
    console.error(error.message);
    process.exit(2);
  }
}
