package dev.akbxr.herdsman

import androidx.compose.animation.animateContentSize
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
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay
import org.json.JSONObject

/** One item of the agent's todo list. [status]: pending, in_progress, completed. */
data class WorkTodo(val text: String, val status: String, val phase: String = "")

/**
 * A subagent the agent started. [state]: running, done, failed, stopped,
 * unknown (it never reported: its session ended). [started]/[ended]: unix ms, 0 unknown.
 */
data class WorkAgent(
    val name: String,
    val state: String,
    val id: String = "",
    val kind: String = "",
    val task: String = "",
    val why: String = "",
    val report: String = "",
    val background: Boolean = false,
    val started: Long = 0,
    val ended: Long = 0,
)

/** What the agent works through, from its transcript: its todo list and subagents. */
data class ChatWork(val todos: List<WorkTodo> = emptyList(), val agents: List<WorkAgent> = emptyList()) {
    val running get() = agents.count { it.state == "running" }
    val done get() = todos.count { it.status == "completed" }
    /** What it works on now: the first in progress, else the first not done. */
    val current get() = todos.firstOrNull { it.status == "in_progress" } ?: todos.firstOrNull { it.status != "completed" }
    val isEmpty get() = todos.isEmpty() && agents.isEmpty()
    /** Something still to do: a todo not done, or a subagent running. */
    val live get() = running > 0 || todos.any { it.status != "completed" }

    /** What the bar above the message box shows while the agent is in [state]: nothing once all is done. */
    fun shown(state: String?): ChatWork? = seenWhile(state).takeIf { it.live }

    /**
     * As it stands while the agent is in [state]: a subagent of a task call
     * (not a background one) can't still run once the agent stopped working;
     * it was interrupted.
     */
    fun seenWhile(state: String?): ChatWork =
        if (state == null || state == "working" || state == "blocked") this
        else copy(agents = agents.map { if (it.state == "running" && !it.background) it.copy(state = "stopped", why = "the agent stopped before it ended") else it })

    companion object {
        fun from(o: JSONObject): ChatWork {
            val todos = o.optJSONArray("todos")?.let { a ->
                (0 until a.length()).map { i -> a.getJSONObject(i).let { WorkTodo(it.optString("text"), it.optString("status"), it.optString("phase")) } }
            } ?: emptyList()
            val agents = o.optJSONArray("agents")?.let { a ->
                (0 until a.length()).map { i ->
                    a.getJSONObject(i).let {
                        WorkAgent(
                            name = it.optString("name"), state = it.optString("state"), id = it.optString("id"),
                            kind = it.optString("kind"), task = it.optString("task"), why = it.optString("why"),
                            report = it.optString("report"), background = it.optBoolean("background"),
                            started = it.optLong("started"), ended = it.optLong("ended"),
                        )
                    }
                }
            } ?: emptyList()
            return ChatWork(todos, agents)
        }
    }
}

/** "4s", "12m", "1h7m". */
fun shortDuration(ms: Long): String {
    val s = (ms / 1000).coerceAtLeast(0)
    return when {
        s < 60 -> "${s}s"
        s < 3600 -> "${s / 60}m"
        else -> "${s / 3600}h${(s % 3600) / 60}m"
    }
}

/**
 * One line above the message box: the todo list's count and what it works
 * on, and the subagents running. Tap: [onOpen] shows all of it. Shown only
 * while there is something left ([ChatWork.shown]).
 */
@Composable
fun WorkBar(work: ChatWork, onOpen: () -> Unit, modifier: Modifier = Modifier) {
    val live = work.running > 0 || work.todos.any { it.status != "completed" }
    Row(
        modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(10.dp))
            .border(1.dp, if (live) Ink.blue.copy(alpha = 0.35f) else Ink.rule, RoundedCornerShape(10.dp))
            .clickable(role = Role.Button, onClickLabel = "Show the todo list and subagents", onClick = onOpen)
            .heightIn(min = 40.dp)
            .padding(horizontal = 12.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (work.todos.isNotEmpty()) {
            Text("Todo ${work.done}/${work.todos.size}", style = Type.label, color = if (live) Ink.text else Ink.dust)
            val now = work.current
            if (now != null) {
                Spacer(Modifier.width(8.dp))
                if (now.status == "in_progress") ShimmerText(now.text, Type.small, base = Ink.blue, light = Ink.text, modifier = Modifier.weight(1f))
                else Text(now.text, style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
            } else {
                Spacer(Modifier.weight(1f))
            }
        } else {
            Text("Subagents", style = Type.label, color = if (live) Ink.text else Ink.dust, modifier = Modifier.weight(1f))
        }
        if (work.agents.isNotEmpty()) {
            Spacer(Modifier.width(10.dp))
            if (work.running > 0) {
                PulseDot(Ink.blue)
                Spacer(Modifier.width(6.dp))
                Text("${work.running} running", style = Type.label, color = Ink.blue)
            } else {
                Text(if (work.agents.size == 1) "1 subagent" else "${work.agents.size} subagents", style = Type.label, color = Ink.dust)
            }
        }
        Spacer(Modifier.width(8.dp))
        Text("▴", style = Type.label, color = Ink.dust)
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun WorkSheet(work: ChatWork, agentName: String, onDismiss: () -> Unit) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = Ink.pen,
        contentColor = Ink.text,
        dragHandle = { Box(Modifier.padding(top = 10.dp, bottom = 6.dp).width(36.dp).height(4.dp).clip(RoundedCornerShape(2.dp)).background(Ink.rule)) },
    ) {
        // Running subagents' time goes on while it is open.
        val now by produceState(System.currentTimeMillis()) { while (true) { delay(1000); value = System.currentTimeMillis() } }
        WorkSheetContent(work, agentName, now, Modifier.navigationBarsPadding())
    }
}

/** What [WorkSheet] shows, without its window (also for snapshots). [now]: unix ms. */
@Composable
fun WorkSheetContent(work: ChatWork, agentName: String, now: Long, modifier: Modifier = Modifier) {
    Column(Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).then(modifier).padding(horizontal = 24.dp).padding(bottom = 20.dp)) {
        Text("$agentName's work", style = Type.title, color = Ink.text, modifier = Modifier.padding(top = 8.dp))
        if (work.todos.isNotEmpty()) {
            SectionHead("Todo", "${work.done} of ${work.todos.size} done")
            var phase: String? = null
            work.todos.forEach { t ->
                if (t.phase.isNotEmpty() && t.phase != phase) {
                    Text(t.phase, style = Type.small, color = Ink.dust, modifier = Modifier.padding(top = 10.dp, bottom = 2.dp))
                }
                phase = t.phase
                TodoRow(t)
            }
        }
        if (work.agents.isNotEmpty()) {
            val running = work.agents.filter { it.state == "running" }
            SectionHead("Subagents", if (running.isEmpty()) "none running" else "${running.size} running")
            // Running first, then the newest ended.
            (running + work.agents.filter { it.state != "running" }.reversed()).forEach { a -> AgentWorkRow(a, now) }
        }
    }
}

