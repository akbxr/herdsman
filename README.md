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
- **Chat** (tap an agent's tag): its conversation, read from the agent's own
  transcript: what you asked, its answers in Markdown, and the tools it ran
  folded into one line each run (tap to see commands, results, failures).
  Below it, what it does now: the tool running at the moment, or "Thinking…",
  in a moving shimmer; a message you send shows at once ("Sent") until the
  transcript has it. When it asks something, the choices of the menu on its
  screen ("Yes", "Yes, and don't ask again", "No…") are buttons; the Mac
  reads the menu again before pressing ↑/↓ and Enter, and types nothing if it
  changed.

  It opens on the last 120 messages; scroll to the top for older ones, back
  to the start of the conversation. The search in the heading looks through
  all of it: matches marked, ↑/↓ and "3 of 12" go from one to the next.
  Pictures in your messages (pasted screenshots, photos attached from the
  phone) show in your bubble; tap one to see it whole and pinch to zoom.
- **Changes**: what is uncommitted in the agent's folder (git, against the
  last commit, new files included): each file with +/− counts, tap for its
  diff. Works for tego, Claude Code and Codex (a shell
  has no chat and opens on its terminal). **Chat | Terminal** at the top
  switches.

  The message box suggests as you type: `/` lists the agent's commands, your
  custom commands (`~/.claude/commands`, `.tego/commands`…) and skills; `@`
  finds files in the folder the agent works in. Under it, **Esc** stops the
  agent, and quick replies (continue, yes, /compact…) put their text in the
  box; hold one to change or remove it, **+** adds one.
- **Model and thinking**: on the Chat tab of tego and Claude Code, the first
  chip under the message box shows what the agent runs with ("◆ Opus 5.5 ·
  high"). Tap it for a sheet with the thinking levels (Claude calls them
  effort) and the models; **Apply** types the agent's own commands
  (`/thinking low`, `/model anthropic/claude-sonnet-5-5:low` for tego;
  `/model claude-sonnet-5-5`, `/effort low` for Claude Code, confirming its
  "Switch model?" question). Only while the agent is idle or done: while it
  works or waits for you the sheet says so and Apply stays off. The chip
  shows the new choice at once, then what the agent's screen says. Both
  agents keep what you pick as their default for new sessions too. tego's
  models are the ones it fetched from your providers (`~/.tego/models.json`),
  Claude Code's those of its `/model` picker. The current values come from
  tego's status line, and from Claude Code's transcript, screen, flags and
  settings. A running Codex can't switch (its `/model` takes no argument).
- **New task** (**New** on the herd): a folder, an agent, what to do; the Mac opens
  a tab there running it. For tego, Claude Code and Codex, **Model** and
  **Thinking** pickers next to the agents pass `--model`/`--thinking`
  (tego), `--model`/`--effort` (Claude Code) or `-m`/`-c
  model_reasoning_effort=` (Codex); "Default" leaves the agent's own. The
  phone remembers the last pick per agent.
