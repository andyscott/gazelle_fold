"""Example fold that maintains one package-local Clippy target per Rust package."""

def _local_rule_labels(ctx, kinds):
    return [":" + rule.name for rule in ctx.rules_matching(kinds = kinds)]

def rust_clippy_fold(name, kinds = None, target_name = "clippy"):
    """Register a fold that derives one managed `rust_clippy` target per package.

    The fold intentionally derives from concrete local Rust rule kinds only. That
    keeps package-local aggregators tied to the rules they summarize instead of
    accidentally sweeping generated wrappers or parent rollups into the target.
    """

    default_kinds = kinds or [
        "rust_binary",
        "rust_library",
        "rust_test",
    ]

    def _apply(ctx):
        active_kinds = ctx.params.get("kinds", default_kinds)
        active_target_name = ctx.params.get("target_name", target_name)
        deps = _local_rule_labels(ctx, active_kinds)
        return [
            gazelle_fold.rule(
                kind = "rust_clippy",
                name = active_target_name,
                present = deps != [],
                bool_attrs = {"testonly": True},
                string_list_attrs = {
                    "deps": deps,
                    "tags": ["clippy"],
                },
            ),
        ]

    gazelle_fold.fold(
        name = name,
        params = {
            "kinds": gazelle_fold.param(type = "strings", default = default_kinds),
            "target_name": gazelle_fold.param(type = "string", default = target_name),
        },
        apply = _apply,
    )

rust_clippy_fold(name = "rust_clippy_targets")
