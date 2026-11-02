package dev.akbxr.herdsman
import androidx.activity.compose.BackHandler
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Close
import androidx.compose.foundation.border
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Icon
import androidx.compose.material.icons.filled.Notifications
import androidx.compose.material.icons.Icons
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.compose.rememberLauncherForActivityResult
import android.provider.Settings
import android.os.Build
import android.content.Intent
import android.Manifest

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.input.OffsetMapping
import androidx.compose.ui.text.input.TransformedText
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.sp
import com.google.mlkit.vision.barcode.common.Barcode
import com.google.mlkit.vision.codescanner.GmsBarcodeScannerOptions
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch

/** Checks a pairing by asking the Mac its name with the token. */
suspend fun confirmPairing(info: PairInfo): PairInfo = info.copy(host = Api(info.address, info.token).ping())

/**
 * Pairing: scan the QR code `herdsman pair` shows on the Mac, or type
 * its address and six-digit code. A herdsman:// link opened from
 * elsewhere (another scanner app) arrives as [incoming].
 */
@Composable
fun ConnectScreen(
    initialAddress: String,
    incoming: String?,
    onIncomingUsed: () -> Unit,
    onPaired: (PairInfo) -> Unit,
    onKeepCurrent: (() -> Unit)? = null,  // already paired: back without changing
) {
    var address by remember { mutableStateOf(initialAddress.removePrefix("http://")) }
    var code by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf<String?>(null) }   // what it is doing, or null
    var problem by remember { mutableStateOf<String?>(null) }
    val scope = rememberCoroutineScope()
    val context = LocalContext.current

    fun pairWith(link: String) {
        val info = PairFlow.parseLink(link)
        if (info == null) {
            problem = "That QR code isn't from herdsman. Run herdsman pair on your Mac and scan the code it shows."
            return
        }
        busy = "Pairing…"
        problem = null
        scope.launch {
            runCatching { confirmPairing(info) }
                .onSuccess { onPaired(it); onIncomingUsed() }
                .onFailure { problem = it.advice() }
            busy = null
        }
    }

    fun pairWithCode() {
        if (busy != null || code.length != 6 || address.isBlank()) return
        busy = "Pairing…"
        problem = null
        scope.launch {
            runCatching { confirmPairing(PairFlow.redeem(PairFlow.normalizeAddress(address), code)) }
                .onSuccess { onPaired(it); onIncomingUsed() }
                .onFailure { problem = it.advice(); code = "" }  // a used or wrong code won't work again
            busy = null
        }
    }

    // A link opened from outside the app (another scanner): pair with it.
    LaunchedEffect(incoming) {
        incoming?.let(::pairWith)
    }

    Column(
        Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 24.dp, vertical = 32.dp),
    ) {
        EarTag(Agent("t1", "", null, State.Idle, "", "", false, ""), width = 46.dp)
        Spacer(Modifier.height(28.dp))
        Text("Pair with your Mac", style = Type.headline, color = Ink.text)
        Spacer(Modifier.height(10.dp))
        Text(
            buildAnnotatedString {
                append("On your Mac, run ")
                withStyle(SpanStyle(fontFamily = JetBrains, color = Ink.text)) { append("herdsman pair") }
                append(". It shows a QR code.")
            },
            style = Type.body, color = Ink.dust,
        )
        Spacer(Modifier.height(24.dp))
        PrimaryButton(busy ?: "Scan the code", enabled = busy == null, modifier = Modifier.fillMaxWidth()) {
            problem = null
            val options = GmsBarcodeScannerOptions.Builder().setBarcodeFormats(Barcode.FORMAT_QR_CODE).build()
            GmsBarcodeScanning.getClient(context, options).startScan()
                .addOnSuccessListener { barcode -> barcode.rawValue?.let(::pairWith) }
                .addOnFailureListener {
                    problem = "The scanner didn't open. It needs Google Play services; type the code below instead."
                }
        }
        problem?.let { Spacer(Modifier.height(16.dp)); Notice(it) }

        Spacer(Modifier.height(36.dp))
        Text("No camera? Type what the Mac shows under the QR code.", style = Type.small, color = Ink.dust)
        Spacer(Modifier.height(14.dp))
        Field("Address", address, { address = it.trim() }, "100.101.102.103:7710", keyboard = KeyboardType.Uri)
        Spacer(Modifier.height(16.dp))
        Field(
            "Code", code,
            { typed ->
                val digits = typed.filter(Char::isDigit).take(6)
                val completed = digits.length == 6 && code.length < 6
                code = digits
                if (completed) pairWithCode()  // the sixth digit pairs: the keyboard hides the button
            },
            "000 000",
            keyboard = KeyboardType.NumberPassword,
            style = Type.headline.copy(fontSize = 22.sp, letterSpacing = 2.sp),
            transformation = SixDigits,
            onDone = ::pairWithCode,
        )
        Spacer(Modifier.height(16.dp))
        QuietButton(
            busy ?: "Pair with the code",
            color = if (busy == null && code.length == 6 && address.isNotBlank()) Ink.text else Ink.dust,
            modifier = Modifier.fillMaxWidth(),
            onClick = ::pairWithCode,
        )
        onKeepCurrent?.let {
            Spacer(Modifier.height(8.dp))
            QuietButton("Keep the Mac I'm paired with", modifier = Modifier.fillMaxWidth(), onClick = it)
        }
    }
}

