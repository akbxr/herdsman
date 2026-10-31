package dev.akbxr.herdrremote

import android.content.ClipboardManager
import android.content.Context
import android.view.ViewGroup
import android.widget.FrameLayout
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import kotlinx.coroutines.launch
import org.json.JSONObject

private val TermBg = Color(0xFF24283B)  // the terminal's own background (Tokyo Night Storm)

/** The key bar: what a phone keyboard lacks, and enough to answer a menu without one. */
private val KEYS = listOf(
    "Esc" to "esc", "Tab" to "tab", "Ctrl" to "ctrl",
    "↑" to "up", "↓" to "down", "←" to "left", "→" to "right", "⏎" to "enter",
    "Paste" to "paste", "⌨" to "keyboard",
)

@Composable
fun AgentScreen(api: Api, terminal: String, agent: Agent?, onBack: () -> Unit) {
    val context = LocalContext.current
    var status by remember { mutableStateOf(TermStatus()) }
    var ctrl by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val controller = remember(terminal) { TerminalController(context) { status = it } }
    controller.onCtrlUsed = { ctrl = false }

    // Attached only while on screen: in a pocket, nothing streams.
    val onScreen = rememberOnScreen()
    LaunchedEffect(controller, onScreen) {
        if (onScreen) controller.connect(api, terminal)
        else controller.web.evaluateJavascript("hr.close()", null)
    }
    DisposableEffect(controller) { onDispose { controller.close() } }

    AgentContent(
        terminal = terminal, agent = agent, status = status, ctrl = ctrl, problem = problem,
        onBack = onBack,
        onShowOnMac = {
            agent?.let { a ->
                scope.launch {
                    runCatching { api.command("tab.select", JSONObject().put("id", a.tabId)) }
                        .onFailure { problem = it.advice() }
                }
            }
        },
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

/** The agent screen around its terminal (the slot), without the network. */
@Composable
fun AgentContent(
    terminal: String,
    agent: Agent?,
    status: TermStatus,
    ctrl: Boolean,
    problem: String?,
    onBack: () -> Unit,
    onShowOnMac: () -> Unit,
    onKey: (String) -> Unit,
    terminalSlot: @Composable () -> Unit,
) {
    val name = agent?.title ?: terminal
    Column(Modifier.fillMaxSize()) {
        // Heading: back, the tag, its name and state, and the way to bring it up on the Mac.
        Row(
            Modifier.fillMaxWidth().padding(start = 4.dp, end = 8.dp, top = 4.dp, bottom = 4.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(
                Modifier.size(48.dp).clip(RoundedCornerShape(12.dp)).clickable(role = Role.Button, onClick = onBack),
                contentAlignment = Alignment.Center,
            ) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back to the herd", tint = Ink.text) }
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
            if (agent != null) QuietButton("Show on Mac", color = Ink.text, onClick = onShowOnMac)
        }

        // The terminal itself, edge to edge.
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
            val overlay = when (status.state) {
                "ready", "connecting" -> "Connecting to $terminal…"
                "reconnecting" -> "Connection lost. Reconnecting…"
                "ended" -> status.reason?.replaceFirstChar { it.uppercase() } ?: "The program has ended."
                else -> null
            }
            overlay?.let {
                Box(
                    Modifier
                        .align(Alignment.TopCenter)
                        .padding(top = 10.dp)
                        .clip(RoundedCornerShape(10.dp))
                        .background(Ink.pen)
                        .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
                        .padding(horizontal = 12.dp, vertical = 7.dp),
                ) { Text(it, style = Type.small, color = Ink.text) }
            }
        }

        // Whose size the terminal has: typing here takes it over.
        val sizeLine = when {
            status.cols == 0 -> " "
            status.driving -> "Sized for this phone · ${status.cols}×${status.rows}"
            else -> "The Mac's size, ${status.cols}×${status.rows} · type to fit it to this phone"
        }
        Text(sizeLine, style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(start = 14.dp, end = 14.dp, top = 8.dp))

        problem?.let { Notice(it, Modifier.padding(horizontal = 8.dp, vertical = 6.dp)) }

        // Key bar.
        Row(
            Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 8.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            KEYS.forEach { (label, key) ->
                val on = key == "ctrl" && ctrl
                Box(
                    Modifier
                        .heightIn(min = 44.dp)
                        .clip(RoundedCornerShape(10.dp))
                        .background(if (on) Ink.text else Ink.pen)
                        .border(1.dp, if (on) Ink.text else Ink.rule, RoundedCornerShape(10.dp))
                        .clickable(role = Role.Button) { onKey(key) }
                        .padding(horizontal = 13.dp, vertical = 10.dp),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(label, style = Type.label.copy(fontFamily = JetBrains), color = if (on) Ink.night else Ink.text)
                }
            }
        }
    }
}
