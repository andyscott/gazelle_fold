# Dependency policy example

This fixture shows a custom policy that forbids handwritten labels for
dependencies that should come from a project helper instead.

The example policy uses `rule.deps.label_literals_matching(...)`, not
`rule.deps.labels_matching(...)`, because it cares about source evidence in the
BUILD file. `all_crate_deps(...)` may evaluate to `@cargo` dependencies, but
that is allowed here; the policy only rejects literal `@cargo//...` labels that
someone wrote into `deps` by hand.

The fixture intentionally fails: `mixed_deps` and `select_deps` contain
handwritten generated labels, while `opaque_deps` shows how a policy can fail
closed when the source expression has no matching literals but also cannot be
fully inspected.

Use `rule.deps.labels_matching(...)` for policies that need to validate a
literal dependency list itself, such as the stock `forbidden_deps` policy.
