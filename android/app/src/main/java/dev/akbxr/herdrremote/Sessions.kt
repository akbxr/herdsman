package dev.akbxr.herdrremote

import androidx.compose.foundation.border
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
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/**
 * An earlier conversation on the Mac an agent can pick up again
 * (GET /api/sessions). [running]: the terminal it runs in now, to open
 * rather than resume twice.
 */
data class EarlierSession(
    val agent: String,
    val id: String,
    val title: String,
    val cwd: String,
    val time: Long,
    val messages: Int,
    val atLeast: Boolean = false,
    val running: String? = null,
)

/** [note]: why there are none, from the machine (e.g. its agents keep no sessions there). */
data class EarlierSessions(val home: String, val list: List<EarlierSession>, val note: String = "")

/** When something last happened, for people: "5 min ago", "yesterday", "12 Oct". */
fun agoText(ms: Long, now: Long, zone: ZoneId = ZoneId.systemDefault()): String {
    val min = (now - ms) / 60_000
    return when {
        min < 1 -> "just now"
        min < 60 -> "$min min ago"
        min < 24 * 60 -> "${min / 60} h ago"
        min < 48 * 60 -> "yesterday"
        min < 7 * 24 * 60 -> "${min / (24 * 60)} days ago"
        else -> {
            val at = Instant.ofEpochMilli(ms).atZone(zone)
            val sameYear = at.year == Instant.ofEpochMilli(now).atZone(zone).year
            at.format(DateTimeFormatter.ofPattern(if (sameYear) "d MMM" else "d MMM yyyy", Locale.ENGLISH))
        }
    }
}

/** "1 message", "14 messages", "14+ messages" (not counted to the end yet). */
fun messagesText(n: Int, atLeast: Boolean): String =
    "$n${if (atLeast) "+" else ""} message${if (n == 1 && !atLeast) "" else "s"}"

/**
 * Picks up a session: opens the terminal it runs in, or asks the Mac to
 * resume it in a new tab; [onTerminal] gets the terminal either way.
 */
suspend fun resumeOrOpen(api: Api, s: EarlierSession, onTerminal: (String) -> Unit, machine: String = "") {
    s.running?.let { onTerminal(it); return }
    try {
        onTerminal(api.resume(s.agent, s.id, machine))
    } catch (e: ApiError) {
        // It started running somewhere since the list came: open that one.
        e.terminal?.let { onTerminal(it); return }
        throw e
    }
}

/**
 * Earlier sessions anywhere on the Mac, newest first: tap one to pick it
 * up again in a new tab, or to open the terminal it runs in now.
 */
@Composable
fun SessionsScreen(api: Api, host: String, onBack: () -> Unit, onTerminal: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    var sessions by remember { mutableStateOf<EarlierSessions?>(null) }
    var loadProblem by remember { mutableStateOf<String?>(null) }
    var attempt by remember { mutableIntStateOf(0) }
    var resuming by rememberSaveable { mutableStateOf<String?>(null) }
    var problem by remember { mutableStateOf<String?>(null) }
    LaunchedEffect(api, attempt) {
        loadProblem = null
        runCatching { api.sessions() }
            .onSuccess { sessions = it }
            .onFailure { loadProblem = it.advice() }
    }
    SessionsList(
        host = host,
        sessions = sessions,
        now = System.currentTimeMillis(),
        loadProblem = loadProblem,
        onRetry = { attempt++ },
        resuming = resuming,
        problem = problem,
        onBack = onBack,
        onSession = pick@{ s ->
            if (resuming != null) return@pick
            resuming = s.id
            problem = null
            scope.launch {
                runCatching { resumeOrOpen(api, s, onTerminal) }.onFailure { problem = it.advice() }
                resuming = null
            }
        },
    )
}

