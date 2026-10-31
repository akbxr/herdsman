package dev.akbxr.herdrremote

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

/** What a new tab or pane runs. null argv: the machine's shell. */
data class Program(val label: String, val argv: List<String>?)

val PROGRAMS = listOf(
    Program("Shell", null),
    Program("tego", listOf("tego")),
    Program("Claude Code", listOf("claude")),
    Program("Codex", listOf("codex")),
    Program("omp", listOf("omp")),
    Program("Pi", listOf("pi")),
)

/** One row of a sheet: an action, said as what it does. */
data class SheetAction(val label: String, val detail: String? = null, val danger: Boolean = false, val run: () -> Unit)

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ActionSheet(title: String, subtitle: String? = null, actions: List<SheetAction>, onDismiss: () -> Unit, extra: @Composable () -> Unit = {}) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = Ink.pen,
        contentColor = Ink.text,
        dragHandle = { Box(Modifier.padding(top = 10.dp, bottom = 6.dp).width(36.dp).height(4.dp).clip(RoundedCornerShape(2.dp)).background(Ink.rule)) },
    ) {
        Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).navigationBarsPadding().padding(bottom = 12.dp)) {
            Column(Modifier.padding(horizontal = 24.dp, vertical = 8.dp)) {
                Text(title, style = Type.title, color = Ink.text)
                subtitle?.let { Text(it, style = Type.small, color = Ink.dust) }
            }
            actions.forEach { a ->
                Row(
                    Modifier
                        .fillMaxWidth()
                        .heightIn(min = 52.dp)
                        .clickable(role = Role.Button) { a.run() }
                        .padding(horizontal = 24.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text(a.label, style = Type.body.copy(fontWeight = Type.label.fontWeight), color = if (a.danger) Ink.red else Ink.text)
                        a.detail?.let { Text(it, style = Type.small, color = Ink.dust) }
                    }
                }
            }
            extra()
        }
    }
}

/** Picks what to run: a known program, or a command of your own. */
@Composable
fun ProgramSheet(title: String, subtitle: String, onPick: (Program) -> Unit, onDismiss: () -> Unit) {
    var custom by remember { mutableStateOf("") }
    ActionSheet(
        title = title,
        subtitle = subtitle,
        actions = PROGRAMS.map { p -> SheetAction(p.label, p.argv?.joinToString(" ") ?: "The Mac's shell") { onPick(p) } },
        onDismiss = onDismiss,
    ) {
        Column(Modifier.padding(horizontal = 24.dp, vertical = 8.dp)) {
            Field("Or a command", custom, { custom = it }, "npm run dev", style = Type.body.copy(fontFamily = JetBrains), onDone = {
                custom.trim().takeIf { it.isNotEmpty() }?.let { onPick(Program(it, it.split(Regex("\\s+")))) }
            })
            Spacer(Modifier.height(10.dp))
            PrimaryButton("Run it", enabled = custom.isNotBlank(), modifier = Modifier.fillMaxWidth()) {
                onPick(Program(custom.trim(), custom.trim().split(Regex("\\s+"))))
            }
        }
    }
}

@Composable
fun ConfirmDialog(title: String, text: String, confirm: String, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Ink.pen,
        title = { Text(title, style = Type.title, color = Ink.text) },
        text = { Text(text, style = Type.body, color = Ink.dust) },
        confirmButton = { QuietButton(confirm, color = Ink.red, onClick = onConfirm) },
        dismissButton = { QuietButton("Keep it", color = Ink.text, onClick = onDismiss) },
    )
}

@Composable
fun RenameDialog(current: String, onRename: (String) -> Unit, onDismiss: () -> Unit) {
    var name by remember { mutableStateOf(current) }
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Ink.pen,
        title = { Text("Rename the tab", style = Type.title, color = Ink.text) },
        text = { Field("Name", name, { name = it }, "Empty: its number again", onDone = { onRename(name.trim()) }) },
        confirmButton = { QuietButton("Rename", color = Ink.text) { onRename(name.trim()) } },
        dismissButton = { QuietButton("Cancel", onClick = onDismiss) },
    )
}

/**
 * Everything you can do to an agent's pane and tab, from a sheet. Steps that
 * need more (what to run in a split, confirming a close, a new name) open
 * their own sheet or dialog. [onClosed] runs after its pane or tab is gone.
 */
@Composable
fun PaneActions(
    api: Api,
    agent: Agent,
    scope: CoroutineScope,  // the caller's: the sheet is gone by the time the Mac answers
    onDismiss: () -> Unit,
    onProblem: (String) -> Unit,
    onClosed: () -> Unit = {},
) {
    var step by remember { mutableStateOf("menu") }  // menu, split-right, split-down, close-pane, close-tab, rename
    fun act(done: () -> Unit = {}, block: suspend () -> Unit) {
        onDismiss()
        scope.launch { runCatching { block() }.onSuccess { done() }.onFailure { onProblem(it.advice()) } }
    }
    val where = "${agent.title} · Tab ${agent.tabName} · ${agent.terminal}"
    when (step) {
        "menu" -> ActionSheet(
            title = agent.title, subtitle = where, onDismiss = onDismiss,
            actions = listOf(
                SheetAction("Show on Mac", "Bring this tab up in the Herdr window") { act { api.showTab(agent.tabId) } },
                SheetAction("Split right", "A new pane beside this one") { step = "split-right" },
                SheetAction("Split down", "A new pane under this one") { step = "split-down" },
                SheetAction("Zoom", "Show only this pane in its tab, or all again") { act { api.zoom(agent.pane) } },
                SheetAction("Rename tab") { step = "rename" },
                SheetAction("Close pane", "Ends ${agent.title} in it", danger = true) { step = "close-pane" },
                SheetAction("Close tab", "Ends every terminal in Tab ${agent.tabName}", danger = true) { step = "close-tab" },
            ),
        )
        "split-right", "split-down" -> ProgramSheet(
            title = if (step == "split-right") "Split right" else "Split down",
            subtitle = "In the folder ${agent.title} is in.",
            onPick = { p -> act { api.split(agent.pane, if (step == "split-right") "right" else "down", p.argv) } },
            onDismiss = onDismiss,
        )
        "rename" -> RenameDialog(agent.tabName.takeUnless { it.matches(Regex("\\d+")) } ?: "", { name -> act { api.renameTab(agent.tabId, name) } }, onDismiss)
        "close-pane" -> ConfirmDialog(
            "Close this pane?", "${agent.title} (${agent.terminal}) ends, and its pane leaves Tab ${agent.tabName}.", "Close pane",
            onConfirm = { act(onClosed) { api.closePane(agent.pane) } }, onDismiss = onDismiss,
        )
        "close-tab" -> ConfirmDialog(
            "Close Tab ${agent.tabName}?", "Every terminal in it ends, ${agent.title} included.", "Close tab",
            onConfirm = { act(onClosed) { api.closeTab(agent.tabId) } }, onDismiss = onDismiss,
        )
    }
}

/** Starts a new tab running the program you pick. */
@Composable
fun NewTabSheet(api: Api, scope: CoroutineScope, onDismiss: () -> Unit, onProblem: (String) -> Unit) {
    ProgramSheet(
        title = "New tab",
        subtitle = "In the folder of the terminal focused on the Mac.",
        onPick = { p ->
            onDismiss()
            scope.launch { runCatching { api.newTab(p.argv) }.onFailure { onProblem(it.advice()) } }
        },
        onDismiss = onDismiss,
    )
}

