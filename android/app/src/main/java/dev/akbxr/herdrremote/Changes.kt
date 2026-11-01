package dev.akbxr.herdrremote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import org.json.JSONObject

data class ChangedFile(
    val path: String,
    val status: String,     // M, A, D, ? (new), T
    val added: Int,
    val deleted: Int,
    val binary: Boolean,
    val patch: String,
    val truncated: Boolean,
)

/** The uncommitted changes in the agent's folder; [files] null when unchanged since [version]. */
data class Changes(
    val repo: Boolean,
    val root: String,
    val branch: String,
    val files: List<ChangedFile>?,
    val more: Int,
    val version: String,
) {
    companion object {
        fun from(o: JSONObject): Changes {
            val files = if (o.optBoolean("same")) null else o.optJSONArray("files")?.let { a ->
                (0 until a.length()).map { i ->
                    a.getJSONObject(i).let {
                        ChangedFile(
                            it.optString("path"), it.optString("status"), it.optInt("added"), it.optInt("deleted"),
                            it.optBoolean("binary"), it.optString("patch"), it.optBoolean("truncated"),
                        )
                    }
                }
            } ?: emptyList()
            return Changes(o.optBoolean("repo"), o.optString("root"), o.optString("branch"), files, o.optInt("more"), o.optString("version"))
        }
    }
}

private const val SHOWN_LINES = 600   // of one file's diff, on a phone

private val Added = Color(0xFF9ECE6A)
private val Removed = Color(0xFFF7768E)

/**
 * What the agent changed, file by file: a summary line, then each file with
 * its counts; tap one for its diff (green added, red removed, scrolling
 * sideways rather than wrapping code).
 */
@Composable
fun ChangesView(changes: Changes?, files: List<ChangedFile>, modifier: Modifier = Modifier) {
    when {
        changes == null -> SkeletonList(modifier)
        !changes.repo -> Empty("This folder isn't a git repository, so there are no changes to show.", modifier)
        files.isEmpty() -> Empty("Nothing changed since the last commit" + (if (changes.branch.isNotEmpty()) " on ${changes.branch}." else "."), modifier)
        else -> LazyColumn(
            modifier.fillMaxSize(),
            contentPadding = PaddingValues(start = 12.dp, end = 12.dp, top = 10.dp, bottom = 16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            item(key = "summary") {
                val added = files.sumOf { it.added }
                val deleted = files.sumOf { it.deleted }
                Row(Modifier.padding(start = 4.dp, bottom = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        "${files.size + changes.more} ${if (files.size + changes.more == 1) "file" else "files"}" +
                            (if (changes.branch.isNotEmpty()) " on ${changes.branch}" else ""),
                        style = Type.label, color = Ink.text,
                    )
                    Spacer(Modifier.width(10.dp))
                    Text("+$added", style = Type.label.copy(fontFamily = JetBrains), color = Added)
                    Spacer(Modifier.width(6.dp))
                    Text("−$deleted", style = Type.label.copy(fontFamily = JetBrains), color = Removed)
                }
            }
            items(files, key = { it.path }) { FileCard(it) }
            if (changes.more > 0) item(key = "more") {
                Text("${changes.more} more files not shown.", style = Type.small, color = Ink.dust, modifier = Modifier.padding(4.dp))
            }
        }
    }
}

@Composable
private fun Empty(text: String, modifier: Modifier) {
    Box(modifier.fillMaxSize().padding(28.dp), contentAlignment = Alignment.Center) {
        Text(text, style = Type.small, color = Ink.dust)
    }
}

@Composable
private fun FileCard(f: ChangedFile) {
    var open by rememberSaveable(f.path) { mutableStateOf(false) }
    val canOpen = f.patch.isNotEmpty()
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(10.dp))
            .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
            .then(if (canOpen) Modifier.clickable(role = Role.Button, onClickLabel = if (open) "Fold the diff" else "Show the diff") { open = !open } else Modifier),
    ) {
        Row(Modifier.padding(horizontal = 12.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
            StatusBadge(f.status)
            Spacer(Modifier.width(10.dp))
            val dir = f.path.substringBeforeLast('/', "")
            Column(Modifier.weight(1f)) {
                Text(f.path.substringAfterLast('/'), style = Type.label.copy(fontFamily = JetBrains, fontSize = 13.sp), color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (dir.isNotEmpty()) Text(dir, style = Type.small.copy(fontSize = 12.sp), color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Spacer(Modifier.width(8.dp))
            when {
                f.binary -> Text("binary", style = Type.small, color = Ink.dust)
                else -> {
                    if (f.added > 0) Text("+${f.added}", style = Type.small.copy(fontFamily = JetBrains), color = Added)
                    if (f.deleted > 0) Text("  −${f.deleted}", style = Type.small.copy(fontFamily = JetBrains), color = Removed)
                }
            }
        }
        if (open) {
            // As wide as the longest line, and at least the card: the stripes reach the edge.
            BoxWithConstraints(Modifier.fillMaxWidth().background(TermBg)) {
                val card = maxWidth
                Box(Modifier.horizontalScroll(rememberScrollState())) {
                    Column(Modifier.width(IntrinsicSize.Max).widthIn(min = card).padding(vertical = 6.dp)) {
                        val lines = f.patch.lines()
                        lines.take(SHOWN_LINES).forEach { DiffLine(it) }
                        if (f.truncated || lines.size > SHOWN_LINES) {
                            Text("  … the rest is on the Mac", style = Type.small, color = Ink.dust, modifier = Modifier.padding(6.dp))
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun StatusBadge(status: String) {
    val (label, color) = when (status) {
        "M" -> "M" to Ink.blue
        "A", "?" -> "A" to Added
        "D" -> "D" to Removed
        else -> status to Ink.dust
    }
    Box(
        Modifier.size(22.dp).clip(RoundedCornerShape(6.dp)).background(color.copy(alpha = 0.16f)),
        contentAlignment = Alignment.Center,
    ) { Text(label, style = Type.label.copy(fontFamily = JetBrains, fontSize = 12.sp), color = color) }
}

@Composable
private fun DiffLine(line: String) {
    val style = Type.small.copy(fontFamily = JetBrains, fontSize = 11.5.sp, lineHeight = 16.sp)
    when {
        line.startsWith("@@") -> Text(line, style = style, color = Ink.blue.copy(alpha = 0.8f), softWrap = false,
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 4.dp))
        line.startsWith("+") -> Text(line, style = style, color = Added, softWrap = false,
            modifier = Modifier.fillMaxWidth().background(Added.copy(alpha = 0.10f)).padding(horizontal = 10.dp))
        line.startsWith("-") -> Text(line, style = style, color = Removed, softWrap = false,
            modifier = Modifier.fillMaxWidth().background(Removed.copy(alpha = 0.10f)).padding(horizontal = 10.dp))
        line.startsWith("\\") -> Text(line, style = style, color = Ink.dust, softWrap = false, modifier = Modifier.padding(horizontal = 10.dp))
        else -> Text(line, style = style, color = Ink.text.copy(alpha = 0.75f), softWrap = false, modifier = Modifier.padding(horizontal = 10.dp))
    }
}
