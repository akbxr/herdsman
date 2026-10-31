package dev.akbxr.herdrremote
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
 * Pairing: scan the QR code `herdr-remote pair` shows on the Mac, or type
 * its address and six-digit code. A herdr-remote:// link opened from
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
            problem = "That QR code isn't from herdr-remote. Run herdr-remote pair on your Mac and scan the code it shows."
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
                withStyle(SpanStyle(fontFamily = JetBrains, color = Ink.text)) { append("herdr-remote pair") }
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
    onNewTab: () -> Unit = {},
    onActions: (Agent) -> Unit = {},  // long press: split, zoom, rename, close
    notifications: @Composable () -> Unit = {},
) {
    val agents = herd?.agents.orEmpty().sortedWith(compareBy({ order.indexOf(it.state) }, { it.tabName }))
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
                    QuietButton("Pair again", onClick = onPairAgain)
                    QuietButton("New tab", color = Ink.text, onClick = onNewTab)
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
                if (problem != null) {
                    Spacer(Modifier.height(14.dp))
                    Notice(problem, Modifier.padding(end = 12.dp))
                }
                Spacer(Modifier.height(14.dp))
            }
        }
        items(agents, key = { it.terminal }) { agent ->
            AgentRow(agent, onClick = { onOpen(agent) }, onLongClick = { onActions(agent) })
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
private fun AgentRow(agent: Agent, onClick: () -> Unit, onLongClick: () -> Unit) {
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
            Text(agent.title, style = Type.title, color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Spacer(Modifier.height(3.dp))
            Text(
                buildAnnotatedString {
                    withStyle(SpanStyle(color = if (agent.state == State.Idle) Ink.dust else agent.state.tagColor())) { append(agent.state.word) }
                    withStyle(SpanStyle(color = Ink.dust)) {
                        append("  ·  Tab ${agent.tabName}")
                        if (agent.tabActive) append(", on screen")
                    }
                },
                style = Type.small, maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/**
 * Notifications while the app is closed: on or off, and what Android needs
 * for them (the permission on 13+, running in the background).
 */
@Composable
fun NotificationsRow(on: Boolean, onChange: (Boolean) -> Unit) {
    val context = LocalContext.current
    var allowed by remember { mutableStateOf(Watch.canNotify(context)) }
    var unrestricted by remember { mutableStateOf(Watch.unrestricted(context)) }
    // Coming back from Android's settings or its dialog: look again.
    val onScreen = rememberOnScreen()
    LaunchedEffect(onScreen) {
        if (onScreen) { allowed = Watch.canNotify(context); unrestricted = Watch.unrestricted(context) }
    }
    val askPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        allowed = granted
        if (granted) onChange(true)
    }

    NotificationsCard(
        on = on, allowed = allowed, unrestricted = unrestricted,
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
    )
}

/** The notifications setting as shown, without Android (also for snapshots). */
@Composable
fun NotificationsCard(
    on: Boolean, allowed: Boolean, unrestricted: Boolean,
    onToggle: () -> Unit, onOpenSettings: () -> Unit, onUnrestrict: () -> Unit,
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
            Text("Notifications are blocked for Herdr Remote in Android's settings.", style = Type.small, color = Ink.yellow)
            QuietButton("Open settings", color = Ink.text, onClick = onOpenSettings)
        } else if (on && !unrestricted) {
            Spacer(Modifier.height(10.dp))
            Text(
                "Android may stop it while the phone sleeps. Let it run in the background so notifications keep coming.",
                style = Type.small, color = Ink.dust,
            )
            QuietButton("Let it run in the background", color = Ink.text, onClick = onUnrestrict)
        }
    }
}
