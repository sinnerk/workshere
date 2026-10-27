# Checks

Every check answers one question: **what is different between this machine and the machine that runs CI?**

A check may only report something it can prove from the repository. When it cannot resolve a path or parse a manifest, it stays quiet instead of guessing — a false accusation costs more trust than a missed finding.

## Severity

| Level | Meaning |
| --- | --- |
| **high** | This fails in CI, deterministically, as written. |
| **medium** | This changes behaviour between environments and is a likely cause of a CI-only failure. |
| **low** | This is a latent risk or a convention that will bite later. |

`--min-severity` filters both output and the exit code.

---

## `case-mismatch` — high

**Catches:** a relative import whose letter case does not match the file on disk.

```text
src/api/handlers.ts:1
imports "../Utils", but the file on disk is "utils.ts"
```

Windows (`NTFS`) and macOS (`APFS`, case-insensitive by default) open `../Utils` when the file is `utils.ts`. Linux does not. The code therefore passes every local run and fails on `ubuntu-latest` with `Cannot find module`.

**Resolution:** every path component is compared case-sensitively against `ReadDir` output, so a wrong-case *directory* is caught too.

**Not covered:** aliased imports (`@/utils`), because resolving them requires reading bundler config and guessing wrong produces noise.

## `case-collision` — high

**Catches:** two tracked paths that differ only by letter case, such as `src/Utils.ts` and `src/utils.ts`.

Git stores both. Neither Windows nor macOS can hold both in one directory, so one silently disappears on checkout. This is the same class of failure as `case-mismatch`, one level up.

## `untracked-import` — high

**Catches:** an import that resolves to a file that exists locally but is not in the index, or is explicitly ignored.

A fresh clone does not have that file, so CI does not either. Generators and type-only build output produce this constantly.

## `env-tracked` — high

**Catches:** a real `.env`, `.env.local`, `.env.production`, `.env.test`, `.env.staging` or `.env.development` in the index.

Two problems in one: the values are in the history forever, and CI loads them as configuration, which makes local and CI behaviour diverge in ways nobody can see.

`.env.example`, `.env.sample`, `.env.template` and `.env.dist` are not reported.

## `crlf-script` — high

**Catches:** a tracked file that starts with `#!` and whose first line ends in CRLF.

```text
/usr/bin/env: 'bash\r': No such file or directory
```

The shebang is parsed by the kernel, which treats the `\r` as part of the interpreter path. Nothing about the error points at line endings.

## `lockfile-drift` — high or medium

**Catches:** `package.json` has uncommitted changes while its lockfile does not.

If CI runs `npm ci`, the install refuses to proceed and the job dies before any test runs — severity **high**. Otherwise the next install resolves a different dependency tree from yours — severity **medium**.

Also reports a lockfile that is in `.gitignore`, which makes reproducible builds impossible.

## `cache-without-lockfile` — high

**Catches:** `actions/setup-node` (or `setup-python`) with `cache: npm` when no lockfile is tracked.

`setup-node` fails the step when it cannot compute a cache key, so the job dies before the first test.

## `missing-script` — high

**Catches:** a workflow that invokes a npm or pnpm script that does not exist in `package.json`, or a `make` target that does not exist in the `Makefile`.

If `package.json` cannot be parsed, the check reports a warning instead of a finding, so a broken manifest does not silently disable it.

## `runtime-mismatch` — medium

**Catches:** a pinned runtime version in CI that differs from the one this machine runs.

| Language | Compared |
| --- | --- |
| Node | major version (`18` vs `24`) |
| Go | major and minor (`1.24` vs `1.27`) |
| Python | major and minor (`3.11` vs `3.12`) |

Versions are read from `node-version`, `go-version`, `python-version`, the matching `*-version-file` (`go.mod`, `.nvmrc`, `.python-version`, `.tool-versions`), and matrix expansion. `stable`, `latest` and ranges are skipped rather than guessed.

Patch versions are ignored on purpose: `22.3.0` against `22.9.0` is not a CI risk.

## `dead-path-filter` — medium

**Catches:** `on.push.paths` or `on.pull_request.paths` entries that cannot match.

Two forms:

- a literal path that does not exist;
- a literal path that is a **directory** — a paths filter is compared against changed file names, so `paths: [services/api]` matches nothing and the workflow never runs.

Globs are skipped, since they are patterns rather than paths.

## `large-file` — high or medium

**Catches:** tracked files at or above 50 MB (medium, GitHub warns) and 100 MB (high, GitHub rejects the push).

## `absolute-path` — medium

**Catches:** `/home/<user>/`, `/Users/<user>/` or `C:\Users\<user>\` inside tracked source or configuration.

The path only exists on the machine that wrote it. CI runs as a different user with a different home directory.

**Excluded:** test files, fixtures and `testdata`. A test that asserts on `C:\Users\tester` is testing a redactor, not leaking a path.

## `symlink` — low

**Catches:** tracked symlinks (index mode `120000`).

Windows checks them out as plain text files unless `core.symlinks` is enabled, which changes what a build sees without changing what git reports.

## `env-undeclared` — low

**Catches:** environment variables read by code (`process.env.X`, `os.Getenv`, `os.environ`) that no `.env.example`-style file and no workflow `env:` block declares.

Deliberately low: a deployment platform may inject the variable. The check only runs when the repository already has a declaration convention, because comparing against nothing would flag every variable in the project.

Ambient variables (`CI`, `PATH`, `HOME`, `NODE_ENV`, the `GITHUB_*` and `RUNNER_*` families) are never reported.

---

## What the checks deliberately do not do

- **Run anything.** No build, no test, no container. `workshere` reads files and prints what it found.
- **Modify anything.** There is no `--fix`. The findings have to be trustworthy before anything is allowed to change a repository.
- **Guess.** An unresolvable import, an unparseable manifest or an unfamiliar workflow shape becomes a note, never a finding.
- **Talk to the network.** It never asks GitHub what your CI environment is; it reads your workflow files.
