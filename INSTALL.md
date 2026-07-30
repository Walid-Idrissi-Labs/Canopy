# Installing Canopy

## Supported platforms

Canopy runs on macOS and Linux, on both amd64 and arm64.

Windows is not supported. Canopy manages agent subprocesses through unix process groups, and that
has no equivalent on Windows today. Support would need real process and terminal handling designed
for Windows rather than approximated, and that has not happened yet.

## What it needs at runtime

- `git`, on your `PATH`.
- `/bin/sh`. Canopy runs shell commands, including test commands, through it.
- One credential, which is one of two things. Either an API key for a provider, Anthropic directly
  or any OpenAI-compatible endpoint such as Kimi or MiniMax, added with `canopy keys add`. Or a
  subscription you already pay for, signed in to with `canopy keys signin`; see
  "Signing in with a subscription" below for what each of the three routes needs on the machine.

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

If what you have is a subscription rather than an API key, `canopy keys signin` replaces the first
of those two lines. See "Signing in with a subscription" below.

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
| `CANOPY_GITHUB_CLIENT_ID` | The client id of the GitHub app that `canopy keys signin -route copilot` signs people in as. Not a secret. See "The Copilot route" below. |
| `CANOPY_GITHUB_CLIENT_SECRET` | Only needed if the app above issues user tokens that expire, since GitHub will only renew one for an app that can prove who it is. The recommended registration does not, and then this is never read. |
| `CANOPY_GITHUB_SCOPES` | Space-separated OAuth scopes for that sign-in, overriding the default `copilot read:user`. GitHub documents no scope for Copilot, so the default is evidence rather than fact and this exists to narrow it by experiment. |
| `COPILOT_CLI_PATH` | Where the GitHub Copilot CLI lives, when it is not on `PATH`. Read by Canopy and by GitHub's SDK, so the two cannot find different binaries. |
| `CANOPY_CLAUDE_ACP` | Where the Claude Code ACP bridge lives, when it is not on `PATH`. Checked rather than trusted: a value pointing at nothing runnable is an error naming the variable, not a fallback to `PATH`. |
| `CANOPY_CODEX` | Where the `codex` binary lives, when it is not on `PATH`. Checked the same way, for the same reason. |
| `CODEX_HOME` | Codex's own variable for where it keeps its state, including the ChatGPT login the app server owns. Canopy reads it and never sets it, so a Codex you have pointed somewhere else is the one Canopy drives. Defaults to `~/.codex`. |

## Signing in with a subscription

If you pay for Claude, Copilot or ChatGPT by the month, you have no API key to paste and
`canopy keys signin` is the way in. Three routes are permitted and each needs something on the
machine that Canopy deliberately does not bundle, so that the vendor's runtime is the version the
vendor currently supports rather than the one Canopy was built against, and so that no proprietary
vendor binary ends up inside Canopy's release archives.

```sh
canopy keys signin mysub -route copilot        # GitHub Copilot
canopy keys signin mysub -route claude-code    # your own Claude Code
canopy keys signin mysub -route codex          # ChatGPT, through OpenAI's Codex app server
canopy keys signin mysub -route codex-device   # the same, with a code to type elsewhere
```

Leaving `-route` off on a build that offers several prints the list and asks you to name one. Every
route prints, before anything is stored, what signing in through it means; that text is worth the
ten seconds. Afterwards, `canopy keys test <name>` says what the vendor currently reports about the
credential, and `canopy keys signout <name>` ends it.

Read the subscription sections of [LIMITATIONS.md](LIMITATIONS.md) before choosing a route. They are
not disclaimers: on two of the three, Canopy's permission gate is not in the path at all.

### The Claude route

Nothing is signed in to here. Canopy holds no Anthropic credential and never sees one, because as of
2026-07-30 Anthropic do not permit third-party tools to offer Claude.ai login and enforce that on
their own servers; LIMITATIONS.md has the full position. Signing in means Canopy finding
the Claude Code you set up yourself, asking it which account it is signed in as, and writing that
down. Two programs have to be present:

```sh
# Claude Code itself, from https://claude.com/claude-code, then:
claude                 # run it once and sign in

# the ACP bridge, which is what lets Canopy talk to it
npm install -g @agentclientprotocol/claude-agent-acp
```

