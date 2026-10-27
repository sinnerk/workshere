# workshere

**Your code works here. Find out what will fail there — before you push.**

CI fails for reasons a local run can never show you: a file that exists only on your disk, an import whose letter case Windows forgives, a lockfile you forgot to commit, a runtime version you have never actually run.

`workshere` reads the repository and its workflow files, compares them with what this machine really has, and prints each divergence with the exact fix. Static analysis, offline, read-only, well under a second on a normal repository.

```console
$ workshere
workshere 0.1.0  %USERPROFILE%\workshere-demo

8 things will behave differently in CI

  high    .env
          .env is committed to git
          Whatever is in this file is in the repository history, and CI will load it as if it were
          configuration.
          fix: git rm --cached .env, add it to .gitignore, and commit an .env.example instead

  high    .github/workflows/ci.yml
          CI runs `npm run lint`, but package.json has no "lint" script
          The job fails on its first command, which means none of the tests it was supposed to protect
          ever run.
          evidence: npm run lint
          fix: add the "lint" script to package.json, or correct the workflow

  high    package.json
          package.json changed, package-lock.json did not
          CI runs `npm ci`, which refuses to install when package.json and the lockfile disagree. The
          build stops before any test runs.
          evidence: git status: M package.json
          fix: run the package manager's install command and commit the updated lockfile

  high    scripts/deploy.sh:1
          shebang line ends with CRLF
          On Linux the interpreter name becomes "bash\r" and the script fails with a confusing "bad
          interpreter" error.
          evidence: #!/bin/sh
          fix: convert the file to LF, and add `* text=auto eol=lf` to .gitattributes

  high    src/api/handlers.ts:1
          imports "../Utils", but the file on disk is "utils.ts"
          Windows and macOS ignore letter case when opening a file. The Linux runner does not, so this
          import resolves here and fails there.
          evidence: ../Utils  ->  src/utils.ts
          fix: change the import to "../utils"

  ...3 more

$ echo $?
1
```

The output above is real, from `testdata/make-demo.ps1`, run with `--redact` so the machine's home directory is replaced. Reproduce it yourself with that script.

Exit code `0` means clean, `1` means something is wrong, `2` means the tool could not run.

## Install

```console
go install github.com/sinnerk/workshere/cmd/workshere@latest
```

Requires Go 1.24 or newer. No runtime, no service, no configuration file, no account. One dependency (`yaml.v3`) to read workflow files correctly.

## What it checks

| Check | Severity | What it catches |
| --- | --- | --- |
| `case-mismatch` | high | `import "./Utils"` where the file is `utils.ts` — resolves on Windows and macOS, fails on Linux |
| `case-collision` | high | two tracked paths that differ only by letter case — one is lost on checkout |
| `untracked-import` | high | an import that resolves to a file git does not track, or explicitly ignores |
| `env-tracked` | high | a real `.env` (not `.env.example`) committed to the repository |
| `crlf-script` | high | a shebang script with CRLF line endings — `bad interpreter: /bin/bash^M` |
| `lockfile-drift` | high/medium | `package.json` changed while the lockfile did not, so `npm ci` refuses to install |
| `cache-without-lockfile` | high | `actions/setup-node` with `cache: npm` and no lockfile to key the cache on |
| `missing-script` | high | a workflow that runs `npm run lint` or `make test` when no such script or target exists |
| `runtime-mismatch` | medium | CI pins Node 18, this machine runs Node 24 |
| `dead-path-filter` | medium | `paths:` that can never match: a missing file, or a directory where a file pattern is required |
| `large-file` | high/medium | tracked files that GitHub will warn about (50 MB) or reject (100 MB) |
| `absolute-path` | medium | `/home/alice/...` or `C:\Users\alice\...` baked into tracked source |
| `symlink` | low | tracked symlinks, which Windows checks out as text files |
| `env-undeclared` | low | environment variables the code reads that nothing in the repository declares |

Test files, fixtures and vendored directories are excluded from the path and environment checks on purpose. A fixture that contains `C:\Users\tester` is doing its job, not breaking the build.

## Why not just use `act`?

`act` runs your workflow locally inside Docker. It answers *"can I execute CI here?"* — at the cost of a container, a pull, and a setup that usually does not match the real runner anyway.

`workshere` answers a different and cheaper question: *"what is different between this machine and that runner?"* It does not execute anything, so it cannot be defeated by a missing Docker daemon, and it finishes before `act` has pulled its first image. They complement each other; use `act` when you want to run the job, `workshere` when you want to know why it will fail.

## Use it as a gate

```yaml
- name: preflight
  run: |
    go install github.com/sinnerk/workshere/cmd/workshere@latest
    workshere --min-severity high
```

## Flags

| Flag | Meaning |
| --- | --- |
| `--json` | machine-readable report, every finding with its check name and severity |
| `--min-severity low\|medium\|high` | severity floor for both output and exit code |
| `--root <dir>` | run from anywhere inside the repository |
| `--redact` | replace the home directory with `%USERPROFILE%` in output, safe to paste into an issue |
| `--list-checks` | print the check names and what they mean |
| `--no-color` | disable ANSI colour |

## Honest limitations

- **GitHub Actions only.** GitLab CI, CircleCI and Buildkite are not parsed yet.
- **Relative imports only.** Aliased imports (`@/utils`, `components/Button`) are skipped rather than guessed, because guessing produces false findings.
- **It cannot know your deployment environment.** That is why `env-undeclared` is low severity: a variable may be injected by your host.
- **Windows-only constructs are not flagged.** A `.bat` file is not reported as "will fail on Linux", since it may be intentional.
- **No auto-fix.** Every finding names the fix; none of them are applied. A diagnostic that edits your repository is harder to trust.

## False positives

A preflight check that cries wolf gets deleted. If `workshere` flags something that is actually fine, that is a bug worth more than a feature request. Open an issue with the `--redact` output and the workflow snippet involved.

## License

MIT. See [LICENSE](LICENSE).
