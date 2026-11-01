package dev.akbxr.herdrremote

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.Text
import androidx.compose.foundation.layout.padding
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import app.cash.paparazzi.DeviceConfig
import app.cash.paparazzi.Paparazzi
import org.junit.Rule
import org.junit.Test

/** Renders each screen with sample data shaped like the server's answers. */
class ScreensSnapshotTest {
    @get:Rule
    val paparazzi = Paparazzi(deviceConfig = DeviceConfig.PIXEL_6, theme = "android:Theme.Material.NoActionBar")

    private val herd = Herd(
        layout = "main-full",
        agents = listOf(
            Agent("t12", "tego", null, State.NeedsYou, "tb1", "1", true, "~"),
            Agent("t14", "claude-code", null, State.Working, "tb2", "2", false, "~"),
            Agent("t9", "omp", "api refactor", State.Done, "tb3", "3", false, "~"),
            Agent("t3", "shell", null, State.Idle, "tb4", "4", false, "~"),
        ),
        tabs = listOf(Tab("tb1", "1", true), Tab("tb2", "2", false), Tab("tb3", "3", false), Tab("tb4", "4", false)),
    )

    private val terminal = run {
        val inner = 50
        val title = " Allow this bash call? "
        fun row(s: String) = "│ " + s.padEnd(inner - 2) + " │"
        listOf(
            "● Reading cmd/tego/progstatus.go",
            "● Editing cmd/tego/repl.go  (+12 −3)",
            "",
            "┌$title" + "─".repeat(inner - title.length) + "┐",
            row("go test ./cmd/tego/ -run 'Status|Herdr' -race"),
            row(""),
            row("❯ Yes        run it"),
            row("  Always     for this session"),
            row("  No         tell tego what to do instead"),
            "└" + "─".repeat(inner) + "┘",
        ).joinToString("\n")
    }

    @Composable
    private fun Frame(content: @Composable () -> Unit) {
        HerdrTheme { Box(Modifier.fillMaxSize().background(Ink.night)) { content() } }
    }

    @Test fun connect() = paparazzi.snapshot {
        Frame { ConnectScreen(initialAddress = "", incoming = null, onIncomingUsed = {}, onPaired = {}) }
    }

    @Test fun herd() = paparazzi.snapshot {
        Frame {
            Column(Modifier.fillMaxSize()) {
                Box(Modifier.weight(1f)) {
                    HerdScreen("AKBARs-MacBook-Air.local", herd, problem = null, onOpen = {}, onPairAgain = {}, notifyOn = true)
                }
                BottomBar(listOf("Herd" to true, "Screen" to false), badge = "1", onSelect = {})
            }
        }
    }

    @Test fun herdOffline() = paparazzi.snapshot {
        Frame {
            HerdScreen(
                "AKBARs-MacBook-Air.local", herd,
                problem = "Tailscale is off on this phone. Turn it on to reach your Mac.",
                onOpen = {}, onPairAgain = {}, problemAction = "Open Tailscale" to {},
            )
        }
    }

    // The terminal is a WebView (xterm.js), which layoutlib can't draw: the
    // slot shows the sample screen as text to check the chrome around it.
    @Composable
    private fun FakeTerminal() {
        Text(terminal, style = Type.terminal, color = Ink.text, softWrap = false, modifier = Modifier.padding(8.dp))
    }

    @Composable
    private fun AgentView(status: TermStatus, ctrl: Boolean, message: String = "", attachments: List<Attachment> = emptyList(), link: Link = Link.Up) {
        AgentContent(
            link = link, onOpenTailscale = {},
            terminal = "t12", agent = herd.agents[0], status = status, ctrl = ctrl, problem = null,
            message = message, onMessage = {}, attachments = attachments, sending = false, refused = false,
            onBack = {}, onMore = {}, onAttach = {}, onRemove = {}, onSend = {}, onKey = {},
        ) { FakeTerminal() }
    }

