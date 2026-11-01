package dev.akbxr.herdrremote

import android.content.Intent
import android.os.Bundle
import android.widget.Toast
import androidx.compose.runtime.rememberCoroutineScope
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    // A herdr-remote://pair link this activity was opened with, until used.
    private var link by mutableStateOf<String?>(null)
    // A terminal a notification asked to open, until opened.
    private var openFromNotification by mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        if (savedInstanceState == null) {
            link = intent?.dataString
            openFromNotification = intent?.getStringExtra(Watch.EXTRA_TERMINAL)
        }
        Watch.start(this)  // if notifications are on and it isn't running (e.g. after an update)
        setContent {
            HerdrTheme {
                App(link, onLinkUsed = { link = null }, openFromNotification, onOpened = { openFromNotification = null })
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        intent.dataString?.let { link = it }
        intent.getStringExtra(Watch.EXTRA_TERMINAL)?.let { openFromNotification = it }
    }

    override fun onStart() { super.onStart(); Watch.appVisible = true }
    override fun onStop() { Watch.appVisible = false; super.onStop() }
}

private enum class Section { Herd, Screen }

@Composable
private fun App(link: String?, onLinkUsed: () -> Unit, openFromNotification: String?, onOpened: () -> Unit) {
    val context = LocalContext.current
    val pairing = remember { Pairing(context) }
    var api by remember {
        mutableStateOf(if (pairing.address.isNotBlank() && pairing.token.isNotBlank()) Api(pairing.address, pairing.token) else null)
    }
    var host by remember { mutableStateOf(pairing.host) }
    var section by rememberSaveable { mutableStateOf(Section.Herd) }
    val scope = rememberCoroutineScope()  // outlives sheets and screens: commands to the Mac finish
    var openTerminal by rememberSaveable { mutableStateOf<String?>(null) }
    var newTask by rememberSaveable { mutableStateOf(false) }
    var resumeList by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(openFromNotification) {
        openFromNotification?.let { openTerminal = it; section = Section.Herd; onOpened() }
    }
    var notify by remember { mutableStateOf(pairing.notify) }

    // The herd, refreshed every two seconds while paired.
    var herd by remember { mutableStateOf<Herd?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    val onScreen = rememberOnScreen()
    // A network change (Tailscale back on, Wi-Fi back) asks again at once.
    val net by rememberLink()
    var herdTick by remember { mutableStateOf(0) }  // bumped: ask again now (a machine added or removed)
    LaunchedEffect(api, onScreen, net, herdTick) {
        val a = api ?: return@LaunchedEffect
        if (!onScreen) return@LaunchedEffect
        while (true) {
            runCatching { a.herd() }
                .onSuccess { herd = it; problem = null }
                .onFailure { problem = it.advice(net) }
            delay(2000)
        }
    }
    val problemAction = ("Open Tailscale" to { Net.openTailscale(context); Unit })
        .takeIf { problem != null && net == Link.NoVpn && Net.hasTailscale(context) }

    Box(Modifier.fillMaxSize().background(Ink.night).safeDrawingPadding().imePadding()) {
        val a = api
        if (a == null || link != null) {
            // Not paired yet, or a pairing link arrived (pairs again with it).
            ConnectScreen(
                initialAddress = pairing.address,
                incoming = link,
                onIncomingUsed = onLinkUsed,
                onPaired = { info ->
                    val keepNotify = pairing.notify
                    pairing.address = info.address; pairing.token = info.token; pairing.host = info.host
                    if (keepNotify) { Watch.stop(context); pairing.notify = true; Watch.start(context) }
                    host = info.host
                    herd = null
                    openTerminal = null
                    newTask = false
                    resumeList = false
                    api = Api(info.address, info.token)
                },
                onKeepCurrent = if (a != null) onLinkUsed else null,
            )
            return@Box
        }

        val agent = openTerminal?.let { t -> herd?.agents?.firstOrNull { it.terminal == t } }
        if (openTerminal != null) {
            BackHandler { openTerminal = null }
            AgentScreen(api = a, terminal = openTerminal!!, agent = agent, appScope = scope, onBack = { openTerminal = null })
            return@Box
        }

        if (newTask) {
            BackHandler { newTask = false }
            NewTaskScreen(
                api = a, host = host, machines = herd?.machines.orEmpty(),
                onBack = { newTask = false },
                onStarted = { t -> newTask = false; section = Section.Herd; openTerminal = t },
                app = herd?.app ?: true, cli = herd?.cli ?: false, workspaces = herd?.workspaces.orEmpty(),
            )
            return@Box
        }

        if (resumeList) {
            BackHandler { resumeList = false }
            SessionsScreen(
                api = a, host = host,
                onBack = { resumeList = false },
                onTerminal = { t -> resumeList = false; section = Section.Herd; openTerminal = t },
            )
            return@Box
        }

        var newMenu by remember { mutableStateOf(false) }
        var newTab by remember { mutableStateOf(false) }
        var actionsFor by remember { mutableStateOf<Agent?>(null) }
        var notifyMenu by remember { mutableStateOf(false) }
        var machinesSheet by remember { mutableStateOf(false) }
        if (machinesSheet) MachinesSheet(a, scope, onChanged = { herdTick++ }, onDismiss = { machinesSheet = false })
        val setNotify = { on: Boolean ->
            notify = on
            pairing.notify = on
            if (on) Watch.start(context) else Watch.stop(context)
        }
        if (notifyMenu) NotificationsSheet(onTurnOff = { setNotify(false) }, onDismiss = { notifyMenu = false })
        val tell = { p: String -> Toast.makeText(context, p, Toast.LENGTH_LONG).show() }
        // Without the Herdr app (only the herdr CLI runs), what needs its window goes.
        val withApp = herd?.app ?: true
        val withCli = herd?.cli ?: false
        if (newMenu) ActionSheet(
            title = "New",
            onDismiss = { newMenu = false },
            actions = listOfNotNull(
                SheetAction("New task", "An agent in a folder you pick, on what you ask; opens here") { newMenu = false; newTask = true },
                if (withApp) SheetAction("Resume a session", "Pick up an earlier tego or Claude Code conversation in a new tab") { newMenu = false; resumeList = true } else null,
                if (withApp) SheetAction("New tab", "A shell or a program, in the folder of the terminal focused on the Mac") { newMenu = false; newTab = true } else null,
                if (withCli) SheetAction(if (withApp) "New tab in herdr" else "New tab", "A shell, in herdr's focused workspace") {
                    newMenu = false
                    scope.launch { runCatching { a.herdrNewTab(herd?.workspaces?.firstOrNull { it.focused }?.id.orEmpty()) }.onSuccess { herdTick++ }.onFailure { tell(it.advice()) } }
                } else null,
                if (withApp) SheetAction("Machines", "Add a machine Herdr runs terminals on over ssh, or manage them") { newMenu = false; machinesSheet = true } else null,
            ),
        )
        if (newTab) NewTabSheet(a, scope, onDismiss = { newTab = false }, onProblem = tell)
        actionsFor?.let { PaneActions(a, it, scope, onDismiss = { actionsFor = null }, onProblem = tell) }

        Column(Modifier.fillMaxSize()) {
            Box(Modifier.weight(1f)) {
                when (section) {
                    Section.Herd -> HerdScreen(
                        host = host,
                        herd = herd,
                        problem = problem,
                        problemAction = problemAction,
                        onOpen = { openTerminal = it.terminal },
                        onPairAgain = { Watch.stop(context); pairing.forget(); notify = false; api = null; herd = null },
                        onNew = { newMenu = true },
                        onNewTask = { newTask = true },
                        onActions = { actionsFor = it },
                        notifications = { NotificationsRow(on = notify, onChange = setNotify) },
                        notifyOn = notify,
                        onNotifyMenu = { notifyMenu = true },
                        onMachines = { machinesSheet = true },
                    )
                    Section.Screen -> ScreenScreen(api = a, herd = herd)
                }
            }
            SectionBar(
                current = section,
                needsYou = herd?.agents?.count { it.state == State.NeedsYou } ?: 0,
                onSelect = { section = it },
            )
        }
    }
}

@Composable
private fun SectionBar(current: Section, needsYou: Int, onSelect: (Section) -> Unit) {
    BottomBar(
        items = listOf("Herd" to (current == Section.Herd), "Screen" to (current == Section.Screen)),
        badge = if (needsYou > 0) needsYou.toString() else null,
        onSelect = { onSelect(if (it == 0) Section.Herd else Section.Screen) },
    )
}
