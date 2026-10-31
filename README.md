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

## Mac: install the server

Once:

```sh
cd server
go run . install
```

That copies `herdr-remote` to `~/.local/bin`, and starts it at every login
through launchd (it restarts if it stops, and waits for Tailscale if it
starts first). Then it shows a QR code for pairing a phone, and under it the
address and a six-digit code:

```
Installed: herdr-remote now starts at login and restarts if it stops.

Pair a phone: open Herdr Remote and tap Scan the code.

  ▀▀▀▀▀▀▀▀▀  (QR code)

No camera? Type the address and this code (works once, for 10 minutes):

  address  100.x.y.z:7710
  code     408 971
```

After that:

| Command | Does |
|---|---|
| `herdr-remote pair` | QR code and a fresh code, to pair another phone |
| `herdr-remote status` | whether it runs, its address, and if it answers |
| `herdr-remote uninstall` | stop starting at login (the token stays) |
| `herdr-remote` | serve in the terminal instead, without launchd |

Run `go run . install` again after changing the code: it replaces the binary
and restarts the server. The log is `~/Library/Logs/herdr-remote.log`.

Tailscale has to be on (`tailscale up`, or the menu bar app); the address is
what `tailscale ip -4` says. Without Tailscale, use an SSH tunnel:
`herdr-remote install -addr 127.0.0.1:7710` on the Mac, and on the phone
(Termux) `ssh -L 7710:127.0.0.1:7710 akbar@<mac>`, then pair with
`127.0.0.1:7710`.

The token lives in `~/.config/herdr-remote/token` (mode 0600). The QR code
holds it; the six-digit code trades for it once (`POST /api/pair`), for ten
minutes, and stops working after five wrong tries.

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