/** The sessions screen as shown, without the Mac (also for snapshots). */
@Composable
fun SessionsList(
    host: String,
    sessions: EarlierSessions?,
    now: Long,
    loadProblem: String?,
    onRetry: () -> Unit,
    resuming: String?,
    problem: String?,
    onBack: () -> Unit,
    onSession: (EarlierSession) -> Unit,
) {
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 32.dp)) {
        item {
            Row(Modifier.fillMaxWidth().padding(start = 4.dp, end = 12.dp, top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier.size(48.dp).clip(RoundedCornerShape(12.dp)).clickable(role = Role.Button, onClick = onBack),
                    contentAlignment = Alignment.Center,
                ) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back to the herd", tint = Ink.text) }
                Text(
                    "Earlier sessions on ${host.removeSuffix(".local").ifBlank { "your Mac" }}",
                    style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            Column(Modifier.padding(horizontal = 24.dp)) {
                Spacer(Modifier.height(4.dp))
                Text("Resume", style = Type.headline, color = Ink.text)
                Spacer(Modifier.height(6.dp))
                Text(
                    "Pick up a tego or Claude Code conversation where it stopped. It opens in a new tab in its folder, and here.",
                    style = Type.body, color = Ink.dust,
                )
                Spacer(Modifier.height(16.dp))
                loadProblem?.let { Notice(it, action = "Try again" to onRetry); Spacer(Modifier.height(16.dp)) }
                problem?.let { Notice(it); Spacer(Modifier.height(16.dp)) }
            }
        }
        when {
            sessions == null -> item { SessionSkeletons(5) }
            sessions.list.isEmpty() -> item {
                Text(
                    sessions.note.ifBlank { "No earlier sessions on the Mac yet. Start a task, and it shows up here once you've said something in it." },
                    style = Type.small, color = Ink.dust, modifier = Modifier.padding(horizontal = 24.dp, vertical = 12.dp),
                )
            }
            else -> items(sessions.list, key = { it.agent + ":" + it.id }) { s ->
                SessionRow(s, now, folder = folderTitle(s.cwd, sessions.home), resuming = resuming == s.id, onClick = { onSession(s) })
            }
        }
    }
}

/** Rows still coming. */
@Composable
fun SessionSkeletons(n: Int, modifier: Modifier = Modifier) {
    Column(modifier.padding(horizontal = 24.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(20.dp)) {
        repeat(n) {
            Column(verticalArrangement = Arrangement.spacedBy(7.dp)) {
                SkeletonBar(Modifier.fillMaxWidth(0.75f), height = 18)
                SkeletonBar(Modifier.fillMaxWidth(0.45f), height = 12)
            }
        }
    }
}

/**
 * A session: its title, then its agent, folder (when the list spans
 * folders), when and how long. One running now says where, and opens.
 */
@Composable
fun SessionRow(s: EarlierSession, now: Long, folder: String?, resuming: Boolean, onClick: () -> Unit) {
    val action = when {
        resuming -> if (s.running != null) "Opening…" else "Resuming…"
        s.running != null -> "Open"
        else -> null
    }
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = 64.dp)
            .clickable(role = Role.Button, onClickLabel = if (s.running != null) "Open ${s.running}" else "Resume", onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                s.title.ifBlank { "Untitled" }, style = Type.title.copy(fontSize = Type.body.fontSize * 1.1f),
                color = Ink.text, maxLines = 2, overflow = TextOverflow.Ellipsis,
            )
            Spacer(Modifier.height(5.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                AgentTag(agentTitle(s.agent))
                Spacer(Modifier.width(8.dp))
                val meta = buildList {
                    if (s.running != null) add("running in ${s.running}") // now: no time
                    folder?.let { add(it) }
                    if (s.running == null) add(agoText(s.time, now))
                    add(messagesText(s.messages, s.atLeast))
                }
                Text(
                    meta.joinToString("  ·  "), style = Type.small,
                    color = if (s.running != null) Ink.blue else Ink.dust,
                    maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (action != null) {
            Spacer(Modifier.width(12.dp))
            Text(action, style = Type.label, color = if (resuming) Ink.dust else Ink.blue, maxLines = 1)
        }
    }
}

/** Which agent: small, quiet. */
@Composable
private fun AgentTag(name: String) {
    Box(
        Modifier
            .clip(RoundedCornerShape(6.dp))
            .border(1.dp, Ink.rule, RoundedCornerShape(6.dp))
            .padding(horizontal = 6.dp, vertical = 1.dp),
    ) {
        Text(name, style = Type.tagSmall, color = Ink.text.copy(alpha = 0.85f), maxLines = 1)
    }
}
