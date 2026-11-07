# Running the suite on a Windows host

Upstream CI runs on Linux only, so tests written there carry POSIX
assumptions. On a Windows host a red test is one of two things: a production
defect on Windows, or a test bound to POSIX. Tell them apart before touching
either.

## Which side to change

- Production behaves correctly on its platform and the test assumes another:
  fix the **test** — compute the expectation per OS, or skip.
- Production is wrong on Windows (a path, a shell, a file left open): fix
  the **code**, with a test that fails first.
- Skip only a verified POSIX dependence with no Windows equivalent, and say
  so: `t.Skip("[platform] <reason>")`. The prefix and a static message are
  required by `TestRepositorySkipsCarryReasons`; the
  [capability census](../../test-capability-ledger.md) classifies them.
- A test that does not compile on Windows (`syscall.Kill`, `Mkfifo`,
  `Rlimit`) moves to a `_unix_test.go` file, or a `_windows_test.go` twin
  with the same signature; never a skip.

## Traps already paid for

- **Backslashes in generated text.** A Windows path put into a hand-built
  JSON or DSL string is an invalid escape. Build JSON with `json.Marshal`.
- **`Perm()` and `Chmod` carry no information.** Windows reports 0666/0777
  and `Chmod` only toggles read-only. Assert mode bits where the OS has them;
  the protection on Windows is an ACL, witnessed in
  `pkg/authoringlock/authoringlock_windows_test.go`.
- **Fake binaries need a PATHEXT extension** (`.exe`) or `exec.LookPath`
  never finds them; a shell script named `dash` is not executable.
- **`pwd` goes through Git Bash** when a script looks POSIX
  (`pkg/internal/hostshell`) and prints `/c/...`. Prove a working directory
  with a marker file the command writes, then `os.Stat` it.
- **`HOME` is not the home.** `os.UserHomeDir` reads `USERPROFILE`; set both.
  Known limit: `hometest.IsolateUserHome` leaves `USERPROFILE` alone, so a
  test that only sets `HOME` may reach the real profile.
- **`filepath.Dir("/w/bots")` is `\w`.** Run expectations through
  `filepath.FromSlash`.
- **Fixed time bounds** judge process start-up. Measure a baseline in the
  same test and compare against `baseline + bound`.
