# Herdr Remote

Control the Herdr app on your Mac from an Android phone: see which agents
need you, read their terminals, answer them with keys or a message, switch
tabs and layouts, and look at the window.

Two parts:

- `server/`: `herdr-remote`, a small Go server on the Mac. It runs the
  `herdr-app` and `hc` command-line tools (never a shell) and needs a token
  on every request.
- `android/`: the app (Kotlin, Jetpack Compose). Design notes in
  [DESIGN.md](DESIGN.md).

## Safety

Anyone with the token can type into your terminals, which means they can run
commands on your Mac. So:

- By default the server listens only on this Mac's **Tailscale** address.
  Tailscale encrypts the traffic and only your own devices can reach it.
- Don't expose it on the internet or forward the port on your router.
- `herdr-remote -new-token` replaces the token (phones pair again).

## Mac: run the server

```sh
cd server
go build -o ~/.local/bin/herdr-remote .
herdr-remote
```

It shows a QR code for pairing a phone, and under it the address and a
six-digit code:

```
herdr-remote 0.1.0 listening on http://100.x.y.z:7710

Pair a phone: open Herdr Remote and tap Scan the code.

  ▀▀▀▀▀▀▀▀▀  (QR code)

No camera? Type the address and this code (works once, for 10 minutes):

  address  100.x.y.z:7710
  code     408 971
```

`herdr-remote pair` shows a QR code and a fresh code again, also while the
server runs elsewhere (under launchd).

Tailscale has to be on (`tailscale up`, or the menu bar app). Without it,
use an SSH tunnel instead: `herdr-remote -addr 127.0.0.1:7710` on the Mac,
and on the phone (Termux) `ssh -L 7710:127.0.0.1:7710 akbar@<mac>`, then pair
with `127.0.0.1:7710`.

The token lives in `~/.config/herdr-remote/token` (mode 0600). The QR code
holds it; the six-digit code trades for it once (`POST /api/pair`), for ten
minutes, and stops working after five wrong tries.

### Keep it running (optional)

`~/Library/LaunchAgents/dev.akbxr.herdr-remote.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>dev.akbxr.herdr-remote</string>
  <key>ProgramArguments</key><array><string>/Users/akbar/.local/bin/herdr-remote</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardErrorPath</key><string>/tmp/herdr-remote.log</string>
</dict></plist>
```

`launchctl load ~/Library/LaunchAgents/dev.akbxr.herdr-remote.plist`

## Phone: install the app

Build it (needs the Android SDK; Android Studio's JDK works):

```sh
cd android
export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
./gradlew assembleDebug
```

The APK is `android/app/build/outputs/apk/debug/app-debug.apk`. Install it
with `adb install app-debug.apk` (USB debugging on), or copy it to the phone
and open it (allow installing from that app).

Open **Herdr Remote** and pair:

- **Scan the code**: point the phone at the QR code on the Mac. The scanner
  comes from Google Play services, so the app asks for no camera permission.
  Scanning the code with another QR app also works: it opens Herdr Remote.
- **No camera**: type the address and the six-digit code shown under the QR
  code, then **Pair with the code**. The address can leave out `http://`
  and `:7710`.

The phone needs Tailscale on too, signed in to the same account.

## What the app does

- **Herd**: every terminal as an ear tag stamped with its address, colored
  by state: yellow needs you, blue working, green done, grey idle. Sorted
  with what needs you first. The tab bar counts the yellow ones.
- **Agent** (tap a tag): the terminal's screen and history, refreshed every
  1.5 s; keys for answering menus (Esc, arrows, Tab, Enter, y, n, Ctrl+C);
  a message box (`hc send`); **Show on Mac** brings its tab up on the Mac.
  If the agent is asking something, a message is held back until you choose
  **Send anyway**.
- **Screen**: a picture of the Herdr window every 3 s (pinch to zoom,
  double-tap to reset), the Mac's tabs to switch between, and **Next layout**.

## API

All under `/api`, with `Authorization: Bearer <token>` (except `/pair`):

| Method | Path | Does |
|---|---|---|
| POST | `/pair` | `{code}` → `{token, host}`; no token needed, the code is the key |
| GET | `/ping` | host name, version |
| GET | `/state` | `herdr-app status` + `herdr-app ls` |
| GET | `/shot?max=1600` | JPEG of the window |
| POST | `/do` | `{command, args?}` → `herdr-app do` |
| GET | `/read?terminal=t12&lines=200` | `hc read` |
| POST | `/send` | `{terminal, text, force?}` → `hc send` |
| POST | `/keys` | `{terminal, keys: [...]}` → `hc keys` |
| POST | `/interrupt` | `{terminal}` → `hc interrupt` |

## Snapshots

The screens render on the JVM with sample data (no device needed):
`./gradlew recordPaparazziDebug`, images in
`android/app/src/test/snapshots/images/`.
