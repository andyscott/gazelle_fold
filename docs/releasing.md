# Releasing

`gazelle_fold` is intended to be consumed through the Bazel Central Registry
(BCR). Releases use the same three-part lane as larger rulesets:

1. `release_ruleset` creates a stable GitHub release artifact.
2. `publish-to-bcr` turns that release into a BCR pull request.
3. `.bcr/` templates keep the registry metadata versioned beside the source.

A release is ready when the public module metadata matches the tag, and both the
root repo and a downstream Bzlmod module pass from a clean checkout.

## One-time setup

Before the first automated publish:

1. Fork `bazelbuild/bazel-central-registry` to
   `andyscott/bazel-central-registry`.
2. Create a classic GitHub PAT with `repo` and `workflow` scopes.
3. Save it in this repository as the `BCR_PUBLISH_TOKEN` Actions secret.

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

4. Push the release tag:

   ```bash
   ./release.sh vX.Y.Z
   ```

   `.github/workflows/release.yaml` will create the GitHub release artifact and
   then call `.github/workflows/publish.yaml` to open the BCR pull request.

## BCR handoff

The first BCR submission should:

- publish module `gazelle_fold` at the same version as `MODULE.bazel`
- use the generated GitHub release artifact as the stable source archive
- expose `@gazelle_fold//:gazelle_fold` in the anonymous-module presubmit
- use `examples/bzlmod` as the BCR test module; it intentionally does not
  check in a lockfile so multiple Bazel majors can resolve it independently
- test Linux on Bazel `8.x` and `9.x` initially

If automation ever needs to be retried for an existing tag, run the
`Publish to BCR` workflow manually and provide the tag name.
