# Filegroup example

This fixture shows the stock `filegroup_rollup` fold on a tiny markdown tree.
The root package contributes its own docs, the child package contributes another
file, and a full Gazelle walk builds the recursive rollup from direct child
exports rather than from one repo-wide declaration.
