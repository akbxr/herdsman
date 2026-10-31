# Herdr Remote: design

**Subject.** The agents (tego, Claude Code, omp…) running in Herdr on akbar's
Mac, seen from an Android phone. **Audience:** one developer away from his
desk. **The app's one job:** show which agents need you and let you answer
them in a few taps.

## Direction: ear tags

Herdr is a herder. The things it herds already carry numbers: every terminal
has an address (`t12`, `t14`), the way livestock carry ear tags. Each agent is
drawn as a plastic ear tag (domed top, punched hole, a stamped number), and
the tag's color is its state. You read the herd from across the room:
a yellow tag needs you.

First idea was the usual agent dashboard: cards with a status pill and a
dot. That works for any monitoring app, so it says nothing about Herdr.
The tags keep the same information but come from the subject.

## Tokens

Colors are taken from Tokyo Night Storm, the theme on akbar's Mac, so the
phone reads as the same place.

| Name | Hex | Use |
|---|---|---|
| Pasture night | `#1f2335` | screen background |
| Pen | `#292e42` | raised surfaces: key strip, input, terminal well |
| Ink | `#c0caf5` | text |
| Dust | `#737aa2` | secondary text, idle tags |
| Tag yellow | `#e0af68` | needs you (blocked): the one loud color |
| Working blue | `#7aa2f7` | working tags |
| Done mint | `#9ece6a` | finished turn, unseen |

Type:
- **Martian Mono** (display, used sparingly): tag numbers and the one
  headline ("1 needs you"). Wide and stamped, like embossed plastic.
- **Atkinson Hyperlegible** (body): names, labels, buttons. Made for reading
  at small sizes on a phone in daylight.
- **JetBrains Mono** (terminal): what the terminal shows, the same face as
  Herdr's terminals.

## Layout

```
Herd                          Agent (tap a tag)
┌─────────────────────────┐   ┌─────────────────────────┐
│ AKBARs-MacBook · online │   │ ←  t12  tego    On Mac ▸│
│ 1 needs you             │   │┌───────────────────────┐│
│                         │   ││ terminal text         ││
│ ╭─o─╮ tego              │   ││ (scrolls, newest at   ││
│ │t12│ Tab 1 · Needs you │   ││  the bottom)          ││
│ ╰───╯                   │   │└───────────────────────┘│
│ ╭─o─╮ claude-code       │   │ Esc  ↑  ↓  Tab  ^C  ⏎  │
│ │t14│ Tab 2 · Idle      │   │ [Message tego…   ] Send │
│ ╰───╯                   │   └─────────────────────────┘
├─────────────────────────┤
│  Herd        Screen     │   Screen: the Mac window as a
└─────────────────────────┘   picture, pinch to zoom.
```

The herd is sorted by what needs you: needs you, working, done, idle.

## Signature

The ear tags, and one motion tied to them: a tag that turns yellow swings
once on its hole, the way a tag moves when the animal turns its head. No
other animation in the app. With animations turned off in Android settings
it does not swing.

## Copy

Sentence case, plain verbs. States: "Needs you", "Working", "Done", "Idle".
Buttons say what happens: "Connect", "Send", "Show on Mac".
Errors say what to do: "Can't reach your Mac. Check that herdr-remote is
running and Tailscale is on."
