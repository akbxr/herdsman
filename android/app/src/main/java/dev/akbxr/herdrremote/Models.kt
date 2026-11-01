package dev.akbxr.herdrremote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
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
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.json.JSONObject

/** A model an agent can run: [id] is what its flag and /model take. */
data class ModelChoice(val id: String, val label: String, val group: String = "")

/** A thinking (effort, reasoning) level. */
data class LevelChoice(val id: String, val label: String, val detail: String = "")

/**
 * What an agent offers (GET /api/models). [thinkingName]: what it calls the
 * level (Thinking, Effort, Reasoning). [live]: a running session can switch.
 * [supported] false: not from the phone for that terminal (another
 * machine's, [why]): the picker hides.
 */
data class ModelChoices(
    val agent: String,
    val models: List<ModelChoice>,
    val thinking: List<LevelChoice>,
    val thinkingName: String = "Thinking",
    val live: Boolean = false,
    val note: String = "",
    val supported: Boolean = true,
    val why: String = "",
) {
    fun model(id: String?) = models.firstOrNull { it.id == id }
    fun level(id: String?) = thinking.firstOrNull { it.id == id }

    companion object {
        fun from(o: JSONObject) = ModelChoices(
            agent = o.optString("agent"),
            models = o.optJSONArray("models")?.let { a ->
                (0 until a.length()).map { a.getJSONObject(it).let { m -> ModelChoice(m.optString("id"), m.optString("label"), m.optString("group")) } }
            }.orEmpty(),
            thinking = o.optJSONArray("thinking")?.let { a ->
                (0 until a.length()).map { a.getJSONObject(it).let { l -> LevelChoice(l.optString("id"), l.optString("label"), l.optString("detail")) } }
            }.orEmpty(),
            thinkingName = o.optString("thinkingName").ifBlank { "Thinking" },
            live = o.optBoolean("live"),
            note = o.optString("note"),
            supported = o.optBoolean("supported", true),
            why = o.str("why"),
        )
    }
}

/** The model and level a running agent uses now ("": not known), from /api/chat. */
data class CurrentModel(val model: String = "", val label: String = "", val thinking: String = "") {
    val known get() = model.isNotEmpty() || label.isNotEmpty() || thinking.isNotEmpty()

    /** "Opus 5.5 · high". */
    fun summary(): String = listOf(label.ifEmpty { model }, thinking).filter { it.isNotEmpty() }.joinToString(" · ")

    companion object {
        fun from(o: JSONObject?) = CurrentModel(
            o?.optString("model").orEmpty(),
            o?.optString("modelLabel").orEmpty(),
            o?.optString("thinking").orEmpty(),
        )
    }
}

/** Agents whose model the phone can show and switch while they run. */
fun switchesLive(agent: String?) = agent == "tego" || agent == "claude"

/** Agents a new task can start with a model. */
fun takesModel(agent: String?) = agent == "tego" || agent == "claude" || agent == "codex"

/**
 * The model chip at the start of the chat's quick replies: what the agent
 * runs with; it opens the sheet. Dimmed while the agent works or the
 * switch is on its way.
 */
@Composable
fun ModelChip(current: CurrentModel, dim: Boolean, switching: Boolean, onClick: () -> Unit) {
    Row(
        Modifier
            .heightIn(min = 42.dp)
            .clip(RoundedCornerShape(10.dp))
            .background(Ink.pen)
            .border(1.dp, Ink.rule, RoundedCornerShape(10.dp))
            .clickable(role = Role.Button, onClickLabel = "Change the model or thinking level", onClick = onClick)
            .padding(start = 11.dp, end = 7.dp, top = 9.dp, bottom = 9.dp)
            .alpha(if (dim) 0.55f else 1f),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text("◆", style = Type.small, color = if (switching) Ink.blue else Ink.dust)
        Spacer(Modifier.width(6.dp))
        Text(
            current.summary().ifEmpty { if (switching) "Switching…" else "Model" },
            style = Type.label, color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis,
            modifier = Modifier.widthIn(max = 200.dp),
        )
        Icon(Icons.Filled.KeyboardArrowDown, contentDescription = null, tint = Ink.dust, modifier = Modifier.size(18.dp))
    }
}

