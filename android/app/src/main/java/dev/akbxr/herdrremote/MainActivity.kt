package dev.akbxr.herdrremote

import android.content.Intent
import android.os.Bundle
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

class MainActivity : ComponentActivity() {
    // A herdr-remote://pair link this activity was opened with, until used.
    private var link by mutableStateOf<String?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        if (savedInstanceState == null) link = intent?.dataString
        setContent { HerdrTheme { App(link, onLinkUsed = { link = null }) } }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        intent.dataString?.let { link = it }
    }
}

private enum class Section { Herd, Screen }

@Composable
private fun App(link: String?, onLinkUsed: () -> Unit) {
    val context = LocalContext.current
    val pairing = remember { Pairing(context) }
    var api by remember {
        mutableStateOf(if (pairing.address.isNotBlank() && pairing.token.isNotBlank()) Api(pairing.address, pairing.token) else null)
    }
    var host by remember { mutableStateOf(pairing.host) }
    var section by rememberSaveable { mutableStateOf(Section.Herd) }
    var openTerminal by rememberSaveable { mutableStateOf<String?>(null) }

    // The herd, refreshed every two seconds while paired.
    var herd by remember { mutableStateOf<Herd?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    val onScreen = rememberOnScreen()
    LaunchedEffect(api, onScreen) {
        val a = api ?: return@LaunchedEffect
        if (!onScreen) return@LaunchedEffect
        while (true) {
            runCatching { a.herd() }
                .onSuccess { herd = it; problem = null }
                .onFailure { problem = it.advice() }
            delay(2000)
        }
    }

    Box(Modifier.fillMaxSize().background(Ink.night).safeDrawingPadding().imePadding()) {
        val a = api
        if (a == null || link != null) {
            // Not paired yet, or a pairing link arrived (pairs again with it).
            ConnectScreen(
                initialAddress = pairing.address,
                incoming = link,
                onIncomingUsed = onLinkUsed,
                onPaired = { info ->
                    pairing.address = info.address; pairing.token = info.token; pairing.host = info.host
                    host = info.host
                    herd = null
                    openTerminal = null
                    api = Api(info.address, info.token)
                },
                onKeepCurrent = if (a != null) onLinkUsed else null,
            )
            return@Box
        }

        val agent = openTerminal?.let { t -> herd?.agents?.firstOrNull { it.terminal == t } }
        if (openTerminal != null) {
            BackHandler { openTerminal = null }
            AgentScreen(api = a, terminal = openTerminal!!, agent = agent, onBack = { openTerminal = null })
            return@Box
        }

        Column(Modifier.fillMaxSize()) {
            Box(Modifier.weight(1f)) {
                when (section) {
                    Section.Herd -> HerdScreen(
                        host = host,
                        herd = herd,
                        problem = problem,
                        onOpen = { openTerminal = it.terminal },
                        onPairAgain = { pairing.forget(); api = null; herd = null },
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
