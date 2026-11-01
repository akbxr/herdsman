package dev.akbxr.herdrremote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog

/** A subject longer than this reads badly in git log and on forges. */
private const val SUBJECT_MAX = 72

/**
 * What to ask the agent in the terminal when it commits for you: all the
 * changes, or only the files picked.
 */
fun askToCommitText(files: List<ChangedFile>, picked: List<String>, more: Int): String {
    if (picked.size == files.size && more == 0) return "Commit the changes with a Conventional Commits message."
    val shown = picked.take(20).joinToString(", ")
    val rest = if (picked.size > 20) " and ${picked.size - 20} more" else ""
    return "Commit only these files, with a Conventional Commits message: $shown$rest."
}

/** The commit sheet as the app shows it (Paparazzi draws [CommitSheetContent] alone). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CommitSheet(onDismiss: () -> Unit, content: @Composable () -> Unit) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = Ink.pen,
        contentColor = Ink.text,
        dragHandle = { Box(Modifier.padding(top = 10.dp, bottom = 6.dp).width(36.dp).height(4.dp).clip(RoundedCornerShape(2.dp)).background(Ink.rule)) },
    ) {
        // The message box stays above the keyboard.
        Box(Modifier.navigationBarsPadding().imePadding()) { content() }
    }
}

/**
 * The commit sheet, stateless: the message (subject, blank line, body),
 * "Write it for me", asking the agent instead, what failed last time (a
 * hook's output, as git printed it), and the files with a box each.
 * [picked]: the files ticked. [ask]: the agent's name, when it can be asked
 * to commit; [askBlocked]: why not now.
 */
@Composable
fun CommitSheetContent(
    files: List<ChangedFile>,
    more: Int,
    branch: String,
    picked: Set<String>,
    onPick: (String, Boolean) -> Unit,
    onPickAll: (Boolean) -> Unit,
    message: String,
    onMessage: (String) -> Unit,
    onSuggest: () -> Unit,
    suggesting: Boolean,
    ask: String?,
    askBlocked: String?,
    onAsk: () -> Unit,
    busy: Boolean,
    error: String?,
    onCommit: () -> Unit,
    onCancel: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val count = files.count { it.path in picked }
    Column(modifier.fillMaxWidth()) {
        LazyColumn(Modifier.weight(1f, fill = false)) {
            item(key = "head") {
                Column(Modifier.padding(start = 24.dp, end = 24.dp, top = 8.dp)) {
                    Text("Commit", style = Type.title, color = Ink.text)
                    Text(
                        "$count of ${files.size + more} ${if (files.size + more == 1) "file" else "files"}" + (if (branch.isNotEmpty()) " on $branch" else "") +
                            " · your git config and hooks apply",
                        style = Type.small, color = Ink.dust,
                    )
                }
                Label("Message")
                MessageBox(message, onMessage, enabled = !busy, modifier = Modifier.padding(horizontal = 24.dp))
                Row(Modifier.fillMaxWidth().padding(start = 14.dp, end = 24.dp, top = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                    LinkButton(if (suggesting) "Writing…" else "Write it for me", enabled = !busy && !suggesting && count > 0, onClick = onSuggest)
                    if (ask != null) LinkButton("Ask $ask to commit", enabled = !busy && askBlocked == null, onClick = onAsk)
                    Spacer(Modifier.weight(1f))
                    val subject = message.lineSequence().firstOrNull().orEmpty().trim().length
                    if (subject > 0) Text("$subject/$SUBJECT_MAX", style = Type.small.copy(fontFamily = JetBrains, fontSize = 11.sp), color = if (subject > SUBJECT_MAX) Ink.yellow else Ink.dust)
                }
                askBlocked?.takeIf { ask != null }?.let {
                    Text(it, style = Type.small, color = Ink.dust, modifier = Modifier.padding(start = 24.dp, end = 24.dp, bottom = 2.dp))
                }
                error?.let { CommitError(it, Modifier.padding(start = 24.dp, end = 24.dp, top = 10.dp)) }
                Row(Modifier.fillMaxWidth().padding(start = 24.dp, end = 14.dp, top = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text("FILES", style = Type.tagSmall, color = Ink.dust.copy(alpha = 0.8f), modifier = Modifier.weight(1f))
                    val all = count == files.size
                    LinkButton(if (all) "None" else "All", enabled = !busy && files.isNotEmpty()) { onPickAll(!all) }
                }
            }
            items(files, key = { "f:" + it.path }) { f -> FileRow(f, f.path in picked, enabled = !busy) { onPick(f.path, it) } }
            if (more > 0) item(key = "more") {
                Text(
                    "$more more ${if (more == 1) "file isn't" else "files aren't"} listed and stay out of this commit.",
                    style = Type.small, color = Ink.dust, modifier = Modifier.padding(horizontal = 24.dp, vertical = 8.dp),
                )
            }
        }
        SheetFooter(
            primary = when {
                busy -> "Committing…"
                count == 1 -> "Commit 1 file"
                else -> "Commit $count files"
            },
            enabled = !busy && count > 0 && message.isNotBlank(),
            onPrimary = onCommit,
            onCancel = onCancel,
        )
    }
}

@Composable
private fun Label(text: String) {
    Text(
        text.uppercase(), style = Type.tagSmall, color = Ink.dust.copy(alpha = 0.8f),
        modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 16.dp, bottom = 8.dp),
    )
}

/** Several lines, in the mono face git log shows them in. */
@Composable
private fun MessageBox(value: String, onValue: (String) -> Unit, enabled: Boolean, modifier: Modifier = Modifier) {
    val style = Type.body.copy(fontFamily = JetBrains, fontSize = 14.sp, lineHeight = 20.sp)
    BasicTextField(
        value = value,
        onValueChange = onValue,
        enabled = enabled,
        textStyle = style.copy(color = Ink.text),
        cursorBrush = SolidColor(Ink.yellow),
        minLines = 4,
        maxLines = 10,
        keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.None, autoCorrectEnabled = true),
        modifier = modifier.fillMaxWidth(),
        decorationBox = { inner ->
            Box(
                Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(12.dp))
                    .background(Ink.night)
                    .border(1.dp, Ink.rule, RoundedCornerShape(12.dp))
                    .padding(horizontal = 14.dp, vertical = 12.dp),
            ) {
                if (value.isEmpty()) Text("fix: what changed\n\nWhy, in a line or two", style = style, color = Ink.dust.copy(alpha = 0.7f))
                inner()
            }
        },
    )
}

