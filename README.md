# Herdsman

An Android app for watching your coding agents in [Herdr](https://herdr.dev)
on your Mac. It works with both the Herdr app and the herdr CLI. See who
needs you, read and answer their chat, commit their changes, start or resume
tasks, and type in their terminals.

- `server/` is `herdsman`, a small Go server on the Mac. It calls `herdr-app`
  and `hc` (never a shell) and herdr's socket, and wants a token on every
  request.
- `android/` is the app, in Kotlin and Jetpack Compose. Design notes are in
  [DESIGN.md](DESIGN.md), the server's endpoints in [docs/API.md](docs/API.md).

## Safety

Whoever has the token can type into your terminals, so they can run commands
on your Mac. The server listens only on the Mac's Tailscale address, which
only your own devices can reach. Don't put it on the internet.
`herdsman -new-token` replaces the token, and every phone pairs again.

## Install the server

Download `herdsman-<version>-darwin-arm64` (or `-darwin-amd64` for Intel)
from the [latest release](https://github.com/akbxr/herdsman/releases/latest):

```sh
chmod +x herdsman-*-darwin-arm64
xattr -d com.apple.quarantine herdsman-*-darwin-arm64 2>/dev/null
./herdsman-*-darwin-arm64 install
```

From source, run `cd server && go run . install`. Run it again after changing
the code.

`install` copies the binary to `~/.local/bin` and starts it at login through
launchd. It then prints a QR code, the address and a six-digit code for
pairing. The code works once, for ten minutes, and dies after five wrong
tries.

| Command | Does |
|---|---|
| `herdsman pair` | a new QR code and code, for another phone |
| `herdsman status` | whether it runs and answers |
| `herdsman uninstall` | stop starting at login |
| `herdsman` | run in the terminal, without launchd |

The log is `~/Library/Logs/herdsman.log`, the token
`~/.config/herdsman/token`.

Without Tailscale, use an SSH tunnel. Run
`herdsman install -addr 127.0.0.1:7710` on the Mac and
`ssh -L 7710:127.0.0.1:7710 you@<mac>` on the phone (Termux), then pair with
`127.0.0.1:7710`.

## Install the app

Get `herdsman-<version>.apk` from the
[latest release](https://github.com/akbxr/herdsman/releases/latest), or
`adb install -r` it. Each release installs over the last and keeps the
pairing.

To build it yourself:

```sh
cd android
export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
./gradlew assembleRelease   # app/build/outputs/apk/release/app-release.apk
```

Use the release build. It scrolls much smoother than the debug one. It is
signed with the release key if `~/.android/herdsman-release.properties`
exists, otherwise with your debug key, and then you have to uninstall a
published APK first.

Open Herdsman and scan the QR code, or type the address and the six-digit
code. The phone needs Tailscale on, signed in to the same account.

## What it does

**Herd.** Every terminal as an ear tag, colored by state: yellow needs you,
blue working, green done, grey idle. What needs you comes first. Each agent
shows its session's title, like "tego · Fix the nav bar". The magnifier
searches agent, title, tab, space, folder, address, machine and state, and
every word has to match ("claude pki"). Press and hold a terminal to pin it
or its whole tab to the top, or to split, zoom, rename or close it.

**Chat.** The agent's conversation, read from its own transcript, with
Markdown answers and tool runs folded into one line. Below it is what the
agent is doing right now. When it asks a question, the menu's choices become
buttons, and the Mac checks the menu again before pressing anything. The
chat loads the last 120 messages and older ones as you scroll up. Search
covers all of it. Works for tego, Claude Code and Codex.

A bar above the message box shows the agent's todo list (done count and
the item in progress) and how many subagents run. Tap it for the whole list
and every subagent, with what it was asked and its report. This reads tego's
todo, task and background tasks, Claude Code's todos and Agent tool, and
Codex's plan.

The message box completes `/` commands and skills, and `@` files. Esc stops
the agent, and quick replies (continue, yes, /compact) are one tap.

**Model and thinking.** For tego and Claude Code, the first chip under the
message box shows the model and thinking level. Change them there while the
agent is idle. The app types the agent's own `/model` and `/thinking` (or
`/effort`) commands.

**Changes.** Uncommitted files in the agent's folder, with diffs. Commit a
chosen set of files with a plain `git commit`, so your hooks run. The app can
write the message for you, or ask the agent to commit. Discard puts one file
back to the last commit.

**New task and resume.** Pick a folder, an agent, a model and a prompt, and
the Mac opens a tab running it. Earlier tego and Claude Code sessions can be
resumed in a new tab. A session that is still running opens its terminal
instead.

**Terminal.** The live terminal, drawn with xterm.js, with a key bar for Esc,
Tab, Ctrl, arrows and Paste. Once you type on the phone, the program
reflows to the phone's size. Typing on the Mac takes it back.

**Screen.** A picture of the Herdr window every 3 s, with the Mac's tabs and
Next layout.

**Notifications.** Turn them on at the bottom of the herd. You get one when
an agent needs you, finishes or fails, with buttons to answer. There is no
push service. A foreground service keeps a long poll open to the Mac, so
allow it to run in the background. On Xiaomi, POCO and Redmi also turn on
Autostart and set Battery saver to No restrictions, or HyperOS kills it. A
plain shell sends no statuses, so it never notifies.

## Releases

Commit messages follow [Conventional
Commits](https://www.conventionalcommits.org). On every push to `master`,
[release-please](https://github.com/googleapis/release-please) updates a
release pull request (`fix:` bumps the patch, `feat:` the minor). Merging it
tags the version, and `.github/workflows/release.yml` attaches the signed APK,
the two server binaries and `SHA256SUMS`.

The signing key lives in the repository secrets `HERDSMAN_KEYSTORE_BASE64`,
`HERDSMAN_KEYSTORE_PASSWORD`, `HERDSMAN_KEY_ALIAS` and
`HERDSMAN_KEY_PASSWORD`. Keep a copy of the keystore elsewhere. An APK signed
with another key can't update the installed app.

CI (`.github/workflows/ci.yml`) runs the server tests, the app build, unit
tests and screen snapshots. Re-record snapshots with
`./gradlew recordPaparazziDebug`.