/**
 * The sheet's content, without the sheet (also for snapshots): the levels
 * as chips, then the models (by provider when there are several); a pick
 * calls [onModel] / [onThinking]. [allowDefault]: a "Default" choice for
 * each (a new task: no flag, the agent's own default; null). [blocked]: why
 * nothing can be applied now. The footer is the caller's.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun ModelSheetContent(
    title: String,
    subtitle: String?,
    choices: ModelChoices?,
    loadProblem: String?,
    model: String?,
    thinking: String?,
    onModel: (String?) -> Unit,
    onThinking: (String?) -> Unit,
    allowDefault: Boolean,
    blocked: String? = null,
    now: String? = null,  // the model it runs now: marked
    modifier: Modifier = Modifier,
    footer: @Composable () -> Unit = {},
) {
    Column(modifier.fillMaxWidth()) {
        LazyColumn(Modifier.weight(1f, fill = false)) {
            item {
                Column(Modifier.padding(start = 24.dp, end = 24.dp, top = 8.dp, bottom = 4.dp)) {
                    Text(title, style = Type.title, color = Ink.text)
                    subtitle?.let { Text(it, style = Type.small, color = Ink.dust) }
                    blocked?.let { Spacer(Modifier.height(12.dp)); Hint(it) }
                    loadProblem?.let { Spacer(Modifier.height(12.dp)); Notice(it) }
                }
            }
            if (choices == null) {
                if (loadProblem == null) item {
                    Column(Modifier.padding(horizontal = 24.dp, vertical = 16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                        // Still bars: the sheet is the colour a skeleton's would blend into.
                        @Composable fun Bar(m: Modifier, h: Int) = Box(m.height(h.dp).clip(RoundedCornerShape(8.dp)).background(Ink.rule.copy(alpha = 0.5f)))
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) { repeat(4) { Bar(Modifier.width(64.dp), 38) } }
                        Spacer(Modifier.height(8.dp))
                        repeat(4) { Column(verticalArrangement = Arrangement.spacedBy(6.dp)) { Bar(Modifier.fillMaxWidth(0.35f), 16); Bar(Modifier.fillMaxWidth(0.5f), 12) } }
                    }
                }
                return@LazyColumn
            }
            item {
                SheetLabel(choices.thinkingName)
                FlowRow(
                    Modifier.padding(horizontal = 24.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    if (allowDefault) LevelChip("Default", selected = thinking == null) { onThinking(null) }
                    choices.thinking.forEach { l -> LevelChip(l.label, selected = l.id == thinking) { onThinking(l.id) } }
                }
                val detail = choices.level(thinking)?.detail.orEmpty()
                if (detail.isNotEmpty()) {
                    Text(detail, style = Type.small, color = Ink.dust, modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 8.dp))
                }
                Spacer(Modifier.height(10.dp))
                SheetLabel("Model")
            }
            if (allowDefault) item {
                ModelRow("Default", "What ${agentTitle(choices.agent)} is set to use", mono = false, selected = model == null) { onModel(null) }
            }
            val groups = choices.models.groupBy { it.group }
            groups.forEach { (group, models) ->
                if (groups.size > 1 && group.isNotEmpty()) item(key = "g:$group") {
                    Text(
                        group, style = Type.small, color = Ink.dust,
                        modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 10.dp, bottom = 2.dp),
                    )
                }
                // A new task's Default stands for Claude's "default" already.
                items(models.filter { !(allowDefault && it.id == "default") }, key = { "m:" + it.id }) { m ->
                    val id = m.id.substringAfter('/')
                    ModelRow(m.label, id.takeIf { it != m.label }, selected = m.id == model, now = m.id == now) { onModel(m.id) }
                }
            }
            if (choices.note.isNotEmpty()) item {
                Text(choices.note, style = Type.small, color = Ink.dust, modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 14.dp, bottom = 4.dp))
            }
        }
        footer()
    }
}

fun agentTitle(agent: String) = when (agent) {
    "claude" -> "Claude Code"
    "codex" -> "Codex"
    else -> agent
}

/**
 * The running agent's sheet: pick, then Apply sends what changed in one go
 * ([onApply] gets null for what stays). [busy]: working or blocked, when
 * nothing can be switched.
 */
@Composable
fun LiveModelSheet(
    agentName: String,
    choices: ModelChoices?,
    loadProblem: String?,
    current: CurrentModel,
    busy: String?,
    onApply: (model: String?, thinking: String?) -> Unit,
    onDismiss: () -> Unit,
) {
    var model by remember { mutableStateOf(current.model.ifEmpty { null }) }
    var thinking by remember { mutableStateOf(current.thinking.ifEmpty { null }) }
    ModelSheet(onDismiss) {
        LiveModelSheetContent(agentName, choices, loadProblem, current, busy, model, thinking, { model = it }, { thinking = it }, onApply, onDismiss)
    }
}

