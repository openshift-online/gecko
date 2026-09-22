# Testing guidance

This file defines repository-wide testing conventions for Gecko. Subsystem
guidance may add more specific expectations.

- Add or update unit tests for behavior changes. Keep tests focused and
  non-invasive: use existing interfaces, fakes, and test helpers rather than
  substantially refactoring production code only to make it testable.
- When fixing a bug, first reproduce the failure in a focused test when
  practical, then verify that the fix makes that test pass. Prefer reproducing
  the externally observable failure rather than asserting the implementation
  used to fix it.
- Start with the smallest test scope that exercises the change. Expand to the
  owning package or module after focused tests pass; use repository-wide tests
  according to the root `AGENTS.md` final-validation guidance.
- Cover customer-observable scenarios, not implementation details. Include the
  primary happy path and relevant non-happy paths such as invalid input,
  dependency failure, missing resources, conflicts, retries, or partial state.
- Tests must not depend on a live customer cluster, cloud account, credentials,
  or network access unless they are explicitly identified as integration tests.
- Assert meaningful outcomes: returned API objects, status and conditions,
  persisted state, emitted requests, and safe retry behavior. Avoid tests that
  only reproduce internal call order or mock choreography.
- Controller changes: add focused reconciliation tests for success, transient
  failure, terminal failure, and idempotent re-reconciliation where applicable.
- Use typed Kubernetes API objects in tests instead of raw YAML when practical.
- Do not skip or weaken a failing test merely to make the suite pass. Explain
  environmental failures and preserve the evidence.
