# Releasing Canopy

The whole release is one pushed tag. Everything below exists so that the tag is the only thing you
have to get right on the day.

## What a tag does

Pushing a tag matching `v*` runs `.github/workflows/release.yml`, which runs GoReleaser, which
builds four binaries, packs them into tar.gz archives with a checksums file, and attaches them to a
GitHub release with a changelog grouped by conventional commit prefix.

A tag containing a hyphen is a prerelease, and two separate mechanisms act on that, which is worth
knowing because the first release got one of them wrong.

`homebrew_casks.skip_upload: auto` reads the parsed version, so the cask is skipped for any tag with
a prerelease suffix. That worked on the first release, which is why it published with no tap and no
token.

`release.prerelease: auto` is what marks the GitHub release itself. It is not the default, and
without it v0.1.0-alpha.1 published as a normal release and showed on GitHub as the latest stable
version. It is set now. A release already published with the wrong label can be corrected in the
GitHub release editor by ticking "Set as a pre-release"; the tag and the assets are untouched.

## Before the first tag

Check the release builds on your own machine, which takes about thirty seconds and is the only way
to find a packaging mistake before it is public:

```sh
make snapshot          # goreleaser build --snapshot --clean
./dist/canopy_darwin_arm64_v8.0/canopy version
rm -rf dist            # 62 MB of binaries, and gitignored rather than committed
```

Then check the tree is clean and the tests pass, because a tag records whatever is committed and
there is no taking it back:

```sh
make test && make lint && git status --short
```

## Cutting a release

```sh
git tag -a v0.1.0-alpha.1 -m "pre-alpha"
git push origin v0.1.0-alpha.1
```

Watch the workflow. If it fails, delete the tag from both places, fix, and tag again:

```sh
git push --delete origin v0.1.0-alpha.1
git tag -d v0.1.0-alpha.1
```

Deleting a tag that people may already have pulled is bad practice, which is exactly why the
snapshot build above is worth the thirty seconds.

## Homebrew

Homebrew finds a tap by repository name and the name is not a free choice. The repository must be
called `homebrew-<something>`, because Homebrew strips the `homebrew-` prefix to get the tap name.
A repository called `canopy-brew` cannot be tapped at all.

The config here expects `Walid-Idrissi-Labs/homebrew-tap`, so:

```sh
brew tap Walid-Idrissi-Labs/tap
brew install canopy
# or in one step
brew install Walid-Idrissi-Labs/tap/canopy
```

`homebrew-tap` rather than `homebrew-canopy` on purpose. A tap can hold any number of casks, and
naming it after the first one means the second project needs a second tap or an awkward name.

### Setting it up, once

1. Create a public repository `Walid-Idrissi-Labs/homebrew-tap`. It can be empty; GoReleaser creates
   the `Casks/` directory and the file in it.
2. Make a personal access token with **contents: write** on that repository. A fine-grained token
   scoped to the one repository is enough.
   The workflow's own `GITHUB_TOKEN` cannot do this. It is scoped to the repository the workflow
   runs in, and a token that cannot write to a different repository fails in a way that reads like a
   permissions bug in GoReleaser rather than a token that was never going to work.
3. Add it to the Canopy repository as the secret `HOMEBREW_TAP_TOKEN`.
4. Tag a version with no hyphen in it. That is the release that writes the cask.

### What it does not cover

Homebrew casks are macOS only. Linux users install with `go install` or from the release archive.
That is the cost of `homebrew_casks`, which GoReleaser now requires: the older `brews` key produced
a formula that worked on both and is deprecated.

The binary is not signed or notarised, so macOS quarantines it and the cask removes the quarantine
attribute on install. Signing properly needs an Apple Developer account.

## Debian and apt

Not done, and worth saying why rather than leaving it as an omission.

An apt repository is not a file you publish. It is a directory served over HTTP with `Packages`,
`Release` and `InRelease` indexes, signed with a GPG key that every user has to be told to trust,
and rebuilt on every release. That is infrastructure with its own uptime and its own key rotation,
and a strange thing to run for a pre-alpha whose audience already has a Go toolchain.

If you want `.deb` files available for download without running a repository, GoReleaser can build
them and attach them to the GitHub release with an `nfpms` block. That gets somebody to
`dpkg -i canopy.deb` without anything to host or sign.

## Versions

`v0.1.0-alpha.1`, then `-alpha.2` and so on while the shape is still moving. `v0.1.0` when Codex and
the classmate have both signed the phase gates, since that is what the ledger means by done.

Nothing here is API stable and the version says so. Going to `v1` is a promise about compatibility,
and it should be made deliberately rather than reached by counting.
