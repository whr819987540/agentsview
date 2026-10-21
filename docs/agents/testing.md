# Testing Rules

Read this file before adding or changing tests.

## Coverage

- Add unit tests for every new feature and bug fix.
- Run the smallest relevant test set before committing. State which checks you
  could not run.
- Keep tests fast and isolated.

## Bug Fixes

1. Reproduce the bug before changing code, in a test or a scripted browser run.
   When the cause is unclear, record the suspect side effect instead of
   guessing, such as the call stack of every `scrollTo` on the transcript.
1. Confirm the regression test fails on the code before the fix, at the
   assertion that covers the bug.
1. When one change fixes several defects, confirm each defect's assertion fails
   on a build that has only the other fixes. An earlier failing assertion
   stops the test and can hide a later one. Defects can also mask each other
   in manual use, for example when one retries work that another abandoned.

## Test Data and Doubles

- Shape fixtures like real data. Include the sizes and shapes that stress the
  code path: long sessions, code blocks, tool output, and transcript rows both
  taller and shorter than the virtualizer's 120 px row estimate. One-line
  messages hide measurement bugs, because TanStack's first-measurement scroll
  compensation can carry the viewport past its target only when rows are
  taller than the estimate.
- Let a fake at a boundary reproduce the dependency's adverse behavior that
  production code must survive, such as a virtual row unmounting right after
  it mounted or a scroll offset shifting after rows are measured. A fake that
  only cooperates cannot catch those failures.

## Go Tests

- Prefer table-driven tests.
- Use `github.com/stretchr/testify` for assertions.
- Use `require.X` when failure must stop the test, such as setup errors, nil
  values, or length checks before indexing.
- Use `assert.X` for independent checks that can continue after failure.
- Do not add `if got != want { t.Fatalf(...) }` comparisons.
- Test helpers must use testify for their own assertions.
- Use the existing `testDB(t)` helper for database tests.
- Use `t.TempDir()` for temporary directories.

### Timing budgets

The kit `deadlinetest` analyzer, run by `kennlint` in `make lint` and
`make lint-ci`, rejects sub-second constant budgets in tests outside a
`testing/synctest` bubble, such as `context.WithTimeout`, `time.After`, timers,
and testify `Eventually`. Wait on a real event, run the test under
`synctest.Test`, or use a long hang guard on a `select` that already waits for
the event. Mutex acquisition does not durably block in a synctest bubble.

Mark a site `//nolint:kennlint // reason` only when the asserted result is the
deadline or cancellation expiring, or when a `select` on a timer shows that an
event does not happen. The reason names which case applies and what holds the
wait.

The check sees only the `fts5` test files each lint run compiles: Linux and
Windows in full, and on macOS only the packages the macOS job lints. Tests
behind `pgtest`, `chtest`, `duckdbtest`, or `s3test` go unchecked. It skips
short positive `Never` windows, which cannot fail.

## Frontend and End-to-End Tests

- Keep frontend unit tests beside the code in `*.test.ts` files.
- Put Playwright tests in `frontend/e2e/`.

### Scrolling and live updates

- Test each external event that changes the transcript against each mode that
  can move the viewport. Events include live session updates, older history
  loading, filter or sort changes, and session switches. Modes include
  in-session find, follow latest, and position restore. Cover at least the
  pairs that compete for the scroll position.
- Test both sides of a scroll contract: what must scroll, and what must leave
  the reader in place.
- Treat every reactive read in an effect that scrolls as a trigger. Add a test
  for each trigger that must not scroll.
- Drive live session updates by routing the session watch stream, as
  `frontend/e2e/session-find-live-updates.spec.ts` does.
- Before asserting that something did not move, wait for a positive signal that
  the triggering event was handled, such as an updated match counter. Then
  wait a few animation frames and compare positions. A fixed wait alone cannot
  tell "did not move" from "not handled yet".

## Shell Tests

Run scripts against controlled input and assert their output, exit code, or side
effects. Do not read a script and assert that it contains an implementation
line, flag, or snippet.
