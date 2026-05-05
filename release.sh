#!/usr/bin/env bash

set -o errexit -o nounset -o pipefail

tag=${1:?usage: ./release.sh vX.Y.Z}

git tag -a "${tag}" -m "${tag}"
git push origin "${tag}"
