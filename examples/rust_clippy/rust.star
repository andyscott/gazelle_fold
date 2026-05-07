"""Example fold that maintains one package-local Clippy target per Rust package."""

_RUST_RULE_KINDS = [
    "rust_binary",
    "rust_library",
    "rust_test",
]


def _local_rule_labels(ctx, kinds):
    return [":" + rule.name for rule in ctx.rules_matching(kinds = kinds)]


def _apply(ctx):
    """Derive one managed `rust_clippy` target from local handwritten Rust rules.

    Concrete Rust rule kinds are the source of truth here. That keeps the local
    aggregator tied to the rules it summarizes instead of accidentally sweeping
    generated wrappers or parent rollups into the target.
    """

    deps = _local_rule_labels(ctx, ctx.params["kinds"])
    return [
        gazelle_fold.rule(
            kind = "rust_clippy",
            name = ctx.params["target_name"],
            present = deps != [],
            attrs = {
                "testonly": True,
                "deps": deps,
                "tags": ["clippy"],
            },
        ),
    ]


gazelle_fold.fold(
    name = "rust_clippy_targets",
    params = {
        "kinds": gazelle_fold.param(type = "strings", default = _RUST_RULE_KINDS),
        "target_name": gazelle_fold.param(type = "string", default = "clippy"),
    },
    apply = _apply,
)
