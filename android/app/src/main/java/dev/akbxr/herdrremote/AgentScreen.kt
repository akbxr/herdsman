package dev.akbxr.herdrremote

import android.content.ClipboardManager
import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import android.os.Build
import android.provider.OpenableColumns
import android.util.Size
import android.view.ViewGroup
import android.widget.FrameLayout
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.isImeVisible
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import kotlinx.coroutines.delay
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

internal val TermBg = Color(0xFF24283B)  // the terminal's own background (Tokyo Night Storm)

/** The key bar: what a phone keyboard lacks, and enough to answer a menu without one. */
private val KEYS = listOf(
    "Esc" to "esc", "Tab" to "tab", "Ctrl" to "ctrl",
    "↑" to "up", "↓" to "down", "←" to "left", "→" to "right", "⏎" to "enter",
    "Paste" to "paste", "⌨" to "keyboard",
)

/** A file on its way to the Mac, then sent along with the message. */
data class Attachment(
    val id: Long,
    val uri: Uri,
    val name: String,
    val size: Long,
    val mime: String,
    val thumb: Bitmap? = null,
    val progress: Float = 0f,
    val path: String? = null,     // where it landed beside the terminal
    val error: String? = null,
) {
    val ready get() = path != null
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun AgentScreen(api: Api, terminal: String, agent: Agent?, appScope: CoroutineScope, onBack: () -> Unit) {
    val context = LocalContext.current
    var status by remember { mutableStateOf(TermStatus()) }
    var ctrl by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    // The message box's text and cursor, in one place: typing fast can't race
    // with text put in from outside (a suggestion, a quick reply).
    var field by remember { mutableStateOf(TextFieldValue("")) }
    val message = field.text
    fun setMessage(text: String) { field = TextFieldValue(text, TextRange(text.length)) }
    var sending by remember { mutableStateOf(false) }
    var refused by remember { mutableStateOf(false) }
    var more by remember { mutableStateOf(false) }
    var attachMenu by remember { mutableStateOf(false) }
    val attachments = remember { mutableStateListOf<Attachment>() }
    val scope = rememberCoroutineScope()
    val controller = remember(terminal) { TerminalController(context) { status = it } }
    controller.onCtrlUsed = { ctrl = false }

    val onScreen = rememberOnScreen()
    val link by rememberLink()

    // The chat: the agent's conversation, read from its transcript (Chat.kt).
    // It shows first for an agent; a shell (no transcript) shows its terminal.
    var chat by remember(terminal) { mutableStateOf<ChatLog?>(null) }
    var chatItems by remember(terminal) { mutableStateOf<List<ChatItem>>(emptyList()) }
    var chatTried by remember(terminal) { mutableStateOf(false) }
    var chatProblem by remember(terminal) { mutableStateOf<String?>(null) }
    var userMode by rememberSaveable(terminal) { mutableStateOf<String?>(null) }
    // An agent runs in it (even one that has said nothing yet): it has a chat.
    val chatAvailable = chat?.agent?.isNotEmpty() == true
    val mode = userMode ?: when {
        chatAvailable -> "chat"
        chatTried -> "terminal"
        else -> "loading"
    }
    // Another program in the terminal (claude typed in a shell, or the agent
    // quit back to it): the view follows, Chat for an agent, Terminal for a shell.
    var lastAgent by remember(terminal) { mutableStateOf<String?>(null) }
    LaunchedEffect(chat?.agent) {
        val a = chat?.agent ?: return@LaunchedEffect
        if (lastAgent != null && lastAgent != a) userMode = null
        lastAgent = a
    }
    LaunchedEffect(terminal, onScreen, mode) {
        if (!onScreen) return@LaunchedEffect
        while (true) {
            runCatching { api.chat(terminal, chat?.version.orEmpty()) }
                .onSuccess { c ->
                    // Another session (a new one, /clear, /resume): its own items, none of the last one's.
                    if (c.source != chat?.source) chatItems = emptyList()
                    c.items?.let { chatItems = it }
                    chat = c
                    chatProblem = null
                }
                .onFailure { if (mode == "chat") chatProblem = it.advice(link) }
            chatTried = true
            // On the chat, closer while it works: each tool call shows as it
            // starts. Elsewhere, slowly: only to notice an agent starting.
            delay(
                when {
                    mode != "chat" -> 3000
                    chat?.status?.state == "working" -> 900
                    else -> 2000
                },
            )
        }
    }

    // Messages sent from the chat show at once, until the transcript has them.
    val pending = remember(terminal) { mutableStateListOf<Pair<String, Long>>() }
    LaunchedEffect(chatItems, pending.size) {
        val recent = chatItems.takeLast(12).filter { it.kind == "user" }.map { it.text }
        val now = System.currentTimeMillis()
        pending.removeAll { (text, at) -> now - at > 120_000 || recent.any { r -> r.contains(text.take(60).trim()) } }
    }

    // The menu a waiting agent shows: its real choices (Chat.kt, Answer.kt).
    var prompt by remember(terminal) { mutableStateOf<Prompt?>(null) }
    val blockedSeq = chat?.status?.takeIf { it.state == "blocked" }?.seq
    LaunchedEffect(terminal, blockedSeq, mode == "chat") {
        prompt = null
        if (blockedSeq == null || mode != "chat") return@LaunchedEffect
        delay(400)  // the menu is drawn just after the status says so
        while (true) {
            runCatching { api.prompt(terminal) }.onSuccess { prompt = it }
            delay(2500)
        }
    }

    // Changes: the git diff of the agent's folder (Changes.kt), while that tab shows.
    var changes by remember(terminal) { mutableStateOf<Changes?>(null) }
    var changedFiles by remember(terminal) { mutableStateOf<List<ChangedFile>>(emptyList()) }
    LaunchedEffect(terminal, onScreen, mode == "changes") {
        if (!onScreen || mode != "changes") return@LaunchedEffect
        while (true) {
            runCatching { api.changes(terminal, changes?.version.orEmpty()) }
                .onSuccess { c -> c.files?.let { changedFiles = it }; changes = c; chatProblem = null }
                .onFailure { chatProblem = it.advice(link) }
            delay(4000)
        }
    }

    // The terminal: attached only while it shows. In a pocket, or on the
    // chat, nothing streams.
    LaunchedEffect(controller, onScreen, mode) {
        if (onScreen && mode == "terminal") controller.connect(api, terminal)
        else controller.web.evaluateJavascript("window.hr && hr.close()", null)
    }
    DisposableEffect(controller) { onDispose { controller.close() } }
    // Wi-Fi or Tailscale back: reconnect now, not at the next try.
    LaunchedEffect(controller, link) { if (link == Link.Up) controller.retryNow() }
    // The keyboard covering the screen (for the terminal's own keys or the
    // message box): the terminal keeps its rows and scrolls instead.
    val keyboardUp = WindowInsets.isImeVisible
    LaunchedEffect(controller, keyboardUp) { controller.keyboard(keyboardUp) }

    // "/" and "@" in the message box (Composer.kt).
    var commands by remember(terminal) { mutableStateOf<List<Command>?>(null) }
    var files by remember(terminal) { mutableStateOf<List<String>>(emptyList()) }
    val want = wanted(message)
    LaunchedEffect(terminal, want is Wanted.Commands) {
        if (want is Wanted.Commands && commands == null) commands = runCatching { api.commands(terminal) }.getOrNull()
    }
    val fileQuery = (want as? Wanted.Files)?.query
    LaunchedEffect(terminal, fileQuery) {
        if (fileQuery == null) { files = emptyList(); return@LaunchedEffect }
        delay(150)  // typing on: ask once
        files = runCatching { api.files(terminal, fileQuery) }.getOrDefault(emptyList())
    }
    val commandHits = (want as? Wanted.Commands)?.let { matchCommands(commands.orEmpty(), it.query) }.orEmpty()
    val suggestions = when (want) {
        is Wanted.Commands -> commandHits.map { it.name to it.help }
        is Wanted.Files -> files.map { it.substringAfterLast('/') to it }
        null -> emptyList()
    }

    // Quick replies.
    val snippetStore = remember { Snippets(context) }
    var snippets by remember { mutableStateOf(snippetStore.load()) }
    var editing by remember { mutableStateOf<Int?>(null) }  // -1: a new one
    editing?.let { i ->
        fun store(list: List<String>) { snippets = list; snippetStore.save(list); editing = null }
        SnippetDialog(
            initial = snippets.getOrNull(i).orEmpty(),
            onSave = { text -> store(if (i < 0) snippets + text else snippets.toMutableList().also { it[i] = text }) },
            onDelete = if (i < 0) null else ({ store(snippets.filterIndexed { j, _ -> j != i }) }),
            onDismiss = { editing = null },
        )
    }

    // Picked files go up right away, so Send only has paths to pass on.
    fun add(uris: List<Uri>) {
        uris.forEach { uri ->
            val a = describe(context, uri)
            attachments += a
            scope.launch {
                val thumb = withContext(Dispatchers.IO) { thumbnail(context, a) }
                update(attachments, a.id) { it.copy(thumb = thumb) }
                runCatching {
                    api.upload(context, a.uri, a.name, a.size, terminal) { p -> scope.launch { update(attachments, a.id) { it.copy(progress = p) } } }
                }.onSuccess { path -> update(attachments, a.id) { it.copy(path = path, progress = 1f) } }
                    .onFailure { e -> update(attachments, a.id) { it.copy(error = e.advice()) } }
            }
        }
    }
    val pickMedia = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(10)) { add(it) }
    val pickFiles = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { add(it) }

    fun send(force: Boolean) {
        val paths = attachments.mapNotNull { it.path }
        val text = listOf(message.trim(), paths.joinToString(" ")).filter { it.isNotEmpty() }.joinToString(" ")
        if (text.isEmpty() || sending) return
        sending = true
        problem = null
        scope.launch {
            runCatching { api.send(terminal, text, force) }
                .onSuccess {
                    setMessage(""); attachments.clear(); refused = false
                    if (mode == "chat") pending += text to System.currentTimeMillis()
                }
                .onFailure {
                    // hc send won't type into a program that is asking something.
                    if (!force && agent?.state == State.NeedsYou) {
                        refused = true
                        problem = "${agent.title} is waiting on a question. Answer it in the terminal, or send anyway to type into it."
                    } else problem = it.advice()
                }
            sending = false
        }
    }

    if (more && agent != null) {
        PaneActions(api, agent, appScope, onDismiss = { more = false }, onProblem = { problem = it }, onClosed = onBack)
    }
    if (attachMenu) {
        ActionSheet(
            title = "Attach", subtitle = "It goes to the Mac beside ${agent?.title ?: terminal}, and its path goes with your message.",
            onDismiss = { attachMenu = false },
            actions = listOf(
                SheetAction("Photos and videos") {
                    attachMenu = false
                    pickMedia.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo))
                },
                SheetAction("Files", "Documents, archives, anything") { attachMenu = false; pickFiles.launch(arrayOf("*/*")) },
            ),
        )
    }

    AgentContent(
        mode = mode,
        chatAvailable = chatAvailable,
        onMode = { userMode = it },
        suggestions = suggestions,
        onPickSuggestion = { i ->
            when (want) {
                is Wanted.Commands -> commandHits.getOrNull(i)?.let { setMessage(it.name + " ") }
                is Wanted.Files -> files.getOrNull(i)?.let { setMessage(withFile(message, it)) }
                null -> {}
            }
        },
        snippets = snippets,
        onSnippet = { text -> setMessage(if (message.isBlank()) text else message.trimEnd() + " " + text); refused = false },
        onEditSnippet = { editing = it },
        onAddSnippet = { editing = -1 },
        onEsc = { scope.launch { runCatching { api.keys(terminal, "esc") }.onFailure { problem = it.advice() } } },
        changesSlot = { ChangesView(changes, changedFiles) },
        chatSlot = {
            ChatView(
                items = chatItems,
                status = chat?.status,
                agentName = agent?.title ?: chat?.agent?.ifBlank { null } ?: terminal,
                prompt = prompt,
                pending = pending.map { it.first },
                loading = chat == null,
                onOption = { option ->
                    val st = chat?.status
                    prompt = null
                    scope.launch {
                        runCatching { api.answer(terminal, st?.seq ?: 0, "option", option = option) }
                            .onSuccess { chat = chat?.copy(status = null) }
                            .onFailure { problem = it.advice() }
                    }
                },
                onAnswer = { answer ->
                    val st = chat?.status
                    scope.launch {
                        runCatching { api.answer(terminal, st?.seq ?: 0, answer) }
                            .onSuccess { chat = chat?.copy(status = null) }
                            .onFailure { problem = it.advice() }
                    }
                },
                onOpenTerminal = { userMode = "terminal" },
            )
        },
        link = link,
        onOpenTailscale = { Net.openTailscale(context); Unit }.takeIf { Net.hasTailscale(context) },
        terminal = terminal, agent = agent, status = status, ctrl = ctrl, problem = problem ?: chatProblem,
        message = message, onMessage = { setMessage(it); refused = false },
        field = field, onField = { if (it.text != field.text) refused = false; field = it },
        attachments = attachments, sending = sending, refused = refused,
        onBack = onBack,
        onMore = { more = true },
        onAttach = { attachMenu = true },
        onRemove = { id -> attachments.removeAll { it.id == id } },
        onSend = ::send,
        onKey = { key ->
            when (key) {
                "ctrl" -> { ctrl = !ctrl; controller.ctrl = ctrl; controller.showKeyboard() }
                "keyboard" -> controller.showKeyboard()
                "paste" -> {
                    val clip = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                    clip.primaryClip?.getItemAt(0)?.coerceToText(context)?.toString()
                        ?.takeIf { it.isNotEmpty() }?.let(controller::paste)
                }
                else -> controller.key(key)
            }
        },
    ) {
        // Keyed: another terminal (opened from a notification) brings its own WebView.
        key(controller) {
            AndroidView(
                factory = {
                    FrameLayout(it).apply {
                        (controller.web.parent as? ViewGroup)?.removeView(controller.web)
                        (controller.input.parent as? ViewGroup)?.removeView(controller.input)
                        addView(controller.web, FrameLayout.LayoutParams(-1, -1))
                        addView(controller.input, FrameLayout.LayoutParams(1, 1))
                    }
                },
                modifier = Modifier.fillMaxSize(),
            )
        }
    }
}