/** A small text action; dimmed while it can't run. */
@Composable
private fun LinkButton(text: String, enabled: Boolean, onClick: () -> Unit) {
    Box(
        Modifier
            .heightIn(min = 40.dp)
            .clip(RoundedCornerShape(10.dp))
            .clickable(enabled = enabled, role = Role.Button, onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 8.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(text, style = Type.label, color = if (enabled) Ink.blue else Ink.dust.copy(alpha = 0.6f))
    }
}

/** What git or a hook printed when the commit failed, as it printed it. */
@Composable
private fun CommitError(text: String, modifier: Modifier = Modifier) {
    Column(
        modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .background(Ink.red.copy(alpha = 0.12f))
            .padding(horizontal = 14.dp, vertical = 12.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(6.dp).clip(RoundedCornerShape(3.dp)).background(Ink.red))
            Spacer(Modifier.width(10.dp))
            Text("Not committed", style = Type.label, color = Ink.text)
        }
        Spacer(Modifier.height(6.dp))
        Box(Modifier.heightIn(max = 180.dp).verticalScroll(rememberScrollState())) {
            Text(text, style = Type.small.copy(fontFamily = JetBrains, fontSize = 11.5.sp, lineHeight = 16.sp), color = Ink.text)
        }
    }
}

@Composable
private fun FileRow(f: ChangedFile, checked: Boolean, enabled: Boolean, onChecked: (Boolean) -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = 52.dp)
            .clickable(enabled = enabled, role = Role.Checkbox) { onChecked(!checked) }
            .padding(start = 12.dp, end = 24.dp, top = 4.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Checkbox(
            checked = checked, onCheckedChange = null, enabled = enabled,
            colors = CheckboxDefaults.colors(checkedColor = Ink.text, uncheckedColor = Ink.dust, checkmarkColor = Ink.night),
            modifier = Modifier.padding(horizontal = 12.dp),
        )
        StatusBadge(f.status)
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Text(f.path.substringAfterLast('/'), style = Type.label.copy(fontFamily = JetBrains, fontSize = 13.sp), color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val dir = f.path.substringBeforeLast('/', "")
            if (dir.isNotEmpty()) Text(dir, style = Type.small.copy(fontSize = 12.sp), color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        if (f.isNew) Text("new", style = Type.small, color = Ink.mint, modifier = Modifier.padding(start = 8.dp))
        if (f.status == "D") Text("deleted", style = Type.small, color = Ink.red, modifier = Modifier.padding(start = 8.dp))
    }
}

/** Asks before a file's changes go: it names the file and says there's no undo. */
@Composable
fun DiscardDialog(file: ChangedFile, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    Dialog(onDismissRequest = onDismiss) { DiscardConfirm(file, onConfirm, onDismiss) }
}

/** What [DiscardDialog] shows (also for snapshots). */
@Composable
fun DiscardConfirm(file: ChangedFile, onConfirm: () -> Unit, onDismiss: () -> Unit, modifier: Modifier = Modifier) {
    val name = file.path.substringAfterLast('/')
    val (title, text, confirm) = when {
        file.isNew -> Triple(
            "Delete $name?",
            "${file.path} is new since the last commit, so discarding it deletes the file from the Mac. This can't be undone.",
            "Delete",
        )
        file.status == "D" -> Triple(
            "Bring back $name?",
            "${file.path} comes back as the last commit has it, and its deletion is discarded. This can't be undone.",
            "Discard",
        )
        else -> Triple(
            "Discard changes to $name?",
            "${file.path} goes back to the last commit; its changes, staged or not, are lost. This can't be undone.",
            "Discard",
        )
    }
    Column(
        modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(28.dp))
            .background(Ink.pen)
            .padding(start = 24.dp, end = 16.dp, top = 24.dp, bottom = 12.dp),
    ) {
        Text(title, style = Type.title, color = Ink.text, modifier = Modifier.padding(end = 8.dp))
        Spacer(Modifier.height(12.dp))
        Text(text, style = Type.body, color = Ink.dust, modifier = Modifier.padding(end = 8.dp))
        Spacer(Modifier.height(16.dp))
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End, verticalAlignment = Alignment.CenterVertically) {
            QuietButton("Keep it", color = Ink.text, onClick = onDismiss)
            Spacer(Modifier.width(4.dp))
            QuietButton(confirm, color = Ink.red, onClick = onConfirm)
        }
    }
}