    @Test fun agentWatching() = paparazzi.snapshot {
        Frame { AgentView(TermStatus(state = "live", cols = 165, rows = 36, phoneCols = 52, phoneRows = 40), ctrl = false) }
    }

    @Test fun agentDriving() = paparazzi.snapshot {
        Frame { AgentView(TermStatus(state = "reconnecting", cols = 52, rows = 40, phoneCols = 52, phoneRows = 40, driving = true), ctrl = true) }
    }

    @Test fun agentNoTailscale() = paparazzi.snapshot {
        Frame { AgentView(TermStatus(state = "reconnecting", cols = 52, rows = 40, attempt = 2), ctrl = false, link = Link.NoVpn) }
    }

    @Test fun agentMacAsleep() = paparazzi.snapshot {
        Frame { AgentView(TermStatus(state = "reconnecting", cols = 52, rows = 40, attempt = 4), ctrl = false) }
    }

    private val chatItems = listOf(
        ChatItem("user", "the login form loses the email after a failed attempt, fix it"),
        ChatItem("assistant", "Looking at how the form keeps its state."),
        ChatItem("tools", tools = listOf(
            ChatTool("grep", "resetForm", "src/login.tsx:41", false),
            ChatTool("read", "src/login.tsx", "", false),
            ChatTool("bash", "npm test -- login", "1 failing: keeps the email", true),
        )),
        ChatItem("assistant", "Found it. `resetForm()` runs on **every** error, not only on success:\n\n```ts\nif (error) resetForm()\n```\n\n- keep the email on errors\n- clear only the password"),
        ChatItem("tools", tools = listOf(ChatTool("edit", "src/login.tsx", "", false))),
        ChatItem("note", "Earlier messages were summarised to free context."),
    )

    private fun event(state: String, kind: String = "", msg: String = "") =
        AgentEvent(seq = 7, terminal = "t12", state = state, kind = kind, msg = msg, app = "tego", where = "~ / 1")

    @Composable
    private fun ChatScreen(
        status: AgentEvent?, message: String = "", suggestions: List<Pair<String, String>> = emptyList(),
        items: List<ChatItem> = chatItems, prompt: Prompt? = null, pending: List<String> = emptyList(),
        mode: String = "chat", changes: Changes? = null,
    ) {
        AgentContent(
            mode = mode, chatAvailable = true, suggestions = suggestions,
            snippets = Snippets.DEFAULT,
            chatSlot = { ChatView(items, status, "tego", prompt = prompt, pending = pending, onAnswer = {}, onOpenTerminal = {}) },
            changesSlot = { ChangesView(changes, changes?.files.orEmpty()) },
            terminal = "t12", agent = herd.agents[0], status = TermStatus(), ctrl = false, problem = null,
            message = message, onMessage = {}, attachments = emptyList(), sending = false, refused = false,
            onBack = {}, onMore = {}, onAttach = {}, onRemove = {}, onSend = {}, onKey = {},
        ) {}
    }

    @Test fun chatWorking() = paparazzi.snapshot { Frame { ChatScreen(event("working", msg = "Running the tests")) } }

    @Test fun chatNeedsYou() = paparazzi.snapshot { Frame { ChatScreen(event("blocked", "permission", "Allow bash: npm test -- login?")) } }

    @Test fun chatPrompt() = paparazzi.snapshot {
        Frame {
            ChatScreen(
                event("blocked", "permission", ""),
                prompt = Prompt("Do you want to proceed?", listOf("Yes", "Yes, and don't ask again for npm test", "No, and tell tego what to do differently"), 0),
            )
        }
    }

    @Test fun chatRunning() = paparazzi.snapshot {
        Frame {
            ChatScreen(
                event("working"),
                items = chatItems.take(4) + ChatItem("tools", tools = listOf(ChatTool("read", "src/login.tsx", "ok", false), ChatTool("bash", "npm test -- login", "", false))),
                pending = listOf("also check the signup form"),
            )
        }
    }