private fun update(list: MutableList<Attachment>, id: Long, change: (Attachment) -> Attachment) {
    val i = list.indexOfFirst { it.id == id }
    if (i >= 0) list[i] = change(list[i])
}

/** Name, size and type of a picked file. */
private fun describe(context: Context, uri: Uri): Attachment {
    var name = uri.lastPathSegment?.substringAfterLast('/') ?: "file"
    var size = -1L
    context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
        if (c.moveToFirst()) {
            c.getString(0)?.let { name = it }
            if (!c.isNull(1)) size = c.getLong(1)
        }
    }
    val mime = context.contentResolver.getType(uri) ?: "application/octet-stream"
    return Attachment(System.nanoTime(), uri, name, size, mime)
}

private fun thumbnail(context: Context, a: Attachment): Bitmap? = runCatching {
    if (!a.mime.startsWith("image/") && !a.mime.startsWith("video/")) return null
    if (Build.VERSION.SDK_INT >= 29) return context.contentResolver.loadThumbnail(a.uri, Size(160, 160), null)
    if (!a.mime.startsWith("image/")) return null
    val opts = BitmapFactory.Options().apply { inSampleSize = 8 }
    context.contentResolver.openInputStream(a.uri)?.use { BitmapFactory.decodeStream(it, null, opts) }
}.getOrNull()

