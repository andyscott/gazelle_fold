"""Helpers for declarative filegroup outputs."""


def filegroup(name, srcs, present = True, visibility = None):
    """Return one declarative `filegroup` output for a fold callback."""

    attrs = {"srcs": srcs}
    if visibility != None:
        attrs["visibility"] = visibility
    return gazelle_fold.rule(
        kind = "filegroup",
        name = name,
        present = present,
        attrs = attrs,
    )