    @Test fun changes() = paparazzi.snapshot {
        Frame {
            ChatScreen(
                event("working"), mode = "changes",
                changes = Changes(
                    repo = true, root = "/p", branch = "main", more = 0, version = "v",
                    files = listOf(
                        ChangedFile("src/login.tsx", "M", 3, 1, false, "@@ -38,7 +38,9 @@ export function Login() {\n   const [email, setEmail] = useState(\"\")\n-  if (error) resetForm()\n+  if (error) {\n+    setPassword(\"\")\n+  }\n   return (", false),
                        ChangedFile("src/login.test.tsx", "?", 24, 0, false, "@@ -0,0 +1,24 @@\n+import { render } from \"@testing-library/react\"", false),
                        ChangedFile("public/logo.png", "M", 0, 0, true, "", false),
                    ),
                ),
            )
        }
    }

    @Test fun chatLive() = paparazzi.snapshot {
        Frame {
            AgentContent(
                mode = "chat", chatAvailable = true, snippets = Snippets.DEFAULT,
                chatSlot = {
                    ChatView(
                        chatItems.take(4), event("working"), "tego",
                        live = listOf(
                            "Found it. resetForm() runs on every error, so the",
                            "email is cleared together with the password. I'll",
                            "keep the email and clear only the password:",
                            "",
                            "╭─ Edit: src/login.tsx ──────────────",
                            "│ ⣾ running · 1s",
                            "╰────────────────────────────────────",
                        ),
                        onAnswer = {}, onOpenTerminal = {},
                    )
                },
                terminal = "t12", agent = herd.agents[0], status = TermStatus(), ctrl = false, problem = null,
                message = "", onMessage = {}, attachments = emptyList(), sending = false, refused = false,
                onBack = {}, onMore = {}, onAttach = {}, onRemove = {}, onSend = {}, onKey = {},
            ) {}
        }
    }

    @Test fun chatLoading() = paparazzi.snapshot {
        Frame {
            AgentContent(
                mode = "chat", chatAvailable = true, snippets = Snippets.DEFAULT,
                chatSlot = { ChatView(emptyList(), null, "tego", loading = true, onAnswer = {}, onOpenTerminal = {}) },
                terminal = "t12", agent = herd.agents[0], status = TermStatus(), ctrl = false, problem = null,
                message = "", onMessage = {}, attachments = emptyList(), sending = false, refused = false,
                onBack = {}, onMore = {}, onAttach = {}, onRemove = {}, onSend = {}, onKey = {},
            ) {}
        }
    }

    @Test fun chatCommands() = paparazzi.snapshot {
        Frame {
            ChatScreen(
                null, message = "/co",
                suggestions = listOf(
                    "/compact" to "Summarise history to free context: /compact [focus]",
                    "/skill:commit-message" to "Conventional Commits from the staged changes",
                    "/clone" to "Duplicate the current session into a new one",
                ),
            )
        }
    }

    @Test fun agentAttachments() = paparazzi.snapshot {
        val uri = android.net.Uri.parse("content://x/1")
        Frame {
            AgentView(
                TermStatus(state = "live", cols = 52, rows = 40, phoneCols = 52, phoneRows = 40, driving = true), ctrl = false,
                message = "what's wrong in this screenshot?",
                attachments = listOf(
                    Attachment(1, uri, "Screenshot_2026-10-31.png", 412_000, "image/png", path = "/x/Screenshot_2026-10-31.png", progress = 1f),
                    Attachment(2, uri, "bug-repro.mp4", 38_400_000, "video/mp4", progress = 0.42f),
                    Attachment(3, uri, "crash.log", 9_800, "text/plain", error = "Can't reach your Mac."),
                ),
            )
        }
    }

    @Test fun notificationsOff() = paparazzi.snapshot {
        Frame {
            HerdScreen(
                "AKBARs-MacBook-Air.local", herd, problem = null, onOpen = {}, onPairAgain = {},
                notifications = { NotificationsCard(on = false, allowed = true, unrestricted = false, onToggle = {}, onOpenSettings = {}, onUnrestrict = {}) },
            )
        }
    }
}