private fun humanSize(bytes: Long): String = when {
    bytes < 0 -> ""
    bytes < 1024 -> "$bytes B"
    bytes < 1024 * 1024 -> "${bytes / 1024} KB"
    bytes < 1024L * 1024 * 1024 -> "%.1f MB".format(bytes / 1048576.0)
    else -> "%.1f GB".format(bytes / 1073741824.0)
}

/** The agent screen around its terminal (the slot), without the network. */
@Composable
fun AgentContent(
    mode: String = "terminal",          // chat, terminal, loading (not known yet)
    chatAvailable: Boolean = false,
    onMode: (String) -> Unit = {},
    suggestions: List<Pair<String, String>> = emptyList(),
    onPickSuggestion: (Int) -> Unit = {},
    snippets: List<String> = emptyList(),
    onSnippet: (String) -> Unit = {},
    onEditSnippet: (Int) -> Unit = {},
    onAddSnippet: () -> Unit = {},
    onEsc: () -> Unit = {},
    chatSlot: @Composable () -> Unit = {},
    changesSlot: @Composable () -> Unit = {},
    field: TextFieldValue? = null,                  // the box's text and cursor; null: message, cursor at its end
    onField: ((TextFieldValue) -> Unit)? = null,
    link: Link = Link.Up,
    onOpenTailscale: (() -> Unit)? = null,
    terminal: String,
    agent: Agent?,
    status: TermStatus,
    ctrl: Boolean,
    problem: String?,
    message: String,
    onMessage: (String) -> Unit,
    attachments: List<Attachment>,
    sending: Boolean,
    refused: Boolean,
    onBack: () -> Unit,
    onMore: () -> Unit,
    onAttach: () -> Unit,
    onRemove: (Long) -> Unit,
    onSend: (force: Boolean) -> Unit,
    onKey: (String) -> Unit,
    terminalSlot: @Composable () -> Unit,
) {
    val name = agent?.title ?: terminal
    Column(Modifier.fillMaxSize()) {
        // Heading: back, the tag, its name and state, and everything else under ⋯.
        Row(
            Modifier.fillMaxWidth().padding(start = 4.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            IconKey(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back to the herd", tint = Ink.text) }
            if (agent != null) {
                EarTag(agent, width = 28.dp)
                Spacer(Modifier.width(12.dp))
            }
            Column(Modifier.weight(1f)) {
                Text(name, style = Type.title, color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    listOfNotNull(agent?.state?.word, agent?.let { "Tab ${it.tabName}" }, terminal).joinToString("  ·  "),
                    style = Type.small, color = agent?.state?.takeIf { it != State.Idle }?.tagColor() ?: Ink.dust, maxLines = 1,
                )
            }
            if (agent != null) {
                IconKey(onClick = onMore) { Icon(Icons.Filled.MoreVert, contentDescription = "Split, zoom, rename, close", tint = Ink.text) }
            }
        }

        ViewTabs(mode, chatAvailable, onMode)

        if (mode == "chat") {
            Box(Modifier.weight(1f).fillMaxWidth()) { chatSlot() }
        } else if (mode == "changes") {
            Box(Modifier.weight(1f).fillMaxWidth()) { changesSlot() }
        } else if (mode == "loading") {
            Box(Modifier.weight(1f).fillMaxWidth())
        } else
        // The terminal itself.
        Box(
            Modifier
                .weight(1f)
                .fillMaxWidth()
                .padding(horizontal = 8.dp)
                .clip(RoundedCornerShape(12.dp))
                .background(TermBg)
                .border(1.dp, Ink.rule, RoundedCornerShape(12.dp)),
        ) {
            terminalSlot()
            val lost = status.state == "reconnecting"
            val overlay = when {
                status.state == "ended" -> status.reason?.replaceFirstChar { it.uppercase() } ?: "The program has ended."
                status.state != "live" && link == Link.Offline -> "This phone is offline. It reconnects when it's back."
                status.state != "live" && link == Link.NoVpn -> "Tailscale is off on this phone. It reconnects once it's on."
                lost && status.attempt >= 3 -> "Your Mac isn't answering. It may be asleep. Still trying…"
                lost -> "Connection lost. Reconnecting…"
                status.state == "ready" || status.state == "connecting" -> "Connecting to $terminal…"
                else -> null
            }
            overlay?.let {
                Row(
                    Modifier
                        .align(Alignment.TopCenter)
                        .padding(top = 10.dp, start = 12.dp, end = 12.dp)
                        .clip(RoundedCornerShape(10.dp))
                        .background(Ink.pen)
                        .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
                        .padding(horizontal = 12.dp, vertical = 7.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(it, style = Type.small, color = Ink.text, modifier = Modifier.weight(1f, fill = false))
                    if (link == Link.NoVpn && status.state != "live" && onOpenTailscale != null) {
                        Spacer(Modifier.width(10.dp))
                        Text(
                            "Open Tailscale", style = Type.label, color = Ink.text,
                            modifier = Modifier.clip(RoundedCornerShape(6.dp)).clickable(onClick = onOpenTailscale).padding(4.dp),
                        )
                    }
                }
            }
        }

        // Whose size the terminal has: typing in it takes it over.
        val sizeLine = when {
            status.cols == 0 -> " "
            status.driving -> "Sized for this phone · ${status.cols}×${status.rows}"
            else -> "The Mac's size, ${status.cols}×${status.rows} · type in it to fit it to this phone"
        }
        if (mode == "terminal") Text(sizeLine, style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(start = 14.dp, end = 14.dp, top = 6.dp))

        problem?.let { Notice(it, Modifier.padding(horizontal = 8.dp, vertical = 6.dp)) }

        // Attachments waiting to go with the message.
        if (attachments.isNotEmpty()) {
            Row(
                Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(start = 8.dp, end = 8.dp, top = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) { attachments.forEach { AttachmentChip(it, onRemove = { onRemove(it.id) }) } }
        }

        // "/" commands and "@" files for the word being typed.
        if (suggestions.isNotEmpty()) {
            Suggestions(suggestions, onPickSuggestion, Modifier.padding(start = 8.dp, end = 8.dp, top = 8.dp))
        }

        // The message box: prose, with suggestions, sent as one message.
        Row(
            Modifier.fillMaxWidth().padding(start = 8.dp, end = 8.dp, top = 8.dp),
            verticalAlignment = Alignment.Bottom,
        ) {
            IconKey(onClick = onAttach, boxed = true) { Icon(Icons.Filled.Add, contentDescription = "Attach photos, videos or files", tint = Ink.text) }
            Spacer(Modifier.width(6.dp))
            BasicTextField(
                value = field ?: TextFieldValue(message, TextRange(message.length)),
                onValueChange = { v -> onField?.invoke(v) ?: onMessage(v.text) },
                textStyle = Type.body.copy(color = Ink.text),
                cursorBrush = SolidColor(Ink.yellow),
                maxLines = 4,
                modifier = Modifier.weight(1f),
                decorationBox = { inner ->
                    Box(
                        Modifier
                            .fillMaxWidth()
                            .heightIn(min = 46.dp)
                            .clip(RoundedCornerShape(12.dp))
                            .background(Ink.pen)
                            .border(1.dp, Ink.rule, RoundedCornerShape(12.dp))
                            .padding(horizontal = 14.dp, vertical = 12.dp),
                    ) {
                        if (message.isEmpty()) Text("Message $name…", style = Type.body, color = Ink.dust.copy(alpha = 0.7f), maxLines = 1)
                        inner()
                    }
                },
            )
            Spacer(Modifier.width(6.dp))
            val uploading = attachments.any { !it.ready && it.error == null }
            val hasContent = message.isNotBlank() || attachments.any { it.ready }
            when {
                refused -> PrimaryButton("Send anyway", enabled = !sending) { onSend(true) }
                else -> PrimaryButton(
                    when { sending -> "Sending…"; uploading -> "Uploading…"; else -> "Send" },
                    enabled = !sending && !uploading && hasContent,
                ) { onSend(false) }
            }
        }

        // The chat's quick replies, or the terminal's key bar.
        if (mode != "terminal") SnippetRow(snippets, onSnippet, onEditSnippet, onAddSnippet, onEsc)
        else Row(
            Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 8.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            KEYS.forEach { (label, key) ->
                val on = key == "ctrl" && ctrl
                Box(
                    Modifier
                        .heightIn(min = 42.dp)
                        .clip(RoundedCornerShape(10.dp))
                        .background(if (on) Ink.text else Ink.pen)
                        .border(1.dp, if (on) Ink.text else Ink.rule, RoundedCornerShape(10.dp))
                        .clickable(role = Role.Button) { onKey(key) }
                        .padding(horizontal = 13.dp, vertical = 9.dp),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(label, style = Type.label.copy(fontFamily = JetBrains), color = if (on) Ink.night else Ink.text)
                }
            }
        }
    }
}

/** Chat, Changes, Terminal: under the heading, the one showing underlined. */
@Composable
private fun ViewTabs(mode: String, chatAvailable: Boolean, onMode: (String) -> Unit) {
    val tabs = listOfNotNull("chat" to "Chat", "changes" to "Changes", "terminal" to "Terminal").filter { it.first != "chat" || chatAvailable }
    Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp)) {
        tabs.forEach { (key, label) ->
            val on = mode == key
            Column(
                Modifier
                    .weight(1f)
                    .clip(RoundedCornerShape(8.dp))
                    .clickable(role = Role.Tab) { onMode(key) }
                    .padding(top = 8.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Text(label, style = Type.label, color = if (on) Ink.text else Ink.dust)
                Spacer(Modifier.height(8.dp))
                Box(Modifier.fillMaxWidth(0.5f).height(2.dp).clip(RoundedCornerShape(1.dp)).background(if (on) Ink.text else Color.Transparent))
            }
        }
    }
    Box(Modifier.fillMaxWidth().height(1.dp).background(Ink.rule))
}

@Composable
private fun IconKey(onClick: () -> Unit, boxed: Boolean = false, content: @Composable () -> Unit) {
    Box(
        Modifier
            .size(if (boxed) 46.dp else 48.dp)
            .clip(RoundedCornerShape(12.dp))
            .then(if (boxed) Modifier.background(Ink.pen).border(1.dp, Ink.rule, RoundedCornerShape(12.dp)) else Modifier)
            .clickable(role = Role.Button, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) { content() }
}

/** A picked file: its picture or type, name, and how its upload is going. */
@Composable
private fun AttachmentChip(a: Attachment, onRemove: () -> Unit) {
    Row(
        Modifier
            .height(52.dp)
            .widthIn(max = 220.dp)
            .clip(RoundedCornerShape(10.dp))
            .background(Ink.pen)
            .border(1.dp, if (a.error != null) Ink.red else Ink.rule, RoundedCornerShape(10.dp)),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(52.dp).background(Ink.night), contentAlignment = Alignment.Center) {
            if (a.thumb != null) {
                Image(a.thumb.asImageBitmap(), contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
            } else {
                Text(a.name.substringAfterLast('.', "file").take(4).uppercase(), style = Type.tagSmall, color = Ink.dust)
            }
            if (a.mime.startsWith("video/")) {
                Text("▶", style = Type.label, color = Ink.text, modifier = Modifier.align(Alignment.BottomStart).padding(4.dp))
            }
        }
        Column(Modifier.weight(1f, fill = false).padding(horizontal = 10.dp)) {
            Text(a.name, style = Type.label, color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                when {
                    a.error != null -> "Didn't upload"
                    a.ready -> "Ready · ${humanSize(a.size)}"
                    else -> "Uploading ${(a.progress * 100).toInt()}%"
                },
                style = Type.small, color = if (a.error != null) Ink.red else Ink.dust, maxLines = 1,
            )
        }
        Box(
            Modifier.fillMaxHeight().width(36.dp).clickable(role = Role.Button, onClick = onRemove),
            contentAlignment = Alignment.Center,
        ) { Icon(Icons.Filled.Close, contentDescription = "Remove ${a.name}", tint = Ink.dust, modifier = Modifier.size(16.dp)) }
    }
}
