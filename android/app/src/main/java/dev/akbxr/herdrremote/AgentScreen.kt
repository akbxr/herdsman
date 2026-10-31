package dev.akbxr.herdrremote

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
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.ScrollState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import org.json.JSONObject

/** Keys a terminal agent's questions need: menus, confirmations, stopping. */
private val KEYS = listOf(
    "Esc" to "esc", "↑" to "up", "↓" to "down", "←" to "left", "→" to "right",
    "Tab" to "tab", "⏎" to "enter", "y" to "y", "n" to "n", "Ctrl+C" to "ctrl+c",
)

@Composable
fun AgentScreen(api: Api, terminal: String, agent: Agent?, onBack: () -> Unit) {
    var screen by remember(terminal) { mutableStateOf<String?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    var message by remember { mutableStateOf("") }
    var sending by remember { mutableStateOf(false) }
    var refusedText by remember { mutableStateOf<String?>(null) }  // a send the program refused
    val scope = rememberCoroutineScope()
    val vertical = rememberScrollState()
    val horizontal = rememberScrollState()
    val name = agent?.title ?: terminal  // for messages below

    // The terminal, refreshed while open; stays at the bottom unless you scrolled up.
    LaunchedEffect(terminal) {
        while (true) {
            runCatching { api.read(terminal) }
                .onSuccess { text ->
                    val atBottom = vertical.value >= vertical.maxValue - 40
                    screen = text
                    problem = null
                    if (atBottom) {
                        delay(16)
                        vertical.scrollTo(vertical.maxValue)
                    }
                }
                .onFailure { problem = it.advice() }
            delay(1500)
        }
    }

    fun act(block: suspend () -> Unit) {
        scope.launch { runCatching { block() }.onFailure { problem = it.advice() } }
    }

    fun send(force: Boolean) {
        val text = (refusedText ?: message).trim()
        if (text.isEmpty() || sending) return
        sending = true
        scope.launch {
            runCatching { api.send(terminal, text, force) }
                .onSuccess { message = ""; refusedText = null; problem = null }
                .onFailure {
                    // hc send won't type into a program that is asking something.
                    if (!force && agent?.state == State.NeedsYou) {
                        refusedText = text
                        problem = "$name is waiting on a question. Answer it with the keys, or send anyway to type into it."
                    } else {
                        problem = it.advice()
                    }
                }
            sending = false
        }
    }

    AgentContent(
        terminal = terminal, agent = agent, screen = screen, problem = problem,
        message = message, onMessage = { message = it; refusedText = null },
        sending = sending, refused = refusedText != null,
        vertical = vertical, horizontal = horizontal,
        onBack = onBack,
        onShowOnMac = { agent?.let { a -> act { api.command("tab.select", JSONObject().put("id", a.tabId)) } } },
        onKey = { key -> act { api.keys(terminal, key) } },
        onSend = { force -> send(force) },
    )
}

/** What the agent screen shows, without the network (also used for snapshots). */
@Composable
fun AgentContent(
    terminal: String,
    agent: Agent?,
    screen: String?,
    problem: String?,
    message: String,
    onMessage: (String) -> Unit,
    sending: Boolean,
    refused: Boolean,
    vertical: ScrollState,
    horizontal: ScrollState,
    onBack: () -> Unit,
    onShowOnMac: () -> Unit,
    onKey: (String) -> Unit,
    onSend: (force: Boolean) -> Unit,
) {
    val name = agent?.title ?: terminal
    Column(Modifier.fillMaxSize()) {
        // Heading: back, the tag's number and name, and the way to bring it up on the Mac.
        Row(
            Modifier.fillMaxWidth().padding(start = 4.dp, end = 8.dp, top = 6.dp, bottom = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(
                Modifier.size(48.dp).clip(RoundedCornerShape(12.dp)).clickable(role = Role.Button, onClick = onBack),
                contentAlignment = Alignment.Center,
            ) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back to the herd", tint = Ink.text) }
            if (agent != null) {
                EarTag(agent, width = 30.dp)
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
                QuietButton("Show on Mac", color = Ink.text, onClick = onShowOnMac)
            }
        }

        // The terminal well.
        Box(
            Modifier
                .weight(1f)
                .fillMaxWidth()
                .padding(horizontal = 12.dp)
                .clip(RoundedCornerShape(14.dp))
                .background(Ink.pen)
                .border(1.dp, Ink.rule, RoundedCornerShape(14.dp)),
        ) {
            val text = screen
            if (text == null) {
                Text("Reading $terminal…", style = Type.small, color = Ink.dust, modifier = Modifier.padding(16.dp))
            } else {
                Text(
                    text.ifEmpty { "(empty screen)" },
                    style = Type.terminal,
                    color = Ink.text,
                    softWrap = false,
                    modifier = Modifier
                        .fillMaxSize()
                        .verticalScroll(vertical)
                        .horizontalScroll(horizontal)
                        .padding(12.dp),
                )
            }
        }

        problem?.let {
            Spacer(Modifier.height(10.dp))
            Notice(it, Modifier.padding(horizontal = 12.dp))
        }

        // Keys, for answering menus and confirmations.
        Row(
            Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp, vertical = 10.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            KEYS.forEach { (label, key) ->
                Box(
                    Modifier
                        .heightIn(min = 44.dp)
                        .clip(RoundedCornerShape(10.dp))
                        .background(Ink.pen)
                        .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
                        .clickable(role = Role.Button) { onKey(key) }
                        .padding(horizontal = 14.dp, vertical = 10.dp),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(label, style = Type.label.copy(fontFamily = JetBrains), color = if (key == "ctrl+c") Ink.red else Ink.text)
                }
            }
        }

        // Message box.
        Row(
            Modifier.fillMaxWidth().padding(start = 12.dp, end = 12.dp, bottom = 12.dp),
            verticalAlignment = Alignment.Bottom,
        ) {
            BasicTextField(
                value = message,
                onValueChange = onMessage,
                textStyle = Type.body.copy(color = Ink.text),
                cursorBrush = SolidColor(Ink.yellow),
                maxLines = 5,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
                keyboardActions = KeyboardActions(onSend = { onSend(false) }),
                modifier = Modifier.weight(1f),
                decorationBox = { inner ->
                    Box(
                        Modifier
                            .fillMaxWidth()
                            .heightIn(min = 48.dp)
                            .clip(RoundedCornerShape(12.dp))
                            .background(Ink.pen)
                            .border(1.dp, Ink.rule, RoundedCornerShape(12.dp))
                            .padding(horizontal = 14.dp, vertical = 13.dp),
                    ) {
                        if (message.isEmpty()) Text("Message $name…", style = Type.body, color = Ink.dust.copy(alpha = 0.7f))
                        inner()
                    }
                },
            )
            Spacer(Modifier.width(8.dp))
            if (refused) {
                PrimaryButton("Send anyway", enabled = !sending) { onSend(true) }
            } else {
                PrimaryButton(if (sending) "Sending…" else "Send", enabled = !sending && message.isNotBlank()) { onSend(false) }
            }
        }
    }
}