Claude Code does not speak the Agent Client Protocol by itself, which is why the bridge is a
separate package. It is published by the protocol's maintainers rather than by Anthropic, it uses
the account you are already signed in as, and installing it changes nothing about that sign-in. A
machine set up some months ago may have it under its previous name, `claude-code-acp`, and Canopy
accepts either. `CANOPY_CLAUDE_ACP` points at it when it is not on `PATH`.

There is nothing to do for a headless box beyond the above, since neither program needs a browser at
the moment Canopy runs it. Signing in to Claude Code itself is Claude Code's business and happens
before Canopy is involved.

### The Copilot route

This is the one route where Canopy runs the sign-in and holds the resulting token. Two things have
to be present.

**The Copilot CLI**, which is what actually talks to GitHub:

```sh
npm install -g @github/copilot
```

Set `COPILOT_CLI_PATH` if it lives somewhere unusual. Canopy and GitHub's SDK both read that
variable, so the two cannot end up on different binaries.

**A GitHub app of Canopy's own.** No release has a client id compiled in yet, so today every build
needs one supplied. GitHub's own guidance for the Copilot SDK is to create an app, have users
authorise it, and pass their token to the SDK; Canopy needs its own identity for that and must not
borrow another editor's.

1. GitHub, Settings, Developer settings, **OAuth Apps**, New OAuth App.
2. Any name and homepage URL. The callback URL is required by the form and never used: Canopy signs
   people in with the device flow so that nothing has to listen on a port.
3. Tick **Enable Device Flow**.
4. Copy the **Client ID** and set `CANOPY_GITHUB_CLIENT_ID` to it, or build with
   `-ldflags "-X github.com/Walid-Idrissi-Labs/Canopy/internal/provider/copilot.clientID=<id>"`.

An OAuth app rather than a GitHub app, and for one specific reason: an OAuth app's user tokens do
not expire, so Canopy never has to renew one. Renewing needs a client secret, and a program people
download cannot keep a secret. A GitHub app works too, and if you leave expiring user tokens on you
have to supply `CANOPY_GITHUB_CLIENT_SECRET` as well.

The scopes Canopy asks for are `copilot read:user`, and that list is evidence rather than
documentation: GitHub publish no OAuth scope for Copilot anywhere. `CANOPY_GITHUB_SCOPES` overrides
it, space separated, so the question can be narrowed one entry at a time without a rebuild.

The sign-in is a device flow, so it prints a code and a page to type it on and works the same on a
headless box as on a laptop. Nothing listens on a port either way.

### The ChatGPT route

Canopy asks OpenAI's own `codex app-server` to sign you in, and it does everything: it builds the
authorisation URL, hosts the callback on its own loopback port, talks to OpenAI, and keeps the grant
in `$CODEX_HOME` afterwards. Canopy holds no ChatGPT token at any point. One program has to be
present:

```sh
npm install -g @openai/codex
# or
brew install codex
```

`CANOPY_CODEX` points at it when it is not on `PATH`. `CODEX_HOME` is Codex's own variable and
Canopy reads it rather than setting it, so if you keep Codex's state somewhere other than `~/.codex`
that is the login Canopy will find and report.

On a headless box, use `-route codex-device`, which asks OpenAI for a code you can type on any other
device. Canopy picks that flow by itself when the session looks remote, meaning `SSH_CONNECTION`,
`SSH_TTY` or `SSH_CLIENT` is set, or when a Linux machine has neither `DISPLAY` nor `WAYLAND_DISPLAY`.
The browser flow's callback is a localhost address, so over ssh it would never arrive and the wait
would look like a hang.

If `codex` is missing and a login already exists in `$CODEX_HOME`, Canopy says whose login it is and
that the program rather than the sign-in is what is missing. It does not take the tokens out of that
file and use them; LIMITATIONS.md says why.

The config directory is `~/Library/Application Support/canopy` on macOS and
`$XDG_CONFIG_HOME/canopy`, usually `~/.config/canopy`, on Linux. Credentials metadata, secrets if
the file backend is in use, history and global commands all live there together, so a tool people
try out is one they can cleanly remove.
