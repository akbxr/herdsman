package dev.akbxr.herdrremote

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** A machine's state, in words: connected, connecting…, needs login, unreachable. */
fun machineStateWord(state: String): String = when (state) {
    "connected" -> "connected"
    "connecting" -> "connecting…"
    "needs_login" -> "needs login"
    "unreachable" -> "unreachable"
    else -> state.replace('_', ' ')
}

fun machineStateColor(state: String): Color = when (state) {
    "connected" -> Ink.mint
    "connecting" -> Ink.blue
    "needs_login" -> Ink.yellow
    "unreachable" -> Ink.red
    else -> Ink.dust
}

/** The dot before a machine's name: its state's colour, breathing while it connects. */
@Composable
fun MachineDot(state: String, modifier: Modifier = Modifier) {
    if (state == "connecting") PulseDot(Ink.blue, modifier, size = 7.dp)
    else Box(modifier.size(7.dp).clip(CircleShape).background(machineStateColor(state)))
}

/** The herd's terminals of one machine ("": this Mac); [machine] null: one only seen in a terminal's address. */
data class MachineGroup(val name: String, val machine: Machine?, val agents: List<Agent>) {
    val label get() = if (name.isEmpty()) "This Mac" else machine?.label ?: name
    val state get() = if (name.isEmpty()) "connected" else machine?.state ?: "connected"
}

/**
 * The herd by machine: this Mac first, then the machines added in Herdr (in
 * their order, terminals or not), then any other a terminal names. Empty
 * when everything is on this Mac and no machine is added: the list stays flat.
 */
fun machineGroups(herd: Herd, agents: List<Agent>): List<MachineGroup> {
    val others = agents.map { it.machine }.filter { it.isNotEmpty() }.distinct()
    if (herd.machines.isEmpty() && others.isEmpty()) return emptyList()
    val known = herd.machines.map { it.name }
    val names = listOf("") + known + others.filter { it !in known }.sorted()
    return names.map { n -> MachineGroup(n, herd.machines.firstOrNull { it.name == n }, agents.filter { it.machine == n }) }
        .filter { it.name.isNotEmpty() || it.agents.isNotEmpty() }  // this Mac: only with terminals
}

/** A machine's heading in the herd: its name, state and number of terminals; tap for the Machines sheet. */
@Composable
fun MachineHeader(group: MachineGroup, onClick: () -> Unit) {
    val m = group.machine
    Column(
        Modifier
            .fillMaxWidth()
            .clickable(role = Role.Button, onClickLabel = "Machines", onClick = onClick)
            .padding(start = 24.dp, end = 24.dp, top = 16.dp, bottom = 6.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            MachineDot(group.state)
            Spacer(Modifier.width(9.dp))
            Text(
                buildAnnotatedString {
                    withStyle(SpanStyle(color = Ink.text)) { append(group.label) }
                    if (group.name.isNotEmpty()) {
                        withStyle(SpanStyle(color = if (group.state == "connected") Ink.dust else machineStateColor(group.state))) {
                            append("  ·  ${machineStateWord(group.state)}")
                        }
                    }
                },
                style = Type.label, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f),
            )
            if (group.agents.isNotEmpty()) Text(group.agents.size.toString(), style = Type.small, color = Ink.dust)
        }
        val why = m?.why?.takeIf { it.isNotBlank() && group.state != "connected" }
        val line = when {
            why != null -> why
            group.agents.isEmpty() && group.name.isNotEmpty() -> "No terminals of ${group.label} in the Herdr window."
            else -> null
        }
        line?.let {
            Text(
                it, style = Type.small, color = Ink.dust.copy(alpha = 0.8f), maxLines = 2, overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(start = 16.dp, top = 2.dp),
            )
        }
    }
}

