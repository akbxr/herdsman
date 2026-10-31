package dev.akbxr.herdrremote

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
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
                    HerdScreen("AKBARs-MacBook-Air.local", herd, problem = null, onOpen = {}, onPairAgain = {})
                }
                BottomBar(listOf("Herd" to true, "Screen" to false), badge = "1", onSelect = {})
            }
        }
    }

    @Test fun herdOffline() = paparazzi.snapshot {
        Frame {
            HerdScreen(
                "AKBARs-MacBook-Air.local", herd,
                problem = "Can't reach your Mac. Check that herdr-remote is running and Tailscale is on.",
                onOpen = {}, onPairAgain = {},
            )
        }
    }

    // The terminal is a WebView (xterm.js), which layoutlib can't draw: the
    // slot shows the sample screen as text to check the chrome around it.
    @Composable
    private fun FakeTerminal() {
        Text(terminal, style = Type.terminal, color = Ink.text, softWrap = false, modifier = Modifier.padding(8.dp))
    }

    @Test fun agentWatching() = paparazzi.snapshot {
        Frame {
            AgentContent(
                terminal = "t12", agent = herd.agents[0], problem = null, ctrl = false,
                status = TermStatus(state = "live", cols = 165, rows = 36, phoneCols = 52, phoneRows = 40),
                onBack = {}, onShowOnMac = {}, onKey = {},
            ) { FakeTerminal() }
        }
    }

    @Test fun agentDriving() = paparazzi.snapshot {
        Frame {
            AgentContent(
                terminal = "t12", agent = herd.agents[0], problem = null, ctrl = true,
                status = TermStatus(state = "reconnecting", cols = 52, rows = 40, phoneCols = 52, phoneRows = 40, driving = true),
                onBack = {}, onShowOnMac = {}, onKey = {},
            ) { FakeTerminal() }
        }
    }
}
