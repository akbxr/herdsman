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

private val TermBg = Color(0xFF24283B)  // the terminal's own background (Tokyo Night Storm)

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
    var message by remember { mutableStateOf("") }
    var sending by remember { mutableStateOf(false) }
    var refused by remember { mutableStateOf(false) }
    var more by remember { mutableStateOf(false) }
    var attachMenu by remember { mutableStateOf(false) }
    val attachments = remember { mutableStateListOf<Attachment>() }
    val scope = rememberCoroutineScope()
    val controller = remember(terminal) { TerminalController(context) { status = it } }
    controller.onCtrlUsed = { ctrl = false }

    // Attached only while on screen: in a pocket, nothing streams.
    val onScreen = rememberOnScreen()
    LaunchedEffect(controller, onScreen) {
        if (onScreen) controller.connect(api, terminal)
        else controller.web.evaluateJavascript("window.hr && hr.close()", null)
    }
    DisposableEffect(controller) { onDispose { controller.close() } }
    // Wi-Fi or Tailscale back: reconnect now, not at the next try.
    val link by rememberLink()
    LaunchedEffect(controller, link) { if (link == Link.Up) controller.retryNow() }
    // The keyboard covering the screen (for the terminal's own keys or the
    // message box): the terminal keeps its rows and scrolls instead.
    val keyboardUp = WindowInsets.isImeVisible
    LaunchedEffect(controller, keyboardUp) { controller.keyboard(keyboardUp) }

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
                .onSuccess { message = ""; attachments.clear(); refused = false }
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
        link = link,
        onOpenTailscale = { Net.openTailscale(context); Unit }.takeIf { Net.hasTailscale(context) },
        terminal = terminal, agent = agent, status = status, ctrl = ctrl, problem = problem,
        message = message, onMessage = { message = it; refused = false },
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
        Text(sizeLine, style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(start = 14.dp, end = 14.dp, top = 6.dp))

        problem?.let { Notice(it, Modifier.padding(horizontal = 8.dp, vertical = 6.dp)) }

        // Attachments waiting to go with the message.
        if (attachments.isNotEmpty()) {
            Row(
                Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(start = 8.dp, end = 8.dp, top = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) { attachments.forEach { AttachmentChip(it, onRemove = { onRemove(it.id) }) } }
        }

        // The message box: prose, with suggestions, sent as one message.
        Row(
            Modifier.fillMaxWidth().padding(start = 8.dp, end = 8.dp, top = 8.dp),
            verticalAlignment = Alignment.Bottom,
        ) {
            IconKey(onClick = onAttach, boxed = true) { Icon(Icons.Filled.Add, contentDescription = "Attach photos, videos or files", tint = Ink.text) }
            Spacer(Modifier.width(6.dp))
            BasicTextField(
                value = message,
                onValueChange = onMessage,
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

        // Key bar.
        Row(
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