/** What [LiveModelSheet] shows, stateless (also for snapshots). */
@Composable
fun LiveModelSheetContent(
    agentName: String,
    choices: ModelChoices?,
    loadProblem: String?,
    current: CurrentModel,
    busy: String?,
    model: String?,
    thinking: String?,
    onModel: (String?) -> Unit,
    onThinking: (String?) -> Unit,
    onApply: (model: String?, thinking: String?) -> Unit,
    onCancel: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val newModel = model?.takeIf { it != current.model }
    val newThinking = thinking?.takeIf { it != current.thinking }
    val blocked = when {
        choices != null && !choices.live -> "$agentName can't switch its model while it runs. Start a new task to pick one."
        busy == "working" -> "$agentName is working; change the model when it's done."
        busy == "blocked" -> "$agentName is waiting for you; answer it first."
        else -> null
    }
    ModelSheetContent(
        title = "Model and ${(choices?.thinkingName ?: "thinking").lowercase()}",
        subtitle = current.summary().takeIf { it.isNotEmpty() }?.let { "Now: $it" } ?: "What $agentName runs with",
        choices = choices, loadProblem = loadProblem,
        model = model, thinking = thinking, onModel = onModel, onThinking = onThinking,
        allowDefault = false, blocked = blocked, now = current.model.ifEmpty { null }, modifier = modifier,
    ) {
        SheetFooter(
            primary = "Apply",
            enabled = blocked == null && choices != null && (newModel != null || newThinking != null),
            onPrimary = { onApply(newModel, newThinking) },
            onCancel = onCancel,
        )
    }
}

/** The sheet itself, as the app shows it (Paparazzi can't draw it: the content is snapshotted alone). */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ModelSheet(onDismiss: () -> Unit, content: @Composable () -> Unit) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = Ink.pen,
        contentColor = Ink.text,
        dragHandle = { Box(Modifier.padding(top = 10.dp, bottom = 6.dp).width(36.dp).height(4.dp).clip(RoundedCornerShape(2.dp)).background(Ink.rule)) },
    ) {
        Box(Modifier.navigationBarsPadding()) { content() }
    }
}

/** The buttons under the sheet's lists. */
@Composable
fun SheetFooter(primary: String, enabled: Boolean, onPrimary: () -> Unit, onCancel: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().padding(start = 16.dp, end = 24.dp, top = 10.dp, bottom = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        QuietButton("Cancel", onClick = onCancel)
        Spacer(Modifier.weight(1f))
        PrimaryButton(primary, enabled = enabled, onClick = onPrimary)
    }
}

/** One button under the sheet's lists: done picking. */
@Composable
fun DoneFooter(onDone: () -> Unit) {
    PrimaryButton("Done", modifier = Modifier.fillMaxWidth().padding(start = 24.dp, end = 24.dp, top = 10.dp, bottom = 12.dp), onClick = onDone)
}

/** A quiet note: why something waits (the blue of working). Machines.kt too. */
@Composable
fun Hint(text: String) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(Ink.blue.copy(alpha = 0.10f)).padding(horizontal = 14.dp, vertical = 12.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Box(Modifier.padding(top = 6.dp).size(6.dp).clip(RoundedCornerShape(3.dp)).background(Ink.blue))
        Spacer(Modifier.width(10.dp))
        Text(text, style = Type.small, color = Ink.text, modifier = Modifier.weight(1f))
    }
}

@Composable
private fun SheetLabel(text: String) {
    Text(
        text.uppercase(), style = Type.tagSmall, color = Ink.dust.copy(alpha = 0.8f),
        modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 14.dp, bottom = 8.dp),
    )
}

@Composable
private fun LevelChip(label: String, selected: Boolean, onClick: () -> Unit) {
    Box(
        Modifier
            .heightIn(min = 40.dp)
            .clip(RoundedCornerShape(10.dp))
            .background(if (selected) Ink.text else Ink.night)
            .border(1.dp, if (selected) Ink.text else Ink.rule, RoundedCornerShape(10.dp))
            .clickable(role = Role.RadioButton, onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 9.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(label, style = Type.label, color = if (selected) Ink.night else Ink.text, maxLines = 1)
    }
}

@Composable
private fun ModelRow(label: String, detail: String?, selected: Boolean, mono: Boolean = true, now: Boolean = false, onClick: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .heightIn(min = 52.dp)
            .background(if (selected) Ink.night.copy(alpha = 0.55f) else Ink.pen)
            .clickable(role = Role.RadioButton, onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(label, style = Type.body.copy(fontWeight = Type.label.fontWeight), color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            detail?.let { Text(it, style = if (mono) Type.small.copy(fontFamily = JetBrains) else Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis) }
        }
        if (now) Text("now", style = Type.small, color = Ink.mint, modifier = Modifier.padding(start = 12.dp))
        if (selected) {
            Spacer(Modifier.width(12.dp))
            Icon(Icons.Filled.Check, contentDescription = "Picked", tint = Ink.text, modifier = Modifier.size(20.dp))
        }
    }
}
