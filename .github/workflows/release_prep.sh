#!/usr/bin/env bash

set -o errexit -o nounset -o pipefail

tag=$1
version=${tag#v}
prefix="gazelle_fold-${version}"
archive="gazelle_fold-${tag}.tar.gz"

# Build a stable release artifact with the same top-level directory shape as a
# normal GitHub source archive, but under our control for BCR publishing.
git archive --format=tar --prefix="${prefix}/" "${tag}" | gzip > "${archive}"

cat <<EOF
## Add to your \`MODULE.bazel\` file

\`\`\`starlark
bazel_dep(name = "gazelle_fold", version = "${version}")
\`\`\`
EOF