/** The machines icon in the herd's heading: two stacked servers. */
@Composable
fun MachinesIcon(tint: Color, modifier: Modifier = Modifier) {
    Canvas(modifier.size(18.dp)) {
        val w = size.width
        val h = size.height
        val stroke = w * 0.09f
        val boxH = h * 0.36f
        listOf(h * 0.1f, h * 0.54f).forEach { top ->
            drawRoundRect(tint, Offset(w * 0.1f, top), Size(w * 0.8f, boxH), CornerRadius(w * 0.1f), style = Stroke(stroke))
            drawCircle(tint, radius = w * 0.06f, center = Offset(w * 0.28f, top + boxH / 2))
        }
    }
}

/**
 * The machines Herdr drives, from the herd's heading: their state, adding
 * one by its ssh address, reconnecting, removing. [onChanged] after each
 * change, so the herd asks again.
 */
@Composable
fun MachinesSheet(api: Api, scope: CoroutineScope, onChanged: () -> Unit, onDismiss: () -> Unit) {
    var machines by remember { mutableStateOf<List<Machine>?>(null) }
    var loadProblem by remember { mutableStateOf<String?>(null) }
    var tick by remember { mutableIntStateOf(0) }
    var host by remember { mutableStateOf("") }
    var name by remember { mutableStateOf("") }
    var adding by remember { mutableStateOf(false) }
    var addProblem by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf<Set<String>>(emptySet()) }
    var hints by remember { mutableStateOf<Map<String, String>>(emptyMap()) }
    var removeAsk by remember { mutableStateOf<Machine?>(null) }

    // Asked again every few seconds while open: a machine connecting settles.
    LaunchedEffect(api, tick) {
        while (true) {
            runCatching { api.machines() }
                .onSuccess { machines = it; loadProblem = null }
                .onFailure { loadProblem = it.advice() }
            delay(3000)
        }
    }
    fun changed() { tick++; onChanged() }

    removeAsk?.let { m ->
        ConfirmDialog(
            "Remove ${m.label}?",
            "Herdr stops driving ${m.label} from this Mac. Its terminals keep running there.",
            "Remove",
            onConfirm = {
                removeAsk = null
                busy = busy + m.name
                scope.launch {
                    runCatching { api.removeMachine(m.name) }
                        .onSuccess { hints = hints - m.name }
                        .onFailure { hints = hints + (m.name to it.advice()) }
                    busy = busy - m.name
                    changed()
                }
            },
            onDismiss = { removeAsk = null },
        )
    }

    ModelSheet(onDismiss) {
        MachinesSheetContent(
            machines = machines, loadProblem = loadProblem, busy = busy, hints = hints,
            host = host, onHost = { host = it; addProblem = null }, name = name, onName = { name = it },
            adding = adding, addProblem = addProblem,
            onAdd = add@{
                val h = host.trim()
                if (h.isEmpty() || adding) return@add
                adding = true
                addProblem = null
                scope.launch {
                    runCatching { api.addMachine(h, name.trim()) }
                        .onSuccess { host = ""; name = "" }
                        .onFailure { addProblem = it.advice() }
                    adding = false
                    changed()
                }
            },
            onReconnect = { m ->
                busy = busy + m.name
                hints = hints - m.name
                scope.launch {
                    // 409: it needs a password typed on the Mac; the reply says so.
                    runCatching { api.connectMachine(m.name) }.onFailure { hints = hints + (m.name to it.advice()) }
                    busy = busy - m.name
                    changed()
                }
            },
            onRemove = { removeAsk = it },
        )
    }
}

