package dev.akbxr.herdrremote

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.RectF
import androidx.compose.foundation.background
import androidx.compose.runtime.CompositionLocalProvider
import kotlinx.coroutines.awaitCancellation
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
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
        mode: String = "chat", changes: Changes? = null, modelChip: (@Composable () -> Unit)? = null,
    ) {
        AgentContent(
            mode = mode, chatAvailable = true, suggestions = suggestions, modelChip = modelChip,
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

    // Pictures in your messages: drawn here (no network in snapshots).
    private fun fakeShot(w: Int, h: Int, accent: Int): Bitmap {
        val b = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
        val c = Canvas(b)
        val p = Paint().apply { isAntiAlias = true }
        p.color = 0xFF1A1B26.toInt(); c.drawRect(0f, 0f, w.toFloat(), h.toFloat(), p)
        p.color = 0xFF24283B.toInt(); c.drawRect(0f, 0f, w.toFloat(), h * 0.09f, p)
        for (i in 0 until 9) {
            p.color = if (i % 3 == 0) accent else 0xFF565F89.toInt()
            val top = h * (0.16f + i * 0.085f)
            c.drawRoundRect(RectF(w * 0.07f, top, w * (0.35f + (i * 37 % 50) / 100f), top + h * 0.035f), 6f, 6f, p)
        }
        return b
    }

    private val fakeImages = object : ChatImages {
        val shots = mapOf(
            "shot" to fakeShot(640, 400, 0xFF7AA2F7.toInt()),
            "phone1" to fakeShot(288, 640, 0xFFE0AF68.toInt()),
            "phone2" to fakeShot(288, 640, 0xFF9ECE6A.toInt()),
        )
        override fun cached(id: String, px: Int) = shots[id]
        override suspend fun load(id: String, px: Int): Bitmap? = shots[id] ?: awaitCancellation()  // others: still loading
    }

    @Composable
    private fun ChatWith(content: @Composable () -> Unit) {
        AgentContent(
            mode = "chat", chatAvailable = true, snippets = Snippets.DEFAULT, onSearch = {},
            chatSlot = { CompositionLocalProvider(LocalChatImages provides fakeImages) { content() } },
            terminal = "t12", agent = herd.agents[0], status = TermStatus(), ctrl = false, problem = null,
            message = "", onMessage = {}, attachments = emptyList(), sending = false, refused = false,
            onBack = {}, onMore = {}, onAttach = {}, onRemove = {}, onSend = {}, onKey = {},
        ) {}
    }

    @Test fun chatImages() = paparazzi.snapshot {
        Frame {
            ChatWith {
                ChatView(
                    listOf(
                        ChatItem("user", "the login form loses the email after a failed attempt", images = listOf("shot"), id = "u1"),
                        ChatItem("assistant", "I see it: the email field is empty after the error banner.", id = "a1"),
                        ChatItem("user", "and this is how it looks on the phone", images = listOf("phone1", "phone2"), id = "u2"),
                        ChatItem("assistant", "Both screens reset the form. Fixing `resetForm()`.", id = "a2"),
                        ChatItem("user", images = listOf("loading"), id = "u3"),
                    ),
                    null, "tego", onAnswer = {}, onOpenTerminal = {},
                )
            }
        }
    }

    @Test fun chatSearch() = paparazzi.snapshot {
        val hits = findHits(chatItems, "login")
        Frame {
            ChatWith {
                Column(Modifier.fillMaxSize()) {
                    ChatSearchBar("login", {}, count = hits.size, current = 1, loading = false, onOlder = {}, onNewer = {}, onClose = {}, autofocus = false)
                    ChatView(chatItems, null, "tego", modifier = Modifier.weight(1f), search = ChatSearch("login", hits, 1), onAnswer = {}, onOpenTerminal = {})
                }
            }
        }
    }

    @Test fun chatLoadingOlder() = paparazzi.snapshot {
        Frame {
            ChatWith {
                ChatView(chatItems, null, "tego", hasOlder = true, loadingOlder = true, onAnswer = {}, onOpenTerminal = {})
            }
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

    private val home = "/Users/akbar"
    private val folders = TaskFolders(
        home,
        recent = listOf(
            TaskFolder("$home/Code/projects/herdr-remote", "herdr-remote"),
            TaskFolder(home, "~"),
            TaskFolder("$home/Code/projects/tego", "tego"),
            TaskFolder("$home/Code/projects/3d-video/gen3", "gen3"),
        ),
        projects = listOf(
            TaskFolder("$home/Code/projects/unli.dev", "unli.dev"),
            TaskFolder("$home/Code/projects/kudu", "kudu"),
            TaskFolder("$home/Code/projects/roblox/noturu-studio", "noturu-studio"),
            TaskFolder("$home/Code/projects/hono-copilot-api", "hono-copilot-api"),
            TaskFolder("$home/Code/projects/agy-proxy", "agy-proxy"),
            TaskFolder("$home/Code/projects/docs.unli.dev", "docs.unli.dev"),
        ),
    )
    private val taskAgents = listOf(TaskAgent("tego", "tego"), TaskAgent("claude", "Claude Code"), TaskAgent("codex", "Codex"), TaskAgent("omp", "omp"), TaskAgent("pi", "Pi"))

    @Composable
    private fun NewTask(
        picked: String? = null, filter: String = "", prompt: String = "", busy: Boolean = false, problem: String? = null,
        folders: TaskFolders? = this.folders, agents: List<TaskAgent>? = taskAgents,
    ) = NoShimmer {
        NewTaskForm(
            host = "AKBARs-MacBook-Air.local", folders = folders, agents = agents, loadProblem = null, onRetry = {},
            filter = filter, onFilter = {}, picked = picked, onPick = {}, agent = "claude", onAgent = {},
            prompt = prompt, onPrompt = {}, busy = busy, problem = problem, onBack = {}, onStart = {},
        )
    }

    @Test fun newTaskFolders() = paparazzi.snapshot { Frame { NewTask() } }

    @Test fun newTaskFoldersLoading() = paparazzi.snapshot { Frame { NewTask(folders = null, agents = null) } }

    @Test fun newTaskFilter() = paparazzi.snapshot { Frame { NewTask(filter = "unli") } }

    @Test fun newTaskPrompt() = paparazzi.snapshot {
        Frame { NewTask(picked = "$home/Code/projects/herdr-remote", prompt = "The login screen forgets the address after a restart. Find out why, fix it and run the tests.") }
    }

    @Test fun newTaskProblem() = paparazzi.snapshot {
        Frame {
            NewTask(
                picked = "$home/Code/projects/herdr-remote", prompt = "Run the tests", busy = false,
                problem = "herdr-app: no window (is the Herdr window open?)",
            )
        }
    }

    @Test fun newTaskStarting() = paparazzi.snapshot {
        Frame { NewTask(picked = "$home/Code/projects/herdr-remote", prompt = "Run the tests", busy = true) }
    }

    // The model and thinking level (Models.kt). The sheet's content is drawn
    // on its own: layoutlib can't draw a ModalBottomSheet's window.
    private val tegoChoices = ModelChoices(
        agent = "tego",
        models = listOf(
            ModelChoice("anthropic/claude-opus-5-5", "Opus 5.5", "anthropic"),
            ModelChoice("anthropic/claude-sonnet-5-5", "Sonnet 5.5", "anthropic"),
            ModelChoice("anthropic/claude-haiku-5-5", "Haiku 5.5", "anthropic"),
            ModelChoice("openai-codex/gpt-6-luna", "gpt-6-luna", "openai-codex"),
            ModelChoice("openai-codex/gpt-5.6-terra", "gpt-5.6-terra", "openai-codex"),
        ),
        thinking = listOf(
            LevelChoice("off", "Off"), LevelChoice("low", "Low"), LevelChoice("medium", "Medium"), LevelChoice("high", "High"),
            LevelChoice("xhigh", "Extra high", "Long agentic work; newest models only"), LevelChoice("max", "Max", "No limit on thinking; newest models only"),
        ),
        thinkingName = "Thinking", live = true, note = "tego keeps what you pick as its default for new sessions too.",
    )
    private val claudeChoices = ModelChoices(
        agent = "claude",
        models = listOf(
            ModelChoice("default", "Default"), ModelChoice("claude-opus-5-5", "Opus 5.5"), ModelChoice("claude-fable-5-1", "Fable 5.1"),
            ModelChoice("claude-sonnet-5-5", "Sonnet 5.5"), ModelChoice("claude-haiku-5-5", "Haiku 5.5"),
        ),
        thinking = listOf(
            LevelChoice("low", "Low", "Quick and straightforward"), LevelChoice("medium", "Medium"), LevelChoice("high", "High"),
            LevelChoice("xhigh", "Extra high"), LevelChoice("max", "Max", "May use many tokens; for the hardest tasks"),
        ),
        thinkingName = "Effort", live = true, note = "Claude Code keeps what you pick as its default for new sessions too.",
    )
    private val opusHigh = CurrentModel("anthropic/claude-opus-5-5", "Opus 5.5", "high")

    /** A sheet's content where the sheet would be: at the bottom, over a dimmed screen. */
    @Composable
    private fun SheetFrame(content: @Composable () -> Unit) {
        Box(Modifier.fillMaxSize().background(Ink.night)) {
            Box(Modifier.fillMaxSize().background(androidx.compose.ui.graphics.Color.Black.copy(alpha = 0.4f)))
            Column(
                Modifier.align(androidx.compose.ui.Alignment.BottomCenter).fillMaxWidth().fillMaxHeight(0.86f)
                    .clip(RoundedCornerShape(topStart = 28.dp, topEnd = 28.dp)).background(Ink.pen),
                horizontalAlignment = androidx.compose.ui.Alignment.CenterHorizontally,
            ) {
                Box(Modifier.padding(top = 10.dp, bottom = 6.dp).width(36.dp).height(4.dp).clip(RoundedCornerShape(2.dp)).background(Ink.rule))
                content()
            }
        }
    }

    @Test fun chatModelChip() = paparazzi.snapshot {
        Frame { ChatScreen(null, modelChip = { ModelChip(opusHigh, dim = false, switching = false) {} }) }
    }

    @Test fun chatModelChipWorking() = paparazzi.snapshot {
        Frame { ChatScreen(event("working", msg = "Running the tests"), modelChip = { ModelChip(opusHigh, dim = true, switching = false) {} }) }
    }

    @Test fun modelSheet() = paparazzi.snapshot {
        HerdrTheme {
            SheetFrame {
                LiveModelSheetContent(
                    "tego", tegoChoices, null, opusHigh, busy = null,
                    model = "anthropic/claude-sonnet-5-5", thinking = "low", onModel = {}, onThinking = {}, onApply = { _, _ -> }, onCancel = {},
                )
            }
        }
    }

    @Test fun modelSheetWorking() = paparazzi.snapshot {
        HerdrTheme {
            SheetFrame {
                LiveModelSheetContent(
                    "tego", tegoChoices, null, opusHigh, busy = "working",
                    model = opusHigh.model, thinking = opusHigh.thinking, onModel = {}, onThinking = {}, onApply = { _, _ -> }, onCancel = {},
                )
            }
        }
    }

    @Test fun modelSheetLoading() = paparazzi.snapshot {
        HerdrTheme {
            NoShimmer {
                SheetFrame {
                    LiveModelSheetContent(
                        "Claude Code", null, null, CurrentModel("default", "Opus 5.5", "high"), busy = null,
                        model = "default", thinking = "high", onModel = {}, onThinking = {}, onApply = { _, _ -> }, onCancel = {},
                    )
                }
            }
        }
    }

    @Test fun newTaskModels() = paparazzi.snapshot {
        Frame {
            NoShimmer {
                NewTaskForm(
                    host = "AKBARs-MacBook-Air.local", folders = folders, agents = taskAgents, loadProblem = null, onRetry = {},
                    filter = "", onFilter = {}, picked = "$home/Code/projects/herdr-remote", onPick = {}, agent = "claude", onAgent = {},
                    modelChoices = claudeChoices, model = "claude-sonnet-5-5", thinking = "high", onModels = {},
                    prompt = "Run the tests", onPrompt = {}, busy = false, problem = null, onBack = {}, onStart = {},
                )
            }
        }
    }

    @Test fun newTaskModelSheet() = paparazzi.snapshot {
        HerdrTheme {
            SheetFrame {
                ModelSheetContent(
                    title = "Claude Code's model", subtitle = "Kept for Claude Code's next tasks",
                    choices = claudeChoices, loadProblem = null, model = "claude-sonnet-5-5", thinking = null,
                    onModel = {}, onThinking = {}, allowDefault = true,
                ) { DoneFooter {} }
            }
        }
    }

    @Test fun herdEmpty() = paparazzi.snapshot {
        Frame { HerdScreen("AKBARs-MacBook-Air.local", Herd("main-full", emptyList(), emptyList()), problem = null, onOpen = {}, onPairAgain = {}) }
    }
}
