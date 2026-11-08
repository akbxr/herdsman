# Herdsman API

Everything is under `/api` and needs `Authorization: Bearer <token>`, except
`/pair`.

| Method | Path | Does |
|---|---|---|
| POST | `/pair` | `{code}` → `{token, host}`; no token needed, the code is the key |
| GET | `/ping` | host name, version; `routes`: every way phones reach this Mac, fastest first (`http://<LAN ip>:7710` with `-lan`, `http://<Tailscale ip>:7710`, `https://<tunnel hostname>`) |
| GET | `/state` | `herdr-app status` + `herdr-app ls`, and `machines`: the array of `hc machine ls --json` (left out when hc can't list them); `herdr`: the herdr CLI's default session, `session.snapshot`'s snapshot `{workspaces, tabs, panes, agents, layouts, focused_*_id, …}` (left out when herdr doesn't answer within 2 s). Either the Herdr app or the herdr CLI is enough: without the app, `status` and `ls` are left out; 502 only when neither answers. `titles`: `{terminal: title}`, the title of the session each agent works in (tego and Claude Code, on this Mac), refreshed in the background at most every 5 s while asked |
| GET | `/shot?max=1600` | JPEG of the window |
| POST | `/do` | `{command, args?}` → `herdr-app do` |
| GET | `/read?terminal=t12&lines=200` | `hc read`; a herdr pane: `pane.read` as plain text (`visible`, or `recent` with `lines` more than the screen) → `{text, complete, how}` |
| POST | `/send` | `{terminal, text, force?}` → `hc send`; a herdr pane: an agent herdr knows gets `agent.prompt` (refused while it waits for an answer, unless `force`), anything else `pane.send_input` (the text, then Enter) → `{ok, how: prompt\|typed}` |
| POST | `/keys` | `{terminal, keys: [...]}` → `hc keys`; a herdr pane: hc's key names translated to herdr's (below), in order; 400 for a key herdr doesn't know |
| POST | `/interrupt` | `{terminal}` → `hc interrupt`; a herdr pane: Esc when herdr sees an agent there, else Ctrl-C → `{ok, key}` |
| GET | `/attach?terminal=t12&cols=&rows=&token=` | WebSocket: `hc attach` in a PTY; binary frames are terminal bytes both ways, text frames `{"type":"size"}` (server) and `{"type":"resize"}`, `{"type":"reply"}` (phone); a herdr pane: a repaint stream (below) |
| POST | `/upload?terminal=t12&name=x.png` | body = the file; `hc put` places it beside the terminal → `{path, bytes}`; a herdr pane's goes to `~/Library/Application Support/herdsman/uploads/<pane>/` |
| GET | `/events?after=41&wait=25` | long poll for agents' status changes → `{seq, events}`; without `after`: `{seq, current}` |
| POST | `/answer` | `{terminal, seq, answer: allow\|deny\|reply\|option, text?, option?}`: answers a waiting agent; 409 when the prompt is gone |
| GET | `/chat?terminal=t12&version=&limit=120` | the last `limit` items of the agent's conversation from its transcript → `{agent, source, version, items, status, live, start, limit, model, modelLabel, thinking}`; `start: true` when nothing older is left; `work`: `{todos: [{text, status, phase}], agents: [{name, id, kind, task, state, why, report, background, started, ended}]}` from the branch in play (the latest todo list; the newest 40 subagents, `state` running, done, failed, stopped or unknown), left out when there is none; `same: true` when unchanged (the version includes the limit). Items carry a stable `id` and, for your messages, picture ids in `images` |
| GET | `/image?id=&w=480` | a picture of the chat by id (an image block's bytes, or an image file named in a message), scaled to fit `w` pixels when given |
| GET | `/prompt?terminal=t12` | the menu on the terminal's screen → `{prompt: {question, options, selected}}` or `null` |
| GET | `/changes?terminal=t12&version=` | uncommitted git changes in the agent's folder: files with status, counts and patch |
| POST | `/commit` | `{terminal, message, paths}`: commits exactly those changed files in the terminal's git root (`git commit --only`, new files added with `--intent-to-add` first; hooks run) → `{hash, subject, files}`; 422 with git's output when it fails, 400 for a path that isn't changed or leaves the repository |
| GET | `/commit/suggest?terminal=t12&path=…` | a Conventional Commits message written from the changed files' names and statuses (all of them without `path`) → `{message}` |
| POST | `/discard` | `{terminal, path}`: one changed file back to the last commit; a new file is deleted (a regular file inside the repository only) |
| GET | `/commands?terminal=t12` | the agent's slash commands, custom commands and skills |
| GET | `/files?terminal=t12&q=att` | files in the agent's folder, best matches first |
| GET | `/folders?machine=` | where a new task can start: `{home, recent, projects}`; with `machine`, on that machine (recent: its terminals' folders) |
| GET | `/agents?machine=` | the agents installed on the Mac, or on `machine` |
| POST | `/task` | `{cwd, agent, prompt, model?, thinking?, machine?}`: a new tab running the agent; model and thinking become its start flags, checked against `/models`; with `machine`, `cwd` is a folder there and the tab opens in a space on that machine (the active one), else in a new space there → `{terminal, tab, space, agent, prompt}` (`terminal` is `machine/t7` then). `backend: "herdr"` (this Mac only): a new tab of the herdr CLI in the focused workspace, or `workspace: "wF"`, its shell typing the agent's command line → `terminal: "herdr:wF:p9"`, `tab: "wF:t5"`, `space: "wF"` |
| GET | `/machines` | `{machines: [{name, label, host, state, why, temporary, local}]}`: this Mac first (`name` "", `local` true), then hc's machines; `state` connecting, connected, needs_login or unreachable; `error` when hc couldn't list them |
| POST | `/machines` | `{host: "user@box", name?}` → `hc machine add --install` without a terminal (ssh keys only, never a password prompt; 60 s at most) → `{machine}`; 400 for a malformed host or name, 502 with hc's message when it fails |
| POST | `/machines/remove` | `{name}` → `hc machine rm` → `{removed}`; 404 for an unknown machine |
| POST | `/machines/connect` | `{name}`: dials it now (hc's `machine.connect`) → `{machine}`; 409 when it needs a password (`hc machine login NAME` on the Mac) |
| GET | `/sessions?cwd=&agent=&limit=50&machine=` | sessions tego and Claude Code can resume, newest first, in `cwd` or anywhere (not temporary or deleted folders) → `{home, sessions: [{agent, id, title, cwd, time, messages, estimate, size, running}], partial}`; `running`: the terminal it runs in now; `estimate`: `messages` is a lower bound, not counted to the end yet; `partial`: the scan ran out of time. Reads the start and end of each transcript, keeps what it read until the file changes. With `machine` (another one): `{home, sessions: [], partial: false, note}` |
| POST | `/resume` | `{agent, id, model?, thinking?}` (this Mac only: 400 with `machine`): a new tab in the session's folder running `tego --resume <id>` / `claude --resume <id>` → `{terminal, tab, space, agent, id, cwd}`; 404 for an unknown session, 400 for a malformed id or an empty session, 409 when its folder is gone or it runs already (`terminal` names it) |
| POST | `/herdr` | `{method, params, confirmed?}`: one of the herdr CLI's `tab.create`, `tab.close`, `tab.rename`, `tab.focus`, `pane.close`, `pane.split`, `workspace.focus` on its default session, with the params herdr's schema gives it (ids as herdr's, a pane also as `herdr:wF:p2`); `tab.close` and `pane.close` need `confirmed: true` → `{result}` (herdr's result); 400 for another method, param or a missing confirm, herdr's errors as `{error, code}` (404 for `*_not_found`), 503 when herdr isn't running |
| GET | `/models?agent=tego\|claude\|codex&terminal=` | `{agent, models: [{id, label, group}], thinking: [{id, label, detail}], thinkingName, live, note, supported: true}` (kept a minute); for a terminal on another machine `{supported: false, why}` |
| POST | `/session` | `{terminal, agent?, model?, thinking?}`: switches a running tego or Claude Code with its own slash commands → `{agent, sent, current}`; 409 while it works or waits for you, or when the terminal no longer runs `agent`; 400 for a model or level it doesn't offer; for a terminal on another machine `{supported: false, why}` and nothing sent |

## Other machines

Terminals on machines added with `hc machine add` have addresses like
`box/t7`, and every endpoint takes them. The server reads git, transcripts,
commands and files there through `hc exec --on box`. Sessions aren't listed
on other machines, and the chat's model comes from the screen only.

## The herdr CLI

herdr panes are terminals too, addressed `herdr:<pane id>` (`herdr:wF:p2`).
The server talks to `~/.config/herdr/herdr.sock` (`pane.read`, `pane.send_*`,
`pane.process_info`). Statuses come from `pane.agent_status_changed` and
reach `/events` in the same shape as hc's.

Key names for `/keys`, hc's on the left:

| hc | herdr |
|---|---|
| `enter`, `return` | `enter` |
| `esc`, `escape` | `esc` |
| `shifttab`, `btab` | `shift+tab` |
| `ctrl+c`, `C-c`, `^c` | `ctrl+c` |
| `alt+x`, `M-x` | `alt+x` |
| `tab`, `backspace`, `bs`, `space`, arrows, `f1`…`f12`, single characters | the same |
| `home`, `end`, `pgup`, `pgdn`, `del`, `insert` | typed as xterm sequences (`ESC[H`, `ESC[5~`, …) |

Both `/attach` streams are pinged every 30 s, so a quiet terminal stays open through Cloudflare (which closes a WebSocket idle for 100 s).

herdr has no byte stream of a pane, so `/attach` repaints instead. It sends
`{"type":"size","cols","rows","snapshot":true}`, then the history and screen
as ANSI, then a full repaint whenever the screen changes (on `pane.updated`,
after typing, and every 300 ms). A grown pane sends a `size` with
`repaint: true`. `resize` and `reply` are ignored, since the herdr TUI owns
the size.

`android/app/src/main/assets/term/terminal.html#ws=<attach URL>` opens the
terminal page in a desktop browser for debugging.
