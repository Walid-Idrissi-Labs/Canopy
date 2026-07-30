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

## Linux without a keychain

Canopy puts secrets in the operating system credential store: the Keychain on macOS, a D-Bus
Secret Service such as gnome-keyring or KWallet on Linux. Plenty of Linux machines have no such
thing. A container, a CI runner, a server you only ever reach over SSH, and most WSL setups have
no D-Bus session at all, and on those `canopy keys add` fails on the first command with an error
about storing in the OS keychain.

There is one way out:

```sh
export CANOPY_KEY_BACKEND=file
canopy keys add claude --provider anthropic
```

That writes secrets to `credentials.json` in Canopy's config directory, as plain JSON, with the
file mode set to 0600 and the directory to 0700.

Be clear about what you are choosing. Plain JSON means any process running as you can read your
API keys, a backup or a synced home directory carries them off the machine in the clear, and file
permissions are the only thing protecting them, where the keychain would also require an unlock.
It is worse than the keychain and there is no version of it that is not. Canopy never selects it
for you and prints a warning on every command while it is set, because the person who exported the
variable is often not the person who later assumes their keys are encrypted.

Use it anyway when the alternative is not using the tool: a headless box, a container you rebuild
often, a CI runner where the key comes from the job's own secret store and the machine is thrown
away afterwards. On a laptop with a working keychain, do not.

To go back, unset the variable and add the keys again. The two backends do not share storage, so a
credential written to the file is not in the keychain and will not be found there.

## Environment variables

| Variable | What it does |
| --- | --- |
| `CANOPY_KEY_BACKEND` | Set to `file` to store secrets in `credentials.json` instead of the OS keychain. Nothing else changes anything. See above for the tradeoff. |
| `CANOPY_HISTORY` | Full path to the session history database, overriding the default `history.db` in the config directory. Parent directories are created. For an encrypted volume, a synced home directory you want conversations kept out of, or a throwaway file while trying Canopy out. |
| `CANOPY_THEME` | Picks a palette by name: `canopy` is the default, `mono` is the monochrome one. An unrecognised name falls back to the default rather than failing, since a typo in an environment variable should not stop the program from starting. |
| `NO_COLOR` | Honoured, and it wins over `CANOPY_THEME`. Set to anything except the literal `0`, including empty, and Canopy starts monochrome. Every state carries a word and a glyph rather than colour alone, so nothing becomes unreadable. |
| `CANOPY_COMMANDS_FILE` | Path to the global custom command definitions, overriding `commands.json` in the config directory. |

The config directory is `~/Library/Application Support/canopy` on macOS and
`$XDG_CONFIG_HOME/canopy`, usually `~/.config/canopy`, on Linux. Credentials metadata, secrets if
the file backend is in use, history and global commands all live there together, so a tool people
try out is one they can cleanly remove.
