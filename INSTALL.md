# Installing Canopy

## Supported platforms

Canopy runs on macOS and Linux, on both amd64 and arm64.

Windows is not supported. Canopy manages agent subprocesses through unix process groups, and that
has no equivalent on Windows today. Support would need real process and terminal handling designed
for Windows rather than approximated, and that has not happened yet.

## What it needs at runtime

- `git`, on your `PATH`.
- `/bin/sh`. Canopy runs shell commands, including test commands, through it.
- An API key for at least one provider: Anthropic directly, or any OpenAI-compatible endpoint
  (for example Kimi or MiniMax). Add one with `canopy keys add` after installing.

A credential on anything other than Anthropic also needs a model, since there is no default anybody
could guess for somebody else's gateway:

```sh
canopy keys add nim -provider openai-compatible \
  -base-url https://integrate.api.nvidia.com/v1 -model minimaxai/minimax-m2.7
canopy keys list      # the MODEL column says NOT SET where one is missing
```

## Homebrew

Not available yet, and it will not be until the first release without a prerelease suffix. The tap
is configured and waiting; see RELEASING.md for what has to exist before it publishes. When it does:

```sh
brew install Walid-Idrissi-Labs/tap/canopy
```

macOS only. Homebrew casks do not install on Linux, so use one of the options below there.

## Option 1: go install

If you already have Go 1.26 or newer:

```sh
go install github.com/Walid-Idrissi-Labs/Canopy/cmd/canopy@latest
```

This puts a `canopy` binary in `$(go env GOPATH)/bin`, so make sure that directory is on your
`PATH`. The binary built this way reports its version as `dev`, since `go install` does not run
the build-time flags a release does; that is expected and does not mean anything is broken.

## Option 2: download a release archive

Download the archive for your platform from the
[releases page](https://github.com/Walid-Idrissi-Labs/Canopy/releases), matching your OS
(`darwin` or `linux`) and architecture (`amd64` or `arm64`). Each release also publishes a
`checksums.txt` to verify the download against.

```sh
tar -xzf canopy_<version>_<os>_<arch>.tar.gz
sudo mv canopy /usr/local/bin/
```

Adjust the destination to wherever is already on your `PATH`.

## Option 3: build from source

```sh
git clone https://github.com/Walid-Idrissi-Labs/Canopy.git
cd Canopy
make install
```

This builds with the same version, commit and date stamping a released binary gets, so
`canopy version` reports something meaningful rather than `dev`. `make build` does the same thing
without installing, and leaves the binary at `./canopy` in the repository.

## After installing

Add a key for at least one provider, then run an agent against it:

```sh
canopy keys add claude --provider anthropic
canopy
```

See [README.md](README.md) for what happens from there.