@Composable
private fun SectionHead(title: String, detail: String) {
    Row(Modifier.padding(top = 18.dp, bottom = 4.dp), verticalAlignment = Alignment.Bottom) {
        Text(title, style = Type.label, color = Ink.yellow)
        Spacer(Modifier.width(8.dp))
        Text(detail, style = Type.small, color = Ink.dust)
    }
}

@Composable
private fun TodoRow(t: WorkTodo) {
    Row(Modifier.fillMaxWidth().padding(vertical = 5.dp), verticalAlignment = Alignment.Top) {
        val (mark, color) = when (t.status) {
            "completed" -> "✓" to Ink.mint
            "in_progress" -> "▸" to Ink.blue
            else -> "○" to Ink.dust
        }
        Text(mark, style = Type.body, color = color, modifier = Modifier.width(22.dp))
        Text(
            t.text,
            style = Type.body,
            color = when (t.status) { "completed" -> Ink.dust; "in_progress" -> Ink.text; else -> Ink.text.copy(alpha = 0.85f) },
        )
    }
}

@Composable
private fun AgentWorkRow(a: WorkAgent, now: Long) {
    var open by rememberSaveable(a.name, a.id, a.started) { mutableStateOf(false) }
    val more = a.task.isNotEmpty() || a.report.isNotEmpty() || a.why.isNotEmpty()
    Column(
        Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(8.dp))
            .clickable(enabled = more, role = Role.Button, onClickLabel = if (open) "Fold" else "Show what it was asked and its report") { open = !open }
            .animateContentSize()
            .padding(vertical = 7.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.width(22.dp)) {
                when (a.state) {
                    "running" -> PulseDot(Ink.blue)
                    "done" -> Text("✓", style = Type.body, color = Ink.mint)
                    "failed" -> Text("✗", style = Type.body, color = Ink.red)
                    else -> Box(Modifier.size(8.dp).clip(CircleShape).background(Ink.dust))
                }
            }
            val title = buildString {
                append(a.name)
            }
            if (a.state == "running") ShimmerText(title, Type.body, base = Ink.text, light = Ink.blue, modifier = Modifier.weight(1f, fill = false))
            else Text(title, style = Type.body, color = if (a.state == "done") Ink.text else Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f, fill = false))
            if (a.id.isNotEmpty()) Text("  ${a.id}", style = Type.small.copy(fontFamily = JetBrains, fontSize = 12.sp), color = Ink.dust)
            Spacer(Modifier.weight(1f))
            val time = when {
                a.state == "running" && a.started > 0 -> shortDuration(now - a.started)
                a.ended > 0 && a.started > 0 -> shortDuration(a.ended - a.started)
                else -> ""
            }
            val tags = listOfNotNull(a.kind.ifEmpty { null }, if (a.background) "background" else null, time.ifEmpty { null })
            if (tags.isNotEmpty()) Text(tags.joinToString(" · "), style = Type.small, color = Ink.dust, maxLines = 1)
        }
        val line = when (a.state) {
            "done" -> a.report
            "running" -> a.task
            else -> listOf(stateWord(a.state), a.why).filter { it.isNotEmpty() }.joinToString(": ")
        }
        if (line.isNotEmpty() && !open) {
            Text(line, style = Type.small, color = if (a.state == "failed") Ink.red.copy(alpha = 0.85f) else Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(start = 22.dp, top = 2.dp))
        }
        if (open) {
            Column(Modifier.padding(start = 22.dp, top = 4.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                if (a.state != "running" && a.state != "done") Text(listOf(stateWord(a.state), a.why).filter { it.isNotEmpty() }.joinToString(": "), style = Type.small, color = if (a.state == "failed") Ink.red else Ink.dust)
                if (a.task.isNotEmpty()) Labeled("Asked", a.task)
                if (a.report.isNotEmpty()) Labeled("Report", a.report)
            }
        }
    }
}

private fun stateWord(state: String) = when (state) {
    "failed" -> "Failed"
    "stopped" -> "Stopped"
    "unknown" -> "No report"
    "done" -> "Done"
    else -> "Running"
}

@Composable
private fun Labeled(label: String, text: String) {
    Column {
        Text(label, style = Type.small, color = Ink.dust)
        Text(text, style = Type.small, color = Ink.text)
    }
}
