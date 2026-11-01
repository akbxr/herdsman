package dev.akbxr.herdrremote

import android.content.Context
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import org.json.JSONArray

/** Quick replies: one tap puts them in the message box. Kept on the phone. */
class Snippets(context: Context) {
    private val prefs = context.getSharedPreferences("snippets", Context.MODE_PRIVATE)

    fun load(): List<String> = prefs.getString("list", null)?.let { raw ->
        runCatching { JSONArray(raw).let { a -> (0 until a.length()).map { a.getString(it) } } }.getOrNull()
    } ?: DEFAULT

    fun save(list: List<String>) = prefs.edit().putString("list", JSONArray(list).toString()).apply()

    companion object {
        val DEFAULT = listOf("continue", "yes", "/compact", "Run the tests", "Commit this", "Explain what you changed")
    }
}

/** What the last word of the message asks for: commands ("/…") or files ("@…"). */
sealed interface Wanted {
    data class Commands(val query: String) : Wanted
    data class Files(val query: String) : Wanted
}

fun wanted(message: String): Wanted? {
    if (message.startsWith("/") && message.none { it.isWhitespace() }) return Wanted.Commands(message)
    val last = message.substringAfterLast(' ').substringAfterLast('\n')
    if (last.startsWith("@")) return Wanted.Files(last.drop(1))
    return null
}

/** The commands for a "/…" query: names starting with it, then names or help holding it. */
fun matchCommands(all: List<Command>, query: String): List<Command> {
    val q = query.lowercase()
    val starts = all.filter { it.name.lowercase().startsWith(q) }
    // Past the slash: in the name ("/msg" finds "/skill:commit-message"),
    // or, for a word, in what it does.
    val word = q.drop(1)
    val holds = all.filter {
        it !in starts && word.isNotEmpty() &&
            (it.name.lowercase().contains(word) || (word.length >= 4 && it.help.lowercase().contains(word)))
    }
    return (starts + holds).take(30)
}

/** The message with its last word ("@que") replaced by the file. */
fun withFile(message: String, path: String): String {
    val cut = maxOf(message.lastIndexOf(' '), message.lastIndexOf('\n')) + 1
    return message.substring(0, cut) + "@" + path + " "
}

/** The suggestions over the message box: a name and what it is. */
@Composable
fun Suggestions(rows: List<Pair<String, String>>, onPick: (Int) -> Unit, modifier: Modifier = Modifier) {
    LazyColumn(
        modifier
            .fillMaxWidth()
            .heightIn(max = 232.dp)
            .clip(RoundedCornerShape(12.dp))
            .background(Ink.pen)
            .border(1.dp, Ink.rule, RoundedCornerShape(12.dp)),
    ) {
        itemsIndexed(rows) { i, (name, help) ->
            Column(
                Modifier
                    .fillMaxWidth()
                    .clickable(role = Role.Button) { onPick(i) }
                    .padding(horizontal = 14.dp, vertical = 9.dp),
            ) {
                Text(name, style = Type.label.copy(fontFamily = JetBrains, fontSize = 13.sp), color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (help.isNotEmpty()) Text(help, style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

/**
 * The row under the message box in the chat: Esc (stops the agent), then the
 * quick replies; hold one to change or remove it, + adds one.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun SnippetRow(
    snippets: List<String>, onUse: (String) -> Unit, onEdit: (Int) -> Unit, onAdd: () -> Unit, onEsc: () -> Unit,
    leading: @Composable () -> Unit = {},  // before Esc: the model chip (Models.kt)
) {
    Row(
        Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 8.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        leading()
        Chip("Esc", mono = true, onClick = onEsc)
        snippets.forEachIndexed { i, s -> Chip(s, onClick = { onUse(s) }, onLongClick = { onEdit(i) }) }
        Chip("+", onClick = onAdd, label = "Add a quick reply")
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun Chip(text: String, mono: Boolean = false, label: String? = null, onLongClick: (() -> Unit)? = null, onClick: () -> Unit) {
    Box(
        Modifier
            .heightIn(min = 42.dp)
            .clip(RoundedCornerShape(10.dp))
            .background(Ink.pen)
            .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
            .combinedClickable(role = Role.Button, onClickLabel = label, onLongClickLabel = onLongClick?.let { "Change or remove" }, onLongClick = onLongClick, onClick = onClick)
            .padding(horizontal = 13.dp, vertical = 9.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(text, style = if (mono) Type.label.copy(fontFamily = JetBrains) else Type.label, color = Ink.text, maxLines = 1)
    }
}

/** Adds a quick reply ([onDelete] null) or changes one. */
@Composable
fun SnippetDialog(initial: String, onSave: (String) -> Unit, onDelete: (() -> Unit)?, onDismiss: () -> Unit) {
    var text by remember { mutableStateOf(initial) }
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = Ink.pen,
        title = { Text(if (onDelete == null) "New quick reply" else "Quick reply", style = Type.title, color = Ink.text) },
        text = { Field("Text", text, { text = it }, "continue, /compact, Run the tests…", onDone = { if (text.isNotBlank()) onSave(text.trim()) }) },
        confirmButton = { QuietButton("Save", color = Ink.text) { if (text.isNotBlank()) onSave(text.trim()) } },
        dismissButton = {
            Row {
                if (onDelete != null) QuietButton("Remove", color = Ink.red, onClick = onDelete)
                QuietButton("Cancel", onClick = onDismiss)
            }
        },
    )
}