/** Shows six digits as "123 456" while keeping the value plain digits. */
private object SixDigits : VisualTransformation {
    override fun filter(text: AnnotatedString): TransformedText {
        val s = text.text
        val shown = if (s.length > 3) s.substring(0, 3) + " " + s.substring(3) else s
        return TransformedText(AnnotatedString(shown), object : OffsetMapping {
            override fun originalToTransformed(offset: Int) = if (offset > 3) offset + 1 else offset
            override fun transformedToOriginal(offset: Int) = if (offset > 3) offset - 1 else offset.coerceAtMost(s.length)
        })
    }
}

private val order = listOf(State.NeedsYou, State.Working, State.Done, State.Idle)

@Composable
fun HerdScreen(
    host: String,
    herd: Herd?,
    problem: String?,
    onOpen: (Agent) -> Unit,
    onPairAgain: () -> Unit,
    problemAction: Pair<String, () -> Unit>? = null,  // e.g. Open Tailscale
    onNew: () -> Unit = {},  // New: a task (an agent on something) or a tab
    onNewTask: () -> Unit = {},
    onActions: (Agent) -> Unit = {},  // long press: split, zoom, rename, close
    notifications: @Composable () -> Unit = {},
    notifyOn: Boolean = false,
    onNotifyMenu: () -> Unit = {},  // the bell: turn off, background setting
    onMachines: () -> Unit = {},  // the machines sheet: add, reconnect, remove
    search: String? = null,  // open with the search showing these words (snapshots)
) {
    // Search: a field under the header; while it holds words, one flat list of what matches.
    var searching by rememberSaveable { mutableStateOf(search != null) }
    var query by rememberSaveable { mutableStateOf(search.orEmpty()) }
    if (searching) BackHandler { searching = false; query = "" }
    val all = herd?.agents.orEmpty().sortedWith(compareBy({ order.indexOf(it.state) }, { it.tabName }))
    val filtering = searching && query.isNotBlank()
    val found = if (filtering) all.filter { it.matches(query) } else all
    val agents = all
    val appAgents = agents.filter { !it.cli }
    val cliAgents = agents.filter { it.cli }
    // The Herdr app's terminals on other machines, or a machine added: grouped by machine.
    val groups = herd?.let { machineGroups(it, appAgents) }.orEmpty()
    // Panes of the herdr CLI beside the Herdr app's: a section each.
    val sections = cliAgents.isNotEmpty() && (appAgents.isNotEmpty() || groups.isNotEmpty())
    val needs = agents.count { it.state == State.NeedsYou }
    val working = agents.count { it.state == State.Working }

    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
        item {
            Column(Modifier.padding(start = 24.dp, end = 12.dp, top = 20.dp, bottom = 8.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        buildAnnotatedString {
                            append(host.removeSuffix(".local").ifBlank { "Your Mac" })
                            if (herd != null && herd.layout.isNotBlank()) {
                                withStyle(SpanStyle(color = Ink.dust.copy(alpha = 0.7f))) { append("  ·  ${herd.layout}") }
                            }
                        },
                        style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f),
                    )
                    if (all.isNotEmpty() && !searching) Box(
                        Modifier.size(40.dp).clip(RoundedCornerShape(10.dp)).clickable(role = Role.Button) { searching = true },
                        contentAlignment = Alignment.Center,
                    ) { Icon(Icons.Filled.Search, contentDescription = "Search the terminals", tint = Ink.dust, modifier = Modifier.size(19.dp)) }
                    if (notifyOn) {
                        Box(
                            Modifier.size(40.dp).clip(RoundedCornerShape(10.dp)).clickable(role = Role.Button, onClick = onNotifyMenu),
                            contentAlignment = Alignment.Center,
                        ) { Icon(Icons.Filled.Notifications, contentDescription = "Notifications are on", tint = Ink.dust, modifier = Modifier.size(18.dp)) }
                    }
                    // Other machines: their sheet from here (none yet: from New).
                    if (groups.isNotEmpty()) Box(
                        Modifier.size(40.dp).clip(RoundedCornerShape(10.dp)).clickable(role = Role.Button, onClick = onMachines)
                            .semantics { contentDescription = "Machines" },
                        contentAlignment = Alignment.Center,
                    ) { MachinesIcon(tint = Ink.dust) }
                    QuietButton("Pair again", onClick = onPairAgain)
                    QuietButton("New", color = Ink.text, onClick = onNew)
                }
                Spacer(Modifier.height(6.dp))
                val headline = when {
                    herd == null -> "Looking for the herd…"
                    needs > 0 -> "$needs needs you"
                    working > 0 -> "$working working"
                    agents.isEmpty() -> "No terminals open"
                    else -> "All quiet"
                }
                Text(headline, style = Type.headline, color = if (needs > 0) Ink.yellow else Ink.text)
                if (herd != null && agents.isEmpty()) {
                    Spacer(Modifier.height(16.dp))
                    PrimaryButton("Start a task", modifier = Modifier.padding(end = 12.dp), onClick = onNewTask)
                }
                if (problem != null) {
                    Spacer(Modifier.height(14.dp))
                    Notice(problem, Modifier.padding(end = 12.dp), action = problemAction)
                }
                Spacer(Modifier.height(14.dp))
                if (searching) {
                    HerdSearchField(query, onQuery = { query = it }, onClose = { searching = false; query = "" }, modifier = Modifier.padding(end = 12.dp))
                    Spacer(Modifier.height(8.dp))
                }
            }
        }
        if (filtering) {
            if (found.isEmpty()) item(key = "nomatch") {
                Text(
                    "Nothing called “${query.trim()}”. Search looks in agents, session titles, tabs, spaces, folders and addresses.",
                    style = Type.small, color = Ink.dust, modifier = Modifier.padding(horizontal = 24.dp, vertical = 12.dp),
                )
            }
            items(found, key = { it.terminal }) { agent ->
                AgentRow(agent, onClick = { onOpen(agent) }, onLongClick = { onActions(agent) }, where = true)
            }
            item { notifications() }
            return@LazyColumn
        }
        if (sections) item(key = "b:app") {
            val spaces = appAgents.map { it.spaceId }.distinct().size
            BackendHeader("Herdr app", if (spaces > 0) plural(spaces, "space") else null, appAgents.size)
        }
        if (groups.isEmpty()) {
            items(if (sections) appAgents else agents, key = { it.terminal }) { agent ->
                AgentRow(agent, onClick = { onOpen(agent) }, onLongClick = { onActions(agent) })
            }
        } else groups.forEach { group ->
            item(key = "m:" + group.name) { MachineHeader(group, onClick = onMachines) }
            items(group.agents, key = { it.terminal }) { agent ->
                AgentRow(agent, onClick = { onOpen(agent) }, onLongClick = { onActions(agent) })
            }
        }
        if (sections) {
            item(key = "b:cli") {
                val spaces = herd?.workspaces?.size?.takeIf { it > 0 } ?: cliAgents.map { it.spaceId }.distinct().size
                BackendHeader("herdr CLI", plural(spaces, "workspace"), cliAgents.size)
            }
            items(cliAgents, key = { it.terminal }) { agent ->
                AgentRow(agent, onClick = { onOpen(agent) }, onLongClick = { onActions(agent) })
            }
        }
        if (agents.isNotEmpty()) item {
            Text(
                "Press and hold a terminal to split, zoom, rename or close it.",
                style = Type.small, color = Ink.dust.copy(alpha = 0.7f),
                modifier = Modifier.padding(horizontal = 24.dp, vertical = 16.dp),
            )
        }
        item { notifications() }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun AgentRow(agent: Agent, onClick: () -> Unit, onLongClick: () -> Unit, where: Boolean = false) {
    Row(
        Modifier
            .fillMaxWidth()
            .combinedClickable(role = Role.Button, onClick = onClick, onLongClick = onLongClick, onLongClickLabel = "Split, zoom, rename or close")
            .padding(horizontal = 24.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        EarTag(agent)
        Spacer(Modifier.width(18.dp))
        Column(Modifier.weight(1f)) {
            // "tego · Fix the nav bar": the session's title after the agent's name.
            Text(
                buildAnnotatedString {
                    append(if (agent.session != null) agent.name else agent.title)
                    agent.session?.let { withStyle(SpanStyle(fontWeight = FontWeight.Normal, color = Ink.text.copy(alpha = 0.82f))) { append("  ·  $it") } }
                },
                style = Type.title, color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.height(3.dp))
            Text(
                buildAnnotatedString {
                    withStyle(SpanStyle(color = if (agent.state == State.Idle) Ink.dust else agent.state.tagColor())) { append(agent.state.word) }
                    withStyle(SpanStyle(color = Ink.dust)) {
                        // A herdr CLI pane: its workspace too, "api / Tab 2".
                        val tab = if (agent.tabName.all(Char::isDigit)) "Tab ${agent.tabName}" else agent.tabName
                        append(if (agent.cli) "  ·  ${agent.spaceName} / $tab" else "  ·  Tab ${agent.tabName}")
                        // In search results, out of their machine's group: the machine.
                        if (where && agent.machine.isNotEmpty()) append(", ${agent.machine}")
                        if (agent.tabActive) append(", on screen")
                    }
                },
                style = Type.small, maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/**
 * Whether every word of [query] is somewhere in what the row is: the agent,
 * its session's title, its name, tab, space or workspace, folder, address,
 * machine or state ("claude pki", "api tab 2", "p3", "needs").
 */
internal fun Agent.matches(query: String): Boolean {
    val words = query.lowercase().split(Regex("\\s+")).filter { it.isNotEmpty() }
    if (words.isEmpty()) return true
    val tab = if (tabName.all(Char::isDigit)) "tab $tabName" else tabName
    val hay = listOfNotNull(app, agentTitle(app), label, session, tab, spaceName, cwd, terminal, tagNumber, machine, state.word)
        .joinToString("\n").lowercase()
    return words.all { it in hay }
}

/** The search field over the herd, as the chat's ([ChatSearchBar]). */
@Composable
private fun HerdSearchField(query: String, onQuery: (String) -> Unit, onClose: () -> Unit, modifier: Modifier = Modifier) {
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
    Row(modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Row(
            Modifier
                .weight(1f)
                .height(44.dp)
                .clip(RoundedCornerShape(10.dp))
                .background(Ink.pen)
                .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
                .padding(horizontal = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(Icons.Filled.Search, contentDescription = null, tint = Ink.dust, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(8.dp))
            BasicTextField(
                value = query,
                onValueChange = onQuery,
                singleLine = true,
                textStyle = Type.body.copy(color = Ink.text),
                cursorBrush = SolidColor(Ink.yellow),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                modifier = Modifier.weight(1f).focusRequester(focus),
                decorationBox = { inner ->
                    Box(contentAlignment = Alignment.CenterStart) {
                        if (query.isEmpty()) Text("Agent, session, tab, space, folder…", style = Type.body, color = Ink.dust.copy(alpha = 0.7f), maxLines = 1)
                        inner()
                    }
                },
            )
        }
        Box(
            Modifier.size(44.dp).clip(RoundedCornerShape(10.dp)).clickable(role = Role.Button, onClick = onClose),
            contentAlignment = Alignment.Center,
        ) { Icon(Icons.Filled.Close, contentDescription = "Close the search", tint = Ink.text) }
    }
}

private fun plural(n: Int, word: String) = if (n == 1) "1 $word" else "$n ${word}s"

/**
 * The heading of the Herdr app's terminals or the herdr CLI's panes, when
 * both show: as a machine's ([MachineHeader]), [detail] after its name.
 */
@Composable
private fun BackendHeader(label: String, detail: String?, count: Int) {
    Row(
        Modifier.fillMaxWidth().padding(start = 24.dp, end = 24.dp, top = 16.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        MachineDot("connected")
        Spacer(Modifier.width(9.dp))
        Text(
            buildAnnotatedString {
                withStyle(SpanStyle(color = Ink.text)) { append(label) }
                detail?.let { withStyle(SpanStyle(color = Ink.dust)) { append("  ·  $it") } }
            },
            style = Type.label, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f),
        )
        if (count > 0) Text(count.toString(), style = Type.small, color = Ink.dust)
    }
}

/**
 * Notifications while the app is closed: on or off, and what Android needs
 * for them (the permission on 13+, running in the background).
 */
@Composable
fun NotificationsRow(on: Boolean, onChange: (Boolean) -> Unit) {
    val context = LocalContext.current
    val pairing = remember { Pairing(context) }
    var allowed by remember { mutableStateOf(Watch.canNotify(context)) }
    var unrestricted by remember { mutableStateOf(Watch.unrestricted(context)) }
    var hintDismissed by remember { mutableStateOf(pairing.backgroundHintDismissed) }
    // Coming back from Android's settings or its dialog: look again.
    val onScreen = rememberOnScreen()
    LaunchedEffect(onScreen) {
        if (onScreen) { allowed = Watch.canNotify(context); unrestricted = Watch.unrestricted(context) }
    }
    val askPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        allowed = granted
        if (granted) onChange(true)
    }

    // Once on and working, the herd stays clean: the bell in the header
    // turns it off. The card comes back only when something is wrong.
    val showHint = on && allowed && !unrestricted && !hintDismissed
    if (on && allowed && !showHint) return

    NotificationsCard(
        on = on, allowed = allowed, unrestricted = unrestricted || hintDismissed,
        onToggle = {
            when {
                on -> onChange(false)
                !allowed && Build.VERSION.SDK_INT >= 33 -> askPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
                else -> onChange(true)
            }
        },
        onOpenSettings = {
            context.startActivity(
                Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
        },
        onUnrestrict = { Watch.askUnrestricted(context) },
        onNotNow = { hintDismissed = true; pairing.backgroundHintDismissed = true },
    )
}

/** What the bell offers: turning notifications off, and the background setting. */
@Composable
fun NotificationsSheet(onTurnOff: () -> Unit, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val unrestricted = remember { Watch.unrestricted(context) }
    ActionSheet(
        title = "Notifications are on",
        subtitle = "You'll hear when an agent needs you, finishes or fails, with the app closed.",
        onDismiss = onDismiss,
        actions = listOfNotNull(
            if (!unrestricted) SheetAction("Let it run in the background", "So Android doesn't stop it while the phone sleeps") {
                onDismiss(); Watch.askUnrestricted(context)
            } else null,
            SheetAction("Turn off notifications", danger = true) { onDismiss(); onTurnOff() },
        ),
    )
}

/** The notifications setting as shown, without Android (also for snapshots). */
@Composable
fun NotificationsCard(
    on: Boolean, allowed: Boolean, unrestricted: Boolean,
    onToggle: () -> Unit, onOpenSettings: () -> Unit, onUnrestrict: () -> Unit,
    onNotNow: () -> Unit = {},
) {
    Column(
        Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp, vertical = 8.dp)
            .clip(RoundedCornerShape(14.dp))
            .background(Ink.pen)
            .padding(horizontal = 16.dp, vertical = 14.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("Notifications", style = Type.label, color = Ink.text)
                Text(
                    if (on) "On: you'll hear when an agent needs you, finishes or fails."
                    else "Hear when an agent needs you, finishes or fails, with the app closed.",
                    style = Type.small, color = Ink.dust,
                )
            }
            Spacer(Modifier.width(12.dp))
            QuietButton(if (on) "Turn off" else "Turn on", color = Ink.text, onClick = onToggle)
        }
        if (on && !allowed) {
            Spacer(Modifier.height(10.dp))
            Text("Notifications are blocked for Herdsman in Android's settings.", style = Type.small, color = Ink.yellow)
            QuietButton("Open settings", color = Ink.text, onClick = onOpenSettings)
        } else if (on && !unrestricted) {
            Spacer(Modifier.height(10.dp))
            Text(
                "Android may stop it while the phone sleeps. Let it run in the background so notifications keep coming.",
                style = Type.small, color = Ink.dust,
            )
            Row {
                QuietButton("Let it run in the background", color = Ink.text, onClick = onUnrestrict)
                QuietButton("Not now", onClick = onNotNow)
            }
        }
    }
}
