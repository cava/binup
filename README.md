# binup

Install prebuilt binaries from GitHub releases and keep them up to date.

```
$ binup install junegunn/fzf
$ binup outdated
$ binup upgrade fzf
$ binup list
$ binup remove fzf
```

Binaries land in `~/.local/bin/` by default (override with `$BINUP_BIN` or `--to`). Before writing anything, `install`/`upgrade` show the files that would be created or overwritten and the asset's checksum status, then ask for confirmation (pass `--yes`/`-y` to skip the prompt, e.g. in scripts/CI).

Supports `.tar.gz`, `.tar.bz2`, `.tar.xz`, `.zip`, single compressed files (`.gz`/`.bz2`/`.xz`), raw binaries, and checksum verification against a release's published checksum file or `--verify-sha256`. Package formats (`.deb`, `.rpm`, `.apk`, `.dmg`, `.msi`) are not supported.

Use `--min-release-age=DUR` (e.g. `30m`, `72h`, `3d`) to ignore releases published more recently than that, guarding against just-published or compromised releases — similar to npm's `minimum-release-age`.

binup keeps a manifest of installed tools at `~/.local/share/binup/manifest.json` (override with `$BINUP_MANIFEST` or `$XDG_DATA_HOME`). Reads GitHub tokens from `$BINUP_GITHUB_TOKEN` or `$GITHUB_TOKEN`.
