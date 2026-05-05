# Bazel Central Registry

This directory contains the templates used by
[`publish-to-bcr`](https://github.com/bazel-contrib/publish-to-bcr) to turn a
GitHub release into a Bazel Central Registry pull request.

- `metadata.template.json` describes the module and its maintainers.
- `source.template.json` points BCR at the signed release artifact.
- `presubmit.yml` tells BCR what public behavior to verify.
- `config.yml` is reserved for optional publisher settings.
