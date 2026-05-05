# Bzlmod consumer example

This nested module exercises `gazelle_fold` the way a downstream repository
will: through `bazel_dep(...)`, with `local_path_override(...)` standing in for
the future Bazel Central Registry release while this source tree is under test.

It is intentionally small. The root repo already has broad behavior coverage;
this module guards the public consumption path:

- `@gazelle_fold//:gazelle_fold` still builds from another module
- a consumer-owned `gazelle_binary(...)` can load `@gazelle_fold//language/fold`
- a stock fold still runs through Gazelle generation tests

This example intentionally does not check in a lockfile. It is meant to behave
like a BCR test module across multiple Bazel majors, and Bazel 8 and Bazel 9 use
different lockfile formats.
