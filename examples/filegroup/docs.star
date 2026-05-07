"""Example fold that keeps one package-local markdown filegroup in sync."""

load("std:lib/filegroup.star", "filegroup")


def markdown_docs_fold(name, target_name = "docs"):
    def _apply(ctx):
        docs = ctx.matching_files(include = ["*.md"])
        return [
            filegroup(
                name = ctx.params.get("target_name", target_name),
                srcs = docs,
                present = docs != [],
            ),
        ]

    gazelle_fold.fold(
        name = name,
        params = {
            "target_name": gazelle_fold.param(type = "string", default = target_name),
        },
        apply = _apply,
    )


markdown_docs_fold(name = "markdown_docs")