- **Resume**: pick up an earlier tego or Claude Code conversation in a new
  tab, in the folder it was in (`tego --resume <id>`, `claude --resume
  <id>`), and open it on the phone. Once a folder is picked, New task lists
  **Earlier sessions here** above the agents; **Resume a session** (**New**
  on the herd) lists the recent ones anywhere. Each shows its title (the
  agent's own, else your first message), agent, when, and how many messages
  you wrote. Sessions with nothing said are left out; one running in a
  terminal now says so and opens that terminal instead of resuming it twice.
  Codex isn't offered: the Codex installed (0.29) has no resume command.
- **Terminal**: the terminal itself, live (`hc attach` over a
  WebSocket, drawn by xterm.js with the Mac's Tokyo Night colors and Nerd
  Font icons). Tap it to type: the phone's keyboard goes straight to the
  program, without suggestions or autocorrect. The key bar adds Esc, Tab,
  Ctrl (sticky: tap it, then a letter), arrows, Enter and Paste, enough to
  answer a menu without opening the keyboard. **Show on Mac** brings its
  tab up on the Mac.

  Size: herdr-core gives a terminal the size of whoever was active last. While
  you only watch, it keeps the Mac's size and the font shrinks to fit the
  width; once you type on the phone, the program reflows to the phone's size
  (the line under the terminal says which), and typing on the Mac takes it
  back.
- **Screen**: a picture of the Herdr window every 3 s (pinch to zoom,
  double-tap to reset), the Mac's tabs to switch between, and **Next layout**.
- **Notifications** (turn them on at the bottom of the herd): when an agent
  needs you, finishes or stops with an error, in its own words ("Allow
  `rm -rf build`?", the first line of its answer). Tap one to open that
  terminal. Nothing is posted while the app is on screen. A needs-you
  notification gets the menu's choices as buttons (three at most; Allow and
  Deny when no menu is found), finished or failed ones a Reply.

  There is no push service: a foreground service on the phone waits on the
  Mac's `/api/events` over Tailscale, with a quiet "Watching your Mac"
  notification. For it to keep running while the phone sleeps:
  - tap **Let it run in the background** in the app (Android's battery
    exemption);
  - on Xiaomi, POCO and Redmi phones also: Settings → Apps → Herdr Remote →
    **Autostart** on, and **Battery saver** → **No restrictions**, or
    HyperOS stops it anyway;
  - Tailscale has to stay on on the phone.

  It starts again after the phone restarts. Statuses come from the programs
  themselves (OSC 7501: tego, Claude Code, Codex, Pi, Crush send them); a
  plain shell has none, so it never notifies.

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
| GET | `/attach?terminal=t12&cols=&rows=&token=` | WebSocket: `hc attach` in a PTY; binary frames are terminal bytes both ways, text frames `{"type":"size"}` (server) and `{"type":"resize"}`, `{"type":"reply"}` (phone) |
| POST | `/upload?terminal=t12&name=x.png` | body = the file; `hc put` places it beside the terminal → `{path, bytes}` |
| GET | `/events?after=41&wait=25` | long poll for agents' status changes → `{seq, events}`; without `after`: `{seq, current}` |
| POST | `/answer` | `{terminal, seq, answer: allow\|deny\|reply\|option, text?, option?}`: answers a waiting agent; 409 when the prompt is gone |
| GET | `/chat?terminal=t12&version=&limit=120` | the last `limit` items of the agent's conversation from its transcript → `{agent, source, version, items, status, live, start, limit, model, modelLabel, thinking}`; `start: true` when nothing older is left; `same: true` when unchanged (the version includes the limit). Items carry a stable `id` and, for your messages, picture ids in `images` |
| GET | `/image?id=&w=480` | a picture of the chat by id (an image block's bytes, or an image file named in a message), scaled to fit `w` pixels when given |
| GET | `/prompt?terminal=t12` | the menu on the terminal's screen → `{prompt: {question, options, selected}}` or `null` |
| GET | `/changes?terminal=t12&version=` | uncommitted git changes in the agent's folder: files with status, counts and patch |
| GET | `/commands?terminal=t12` | the agent's slash commands, custom commands and skills |
| GET | `/files?terminal=t12&q=att` | files in the agent's folder, best matches first |
| GET | `/folders` | where a new task can start: `{home, recent, projects}` |
| GET | `/agents` | the agents installed on the Mac |
| POST | `/task` | `{cwd, agent, prompt, model?, thinking?}`: a new tab running the agent; model and thinking become its start flags, checked against `/models` → `{terminal, tab, prompt}` |
| GET | `/sessions?cwd=&agent=&limit=50` | sessions tego and Claude Code can resume, newest first, in `cwd` or anywhere (not temporary or deleted folders) → `{home, sessions: [{agent, id, title, cwd, time, messages, estimate, size, running}], partial}`; `running`: the terminal it runs in now; `estimate`: `messages` is a lower bound, not counted to the end yet; `partial`: the scan ran out of time. Reads the start and end of each transcript, keeps what it read until the file changes |
| POST | `/resume` | `{agent, id, model?, thinking?}`: a new tab in the session's folder running `tego --resume <id>` / `claude --resume <id>` → `{terminal, tab, space, agent, id, cwd}`; 404 for an unknown session, 400 for a malformed id or an empty session, 409 when its folder is gone or it runs already (`terminal` names it) |
| GET | `/models?agent=tego\|claude\|codex` | `{agent, models: [{id, label, group}], thinking: [{id, label, detail}], thinkingName, live, note}` (kept a minute) |
| POST | `/session` | `{terminal, agent?, model?, thinking?}`: switches a running tego or Claude Code with its own slash commands → `{agent, sent, current}`; 409 while it works or waits for you, or when the terminal no longer runs `agent`; 400 for a model or level it doesn't offer |

The terminal page (`android/app/src/main/assets/term/terminal.html`) also
opens in a desktop browser for debugging: `terminal.html#ws=<attach URL>`.

## Snapshots

The screens render on the JVM with sample data (no device needed):
`./gradlew recordPaparazziDebug`, images in
`android/app/src/test/snapshots/images/`.