/** What [MachinesSheet] shows, stateless (also for snapshots). */
@Composable
fun MachinesSheetContent(
    machines: List<Machine>?,
    loadProblem: String?,
    busy: Set<String>,
    hints: Map<String, String>,
    host: String,
    onHost: (String) -> Unit,
    name: String,
    onName: (String) -> Unit,
    adding: Boolean,
    addProblem: String?,
    onAdd: () -> Unit,
    onReconnect: (Machine) -> Unit,
    onRemove: (Machine) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(bottom = 12.dp)) {
        Column(Modifier.padding(start = 24.dp, end = 24.dp, top = 8.dp, bottom = 4.dp)) {
            Text("Machines", style = Type.title, color = Ink.text)
            Text("Herdr runs terminals on these over ssh, in its own window.", style = Type.small, color = Ink.dust)
            loadProblem?.let { Spacer(Modifier.height(12.dp)); Notice(it) }
        }
        if (machines == null && loadProblem == null) {
            Column(Modifier.padding(horizontal = 24.dp, vertical = 12.dp)) {
                repeat(2) {
                    Box(Modifier.fillMaxWidth(0.4f).height(16.dp).clip(CircleShape).background(Ink.rule.copy(alpha = 0.5f)))
                    Spacer(Modifier.height(6.dp))
                    Box(Modifier.fillMaxWidth(0.6f).height(12.dp).clip(CircleShape).background(Ink.rule.copy(alpha = 0.5f)))
                    Spacer(Modifier.height(16.dp))
                }
            }
        }
        machines.orEmpty().forEach { m ->
            MachineRow(m, busy = m.name in busy, hint = hints[m.name], onReconnect = { onReconnect(m) }, onRemove = { onRemove(m) })
        }

        Text(
            "ADD A MACHINE", style = Type.tagSmall, color = Ink.dust.copy(alpha = 0.8f),
            modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 18.dp, bottom = 8.dp),
        )
        Column(Modifier.padding(horizontal = 24.dp)) {
            Field("ssh address", host, onHost, "user@host, or an ssh config alias", keyboard = KeyboardType.Uri, style = Type.body.copy(fontFamily = JetBrains))
            Spacer(Modifier.height(12.dp))
            Field("Name, optional", name, onName, "What Herdr calls it: box", onDone = onAdd)
            addProblem?.let { Spacer(Modifier.height(12.dp)); Notice(it) }
            Spacer(Modifier.height(14.dp))
            PrimaryButton(if (adding) "Connecting…" else "Add machine", enabled = !adding && host.isNotBlank(), modifier = Modifier.fillMaxWidth(), onClick = onAdd)
            if (adding) {
                Spacer(Modifier.height(8.dp))
                Text("The Mac connects over ssh; it can take a while.", style = Type.small, color = Ink.dust)
            }
        }
    }
}

/** One machine: its state, what went wrong, and what can be done. */
@Composable
private fun MachineRow(m: Machine, busy: Boolean, hint: String?, onReconnect: () -> Unit, onRemove: () -> Unit) {
    Column(Modifier.fillMaxWidth().heightIn(min = 56.dp).padding(start = 24.dp, end = 14.dp, top = 8.dp, bottom = 8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            MachineDot(m.state)
            Spacer(Modifier.width(10.dp))
            Column(Modifier.weight(1f)) {
                Text(m.label, style = Type.body.copy(fontWeight = Type.label.fontWeight), color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(
                    buildAnnotatedString {
                        withStyle(SpanStyle(color = if (m.connected) Ink.dust else machineStateColor(m.state))) { append(machineStateWord(m.state)) }
                        val host = m.host.takeIf { it.isNotBlank() && it != m.label }
                        host?.let { withStyle(SpanStyle(color = Ink.dust)) { append("  ·  $it") } }
                    },
                    style = Type.small, maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            if (!m.local) {
                when {
                    busy -> Text("Working…", style = Type.label, color = Ink.dust, modifier = Modifier.padding(horizontal = 10.dp))
                    else -> {
                        if (m.reconnectable) QuietButton("Reconnect", color = Ink.text, onClick = onReconnect)
                        QuietButton("Remove", onClick = onRemove)
                    }
                }
            }
        }
        m.why.takeIf { it.isNotBlank() && !m.connected }?.let {
            Text(it, style = Type.small, color = Ink.dust, modifier = Modifier.padding(start = 17.dp, top = 2.dp, end = 10.dp))
        }
        hint?.let { Box(Modifier.padding(start = 17.dp, top = 8.dp, end = 10.dp)) { Hint(it) } }
    }
}
