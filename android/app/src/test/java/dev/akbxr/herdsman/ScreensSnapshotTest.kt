package dev.akbxr.herdsman

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
        changesSlot: (@Composable () -> Unit)? = null,
    ) {
        AgentContent(
            mode = mode, chatAvailable = true, suggestions = suggestions, modelChip = modelChip,
            snippets = Snippets.DEFAULT,
            chatSlot = { ChatView(items, status, "tego", prompt = prompt, pending = pending, onAnswer = {}, onOpenTerminal = {}) },
            changesSlot = changesSlot ?: { ChangesView(changes, changes?.files.orEmpty()) },
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

    @Test fun changesDiffOpen() = paparazzi.snapshot {
        Frame {
            Column(Modifier.padding(14.dp)) {
                FileCard(changedFiles[0].copy(patch = changedFiles[0].patch + "\n+    const veryLongLineThatGoesPastTheEdgeOfThePhoneScreen = computeSomethingWide(a, b, c)\n\\ No newline at end of file"), null, startOpen = true)
            }
        }
    }

    private val changedFiles = listOf(
        ChangedFile("src/login.tsx", "M", 3, 1, false, "@@ -38,7 +38,9 @@ export function Login() {\n   const [email, setEmail] = useState(\"\")\n-  if (error) resetForm()\n+  if (error) {\n+    setPassword(\"\")\n+  }\n   return (", false),
        ChangedFile("src/login.test.tsx", "?", 24, 0, false, "@@ -0,0 +1,24 @@\n+import { render } from \"@testing-library/react\"", false),
        ChangedFile("src/old-form.ts", "D", 0, 31, false, "@@ -1,31 +0,0 @@\n-export function resetForm() {", false),
        ChangedFile("public/logo.png", "M", 0, 0, true, "", false),
    )
    private val sampleChanges = Changes(repo = true, root = "/p", branch = "main", more = 0, version = "v", files = changedFiles)

    @Test fun changesCommitBar() = paparazzi.snapshot {
        Frame {
            ChatScreen(event("done"), mode = "changes", changesSlot = { ChangesView(sampleChanges, changedFiles, onCommit = {}, onDiscard = {}) })
        }
    }

    @Test fun changesCommitted() = paparazzi.snapshot {
        Frame {
            ChatScreen(event("done"), mode = "changes", changesSlot = {
                ChangesView(
                    sampleChanges.copy(files = changedFiles.drop(3)), changedFiles.drop(3),
                    committed = CommitDone("a1b2c3d", "fix(login): keep the email after a failed attempt", 3),
                    onCommit = {}, onDiscard = {},
                )
            })
        }
    }

    @Test fun commitSheetHookError() = paparazzi.snapshot {
        HerdrTheme {
            SheetFrame {
                CommitSheetContent(
                    files = changedFiles, more = 0, branch = "main",
                    picked = changedFiles.take(3).map { it.path }.toSet(), onPick = { _, _ -> }, onPickAll = {},
                    message = "fix(login): keep the email after a failed attempt\n\nresetForm() ran on every error; now only the password clears.",
                    onMessage = {}, onSuggest = {}, suggesting = false,
                    ask = "tego", askBlocked = null, onAsk = {},
                    busy = false,
                    error = "husky - pre-commit script failed (code 1)\n> eslint --max-warnings 0 src\n\nsrc/login.tsx\n  41:7  error  'resetForm' is defined but never used  no-unused-vars\n\n✖ 1 problem (1 error, 0 warnings)",
                    onCommit = {}, onCancel = {},
                )
            }
        }
    }

    @Test fun commitSheetAgentWorking() = paparazzi.snapshot {
        HerdrTheme {
            SheetFrame {
                CommitSheetContent(
                    files = changedFiles, more = 2, branch = "main",
                    picked = changedFiles.map { it.path }.toSet(), onPick = { _, _ -> }, onPickAll = {},
                    message = "", onMessage = {}, onSuggest = {}, suggesting = false,
                    ask = "tego", askBlocked = "tego is working; ask it when it's done.", onAsk = {},
                    busy = false, error = null, onCommit = {}, onCancel = {},
                )
            }
        }
    }

    @Composable
    private fun DiscardOver(file: ChangedFile) {
        Frame {
            ChatScreen(event("done"), mode = "changes", changesSlot = { ChangesView(sampleChanges, changedFiles, onCommit = {}, onDiscard = {}) })
            Box(Modifier.fillMaxSize().background(androidx.compose.ui.graphics.Color.Black.copy(alpha = 0.5f)))
            Box(Modifier.fillMaxSize().padding(horizontal = 24.dp), contentAlignment = androidx.compose.ui.Alignment.Center) {
                DiscardConfirm(file, onConfirm = {}, onDismiss = {})
            }
        }
    }

    @Test fun discardConfirm() = paparazzi.snapshot { DiscardOver(changedFiles[0]) }

    @Test fun discardConfirmNew() = paparazzi.snapshot { DiscardOver(changedFiles[1]) }

    @Test fun chatMarkdown() = paparazzi.snapshot {
        Frame {
            ChatScreen(
                null,
                items = listOf(
                    ChatItem("user", "bandingkan fiturnya"),
                    ChatItem("assistant", "## Perbandingan\n\n| Fitur | herdsman | Ovis |\n|---|:---:|---|\n| Chat | ✓ | ✓ |\n| **Changes** | ✓ | ~~✗~~ diff saja |\n| Notifikasi | ✓ tombol | web push |\n\n- satu-satunya untuk `hc`\n  - tanpa SSH\n1. pertama\n2. kedua"),
                ),
            )
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
            TaskFolder("$home/Code/projects/herdsman", "herdsman"),
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
        sessions: List<EarlierSession>? = null, resuming: String? = null, resumeProblem: String? = null,
    ) = NoShimmer {
        NewTaskForm(
            host = "AKBARs-MacBook-Air.local", folders = folders, agents = agents, loadProblem = null, onRetry = {},
            filter = filter, onFilter = {}, picked = picked, onPick = {}, agent = "claude", onAgent = {},
            prompt = prompt, onPrompt = {}, busy = busy, problem = problem,
            sessions = sessions, resuming = resuming, resumeProblem = resumeProblem, now = now,
            onBack = {}, onStart = {},
        )
    }

    @Test fun newTaskFolders() = paparazzi.snapshot { Frame { NewTask() } }

    // Earlier sessions (Sessions.kt): times relative to a fixed now.
    private val now = 1_793_500_000_000L // 2026-11-01, late morning
    private fun ago(min: Long) = now - min * 60_000
    private val herdrRemote = "$home/Code/projects/herdsman"
    private val sessionsHere = listOf(
        EarlierSession("tego", "20261101-093000-a1b2c3", "Resume earlier sessions from the phone", herdrRemote, ago(4), 12, running = "t7"),
        EarlierSession("claude", "78759a30-f50b-498a-9f99-8c319953813a", "Login screen forgets the address after a restart", herdrRemote, ago(150), 6),
        EarlierSession("tego", "20261031-201500-0a0b0c", "Answer permission prompts from the notification", herdrRemote, ago(60 * 20), 31),
        EarlierSession("tego", "20261029-101500-0d0e0f", "Paparazzi snapshots for the chat screen", herdrRemote, ago(60 * 24 * 3), 140, atLeast = true),
        EarlierSession("claude", "f0d71145-8209-4082-96b6-c9647e912c20", "Tailscale address in the pairing link", herdrRemote, ago(60 * 24 * 12), 2),
    )
    private val sessionsAnywhere = EarlierSessions(
        home,
        listOf(
            EarlierSession("tego", "20261030-060455-b94bfe", "Riset herdr-core dan status agen", home, ago(1), 60, running = "t12"),
            sessionsHere[1],
            EarlierSession("tego", "20261031-142036-ea0071", "Skill pembuat video otomatis Blender Remotion", "$home/Code/projects/3d-video/gen3", ago(60 * 19), 11),
            EarlierSession("tego", "20261013-140115-07dbc3", "Mencari papers implementasi untuk Tego", "$home/Code/projects/tego", ago(60 * 24 + 90), 267),
            EarlierSession("claude", "619ad0d4-7f61-4d19-92b0-6f1d0e5c6a11", "Project tego dan kudu", "$home/Code/projects/kudu", ago(60 * 24 * 4), 1),
            EarlierSession("tego", "20261020-003859-0e480c", "Ide T3 Code dengan GPUI", "$home/Code/projects/kudu", ago(60 * 24 * 12), 14),
            EarlierSession(
                "tego", "20261012-181901-a56268", "Redesign dashboard billing keys modern interface, with a much longer title that wraps",
                "$home/Code/projects/unli.dev", ago(60 * 24 * 20), 136,
            ),
        ),
    )

    @Test fun newTaskSessions() = paparazzi.snapshot {
        Frame { NewTask(picked = herdrRemote, sessions = sessionsHere) }
    }

    @Test fun newTaskSessionResuming() = paparazzi.snapshot {
        Frame { NewTask(picked = herdrRemote, sessions = sessionsHere.take(3), resuming = sessionsHere[1].id) }
    }

    @Test fun newTaskSessionProblem() = paparazzi.snapshot {
        Frame {
            NewTask(
                picked = herdrRemote, sessions = sessionsHere.drop(1).take(2),
                resumeProblem = "the session's folder /Users/akbar/Code/old is gone from the Mac",
            )
        }
    }

    @Composable
    private fun Sessions(sessions: EarlierSessions?, resuming: String? = null, problem: String? = null, loadProblem: String? = null) = NoShimmer {
        SessionsList(
            host = "AKBARs-MacBook-Air.local", sessions = sessions, now = now, loadProblem = loadProblem, onRetry = {},
            resuming = resuming, problem = problem, onBack = {}, onSession = {},
        )
    }

    @Test fun sessions() = paparazzi.snapshot { Frame { Sessions(sessionsAnywhere) } }

    @Test fun sessionsLoading() = paparazzi.snapshot { Frame { Sessions(null) } }

    @Test fun sessionsInHerdrCli() = paparazzi.snapshot {
        Frame {
            NoShimmer {
                SessionsList(
                    host = "AKBARs-MacBook-Air.local", sessions = sessionsAnywhere, now = now, loadProblem = null, onRetry = {},
                    resuming = null, problem = null, onBack = {}, onSession = {},
                    backends = true, cli = true, workspaces = workspaces, workspace = "wF",
                )
            }
        }
    }

    @Test fun sessionsEmpty() = paparazzi.snapshot { Frame { Sessions(EarlierSessions(home, emptyList())) } }

    @Test fun sessionsProblem() = paparazzi.snapshot {
        Frame { Sessions(sessionsAnywhere, problem = "the session's folder /Users/akbar/Code/old is gone from the Mac") }
    }

    @Test fun newTaskFoldersLoading() = paparazzi.snapshot { Frame { NewTask(folders = null, agents = null) } }

    @Test fun newTaskFilter() = paparazzi.snapshot { Frame { NewTask(filter = "unli") } }

    @Test fun newTaskPrompt() = paparazzi.snapshot {
        Frame { NewTask(picked = "$home/Code/projects/herdsman", prompt = "The login screen forgets the address after a restart. Find out why, fix it and run the tests.") }
    }

    @Test fun newTaskProblem() = paparazzi.snapshot {
        Frame {
            NewTask(
                picked = "$home/Code/projects/herdsman", prompt = "Run the tests", busy = false,
                problem = "herdr-app: no window (is the Herdr window open?)",
            )
        }
    }

    @Test fun newTaskStarting() = paparazzi.snapshot {
        Frame { NewTask(picked = "$home/Code/projects/herdsman", prompt = "Run the tests", busy = true) }
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
                    filter = "", onFilter = {}, picked = "$home/Code/projects/herdsman", onPick = {}, agent = "claude", onAgent = {},
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

    // Machines (Machines.kt): terminals on this Mac and on vps, gpu-box unreachable.
    private val vps = Machine("vps", "vps", "akbar@vps.unli.dev", "connected")
    private val gpu = Machine("gpu", "gpu", "gpu-box", "unreachable", "ssh: connect to host gpu-box port 22: Operation timed out")
    private val herdMachines = Herd(
        layout = "main-full",
        agents = herd.agents.take(2) + listOf(
            Agent("vps/t3", "claude-code", "deploy", State.Working, "tv1", "1", false, "api", machine = "vps"),
            Agent("vps/t5", "tego", null, State.Done, "tv2", "2", false, "api", machine = "vps"),
            Agent("vps/t8", "shell", null, State.Idle, "tv3", "3", false, "api", machine = "vps"),
        ),
        tabs = herd.tabs,
        machines = listOf(vps, gpu),
    )

    @Test fun herdMachines() = paparazzi.snapshot {
        Frame {
            NoShimmer {
                Column(Modifier.fillMaxSize()) {
                    Box(Modifier.weight(1f)) {
                        HerdScreen("AKBARs-MacBook-Air.local", herdMachines, problem = null, onOpen = {}, onPairAgain = {}, notifyOn = true)
                    }
                    BottomBar(listOf("Herd" to true, "Screen" to false), badge = "1", onSelect = {})
                }
            }
        }
    }

    @Test fun machinesSheet() = paparazzi.snapshot {
        HerdrTheme {
            NoShimmer {
                SheetFrame {
                    MachinesSheetContent(
                        machines = listOf(
                            Machine("", "This Mac", "AKBARs-MacBook-Air", "connected", local = true),
                            vps,
                            gpu,
                            Machine("box", "box", "root@10.0.0.7", "needs_login", "ssh asks for a password"),
                            Machine("pi", "pi", "pi.local", "connecting"),
                        ),
                        loadProblem = null, busy = emptySet(),
                        hints = mapOf("box" to "box needs its password typed on the Mac: open the Herdr window and enter it there."),
                        host = "akbar@build", onHost = {}, name = "", onName = {},
                        adding = false, addProblem = "ssh: Could not resolve hostname build: nodename nor servname provided",
                        onAdd = {}, onReconnect = {}, onRemove = {},
                    )
                }
            }
        }
    }

    @Test fun newTaskMachines() = paparazzi.snapshot {
        Frame {
            NoShimmer {
                NewTaskForm(
                    host = "AKBARs-MacBook-Air.local",
                    machines = listOf(vps, gpu), machine = "vps",
                    folders = TaskFolders(
                        "/home/akbar",
                        recent = listOf(TaskFolder("/home/akbar/api", "api"), TaskFolder("/home/akbar", "~")),
                        projects = listOf(TaskFolder("/home/akbar/src/unli.dev", "unli.dev"), TaskFolder("/home/akbar/src/billing", "billing")),
                    ),
                    agents = taskAgents.take(2), loadProblem = null, onRetry = {},
                    filter = "", onFilter = {}, picked = null, onPick = {}, agent = "claude", onAgent = {},
                    prompt = "", onPrompt = {}, busy = false, problem = null, onBack = {}, onStart = {},
                )
            }
        }
    }

    @Test fun newTaskMachineSessionsNote() = paparazzi.snapshot {
        Frame {
            NoShimmer {
                NewTaskForm(
                    host = "AKBARs-MacBook-Air.local",
                    machines = listOf(vps, gpu), machine = "vps",
                    folders = TaskFolders("/home/akbar", emptyList(), emptyList()),
                    agents = taskAgents.take(2), loadProblem = null, onRetry = {},
                    filter = "", onFilter = {}, picked = "/home/akbar/api", onPick = {}, agent = "claude", onAgent = {},
                    modelChoices = claudeChoices.copy(supported = false),
                    prompt = "Deploy the api", onPrompt = {}, busy = false, problem = null,
                    sessions = emptyList(), sessionsNote = "Earlier sessions on vps can't be listed yet.",
                    onBack = {}, onStart = {},
                )
            }
        }
    }

    // The herdr CLI (herdr:<pane>) beside the Herdr app, or alone.
    private val cliAgents = listOf(
        Agent("herdr:wF:p2", "claude", "Fix the login form", State.Working, "wF:t2", "2", true, "api", pane = "wF:p2", spaceId = "wF", source = "herdr"),
        Agent("herdr:wF:p3", "shell", null, State.Idle, "wF:t2", "2", true, "api", pane = "wF:p3", spaceId = "wF", source = "herdr"),
        Agent("herdr:wA:p1", "codex", null, State.NeedsYou, "wA:t1", "build", false, "home", pane = "wA:p1", spaceId = "wA", source = "herdr"),
    )
    private val workspaces = listOf(Workspace("wF", "api", true), Workspace("wA", "home"), Workspace("wB", "blog"))
    private val herdBoth = herd.copy(agents = herd.agents.take(3) + cliAgents, cli = true, workspaces = workspaces)

    @Test fun herdBothBackends() = paparazzi.snapshot {
        Frame {
            Column(Modifier.fillMaxSize()) {
                Box(Modifier.weight(1f)) {
                    HerdScreen("AKBARs-MacBook-Air.local", herdBoth, problem = null, onOpen = {}, onPairAgain = {}, notifyOn = true)
                }
                BottomBar(listOf("Herd" to true, "Screen" to false), badge = "2", onSelect = {})
            }
        }
    }

    @Test fun herdCliOnly() = paparazzi.snapshot {
        Frame {
            HerdScreen(
                "AKBARs-MacBook-Air.local", Herd("", cliAgents, emptyList(), app = false, cli = true, workspaces = workspaces.take(2)),
                problem = null, onOpen = {}, onPairAgain = {},
            )
        }
    }

    @Test fun newTaskBackends() = paparazzi.snapshot {
        Frame {
            NoShimmer {
                NewTaskForm(
                    host = "AKBARs-MacBook-Air.local",
                    backends = true, cli = true, workspaces = workspaces, workspace = "wF",
                    folders = folders, agents = taskAgents, loadProblem = null, onRetry = {},
                    filter = "", onFilter = {}, picked = "$home/Code/projects/herdsman", onPick = {}, agent = "claude", onAgent = {},
                    prompt = "Run the tests", onPrompt = {}, busy = false, problem = null, onBack = {}, onStart = {},
                )
            }
        }
    }

    @Test fun paneActionsCli() = paparazzi.snapshot {
        val agent = cliAgents[0]
        HerdrTheme {
            SheetFrame { ActionSheetContent(agent.title, paneWhere(agent), paneMenu(agent) {}) }
        }
    }

    @Test fun agentCliTerminal() = paparazzi.snapshot {
        Frame {
            AgentContent(
                terminal = "herdr:wF:p2", agent = cliAgents[0], status = TermStatus(state = "live", cols = 120, rows = 34, phoneCols = 52, phoneRows = 40),
                ctrl = false, problem = null, message = "", onMessage = {}, attachments = emptyList(), sending = false, refused = false,
                onBack = {}, onMore = {}, onAttach = {}, onRemove = {}, onSend = {}, onKey = {},
            ) { FakeTerminal() }
        }
    }

    @Test fun screenNoWindow() = paparazzi.snapshot {
        Frame {
            Column(Modifier.fillMaxSize()) {
                Box(Modifier.weight(1f)) { NoWindow() }
                BottomBar(listOf("Herd" to false, "Screen" to true), badge = null, onSelect = {})
            }
        }
    }

    @Test fun chatRemote() = paparazzi.snapshot {
        Frame {
            AgentContent(
                mode = "chat", chatAvailable = true, snippets = Snippets.DEFAULT,
                chatSlot = { ChatView(chatItems.take(4), null, "deploy", onAnswer = {}, onOpenTerminal = {}) },
                terminal = "vps/t3", agent = herdMachines.agents[2], status = TermStatus(), ctrl = false, problem = null,
                message = "", onMessage = {}, attachments = emptyList(), sending = false, refused = false,
                onBack = {}, onMore = {}, onAttach = {}, onRemove = {}, onSend = {}, onKey = {},
            ) {}
        }
    }
}
