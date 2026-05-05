# Releasing

`gazelle_fold` is intended to be consumed through the Bazel Central Registry
(BCR). A release is ready when the source archive is stable, the public module
metadata matches the tag, and both the root repo and a downstream Bzlmod module
pass from a clean checkout.

## Before cutting a release

1. Update `MODULE.bazel` to the release version.
2. Keep the Gazelle version aligned between `MODULE.bazel` and `go.mod`.
3. Run the same checks as CI:

   ```bash
   bazelisk mod tidy
   git diff --exit-code -- MODULE.bazel MODULE.bazel.lock
   bazelisk test --lockfile_mode=error //...
   bazelisk run --lockfile_mode=error //:gazelle_ci

   cd examples/bzlmod
   bazelisk mod tidy
   git diff --exit-code -- MODULE.bazel
   bazelisk build @gazelle_fold//:gazelle_fold
   bazelisk test //...
   ```

4. Tag the commit as `vX.Y.Z` and create a GitHub release for that tag.

## BCR handoff

The first BCR submission should:

- publish module `gazelle_fold` at the same version as `MODULE.bazel`
- use the GitHub release archive as the stable source archive
- expose `@gazelle_fold//:gazelle_fold` in the anonymous-module presubmit
- use `examples/bzlmod` as the BCR test module; it intentionally does not
  check in a lockfile so multiple Bazel majors can resolve it independently
- test Linux on Bazel `8.x` and `9.x` initially

The BCR helper generates the registry-side files:

```bash
git clone https://github.com/bazelbuild/bazel-central-registry.git
cd bazel-central-registry
bazel run //tools:add_module
```

After the first module lands, future releases can be automated with
[`publish-to-bcr`](https://github.com/bazel-contrib/publish-to-bcr).
