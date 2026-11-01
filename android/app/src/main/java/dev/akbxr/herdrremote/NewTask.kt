package dev.akbxr.herdrremote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
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
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
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
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch

/** A folder on the Mac a task can start in. */
data class TaskFolder(val path: String, val name: String)

data class TaskFolders(val home: String, val recent: List<TaskFolder>, val projects: List<TaskFolder>)

/** An agent installed on the Mac: its id for the server, its name for people. */
data class TaskAgent(val id: String, val name: String)

/** "/Users/me/Code/app" as "~/Code/app". */
fun shortPath(path: String, home: String): String = when {
    home.isNotEmpty() && path == home -> "~"
    home.isNotEmpty() && path.startsWith("$home/") -> "~" + path.removePrefix(home)
    else -> path
}

/** What a folder is called on the phone: home is "Home" (its path shows as ~). */
fun folderTitle(path: String, home: String): String =
    if (home.isNotEmpty() && path == home) "Home" else path.trimEnd('/').substringAfterLast('/').ifEmpty { "/" }

/** A path typed into the filter ("~/x", "/x"), as a full path; null when it isn't one. */
fun typedPath(filter: String, home: String): String? {
    val t = filter.trim().trimEnd('/')
    return when {
        t.startsWith("/") && t.length > 1 -> t
        t == "~" && home.isNotEmpty() -> home
        t.startsWith("~/") && home.isNotEmpty() -> home + t.removePrefix("~")
        else -> null
    }
}

/**
 * A new task: pick a folder, pick an agent, say what to do. The Mac opens a
 * tab there running the agent with the task; [onStarted] gets its terminal.
 */
@Composable
fun NewTaskScreen(api: Api, host: String, onBack: () -> Unit, onStarted: (String) -> Unit) {
    val context = LocalContext.current
    val pairing = remember { Pairing(context) }
    val scope = rememberCoroutineScope()
    var folders by remember { mutableStateOf<TaskFolders?>(null) }
    var agents by remember { mutableStateOf<List<TaskAgent>?>(null) }
    var loadProblem by remember { mutableStateOf<String?>(null) }
    var attempt by remember { mutableIntStateOf(0) }
    var filter by rememberSaveable { mutableStateOf("") }
    var picked by rememberSaveable { mutableStateOf<String?>(null) }
    var agent by rememberSaveable { mutableStateOf<String?>(null) }
    var prompt by rememberSaveable { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var problem by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(api, attempt) {
        loadProblem = null
        runCatching {
            coroutineScope {
                val f = async { api.folders() }
                val a = async { api.agents() }
                f.await() to a.await()
            }
        }.onSuccess { (f, a) ->
            folders = f
            agents = a
            if (agent == null || a.none { it.id == agent }) {
                agent = a.firstOrNull { it.id == pairing.lastAgent }?.id ?: a.firstOrNull()?.id
            }
        }.onFailure { loadProblem = it.advice() }
    }

    NewTaskForm(
        host = host,
        folders = folders,
        agents = agents,
        loadProblem = loadProblem,
        onRetry = { attempt++ },
        filter = filter,
        onFilter = { filter = it },
        picked = picked,
        onPick = { picked = it; problem = null },
        agent = agent,
        onAgent = { agent = it },
        prompt = prompt,
        onPrompt = { prompt = it },
        busy = busy,
        problem = problem,
        onBack = onBack,
        onStart = start@{
            val cwd = picked ?: return@start
            val id = agent ?: return@start
            if (busy) return@start
            busy = true
            problem = null
            scope.launch {
                runCatching { api.startTask(cwd, id, prompt.trim()) }
                    .onSuccess { terminal -> pairing.lastAgent = id; onStarted(terminal) }
                    .onFailure { problem = it.advice() }
                busy = false
            }
        },
    )
}

/** The new-task screen as shown, without the Mac (also for snapshots). */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun NewTaskForm(
    host: String,
    folders: TaskFolders?,
    agents: List<TaskAgent>?,
    loadProblem: String?,
    onRetry: () -> Unit,
    filter: String,
    onFilter: (String) -> Unit,
    picked: String?,
    onPick: (String?) -> Unit,
    agent: String?,
    onAgent: (String) -> Unit,
    prompt: String,
    onPrompt: (String) -> Unit,
    busy: Boolean,
    problem: String?,
    onBack: () -> Unit,
    onStart: () -> Unit,
) {
    val home = folders?.home.orEmpty()
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 32.dp)) {
        item {
            Row(Modifier.fillMaxWidth().padding(start = 4.dp, end = 12.dp, top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier.size(48.dp).clip(RoundedCornerShape(12.dp)).clickable(role = Role.Button, onClick = onBack),
                    contentAlignment = Alignment.Center,
                ) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back to the herd", tint = Ink.text) }
                Text(
                    "A new tab on ${host.removeSuffix(".local").ifBlank { "your Mac" }}",
                    style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            Column(Modifier.padding(horizontal = 24.dp)) {
                Spacer(Modifier.height(4.dp))
                Text("New task", style = Type.headline, color = Ink.text)
                Spacer(Modifier.height(6.dp))
                Text("Pick a folder and an agent, say what to do. It starts on the Mac and opens here.", style = Type.body, color = Ink.dust)
                Spacer(Modifier.height(20.dp))
                loadProblem?.let { Notice(it, action = "Try again" to onRetry); Spacer(Modifier.height(16.dp)) }
            }
        }

        if (picked == null) {
            folderPicker(folders, filter, onFilter, onPick)
            return@LazyColumn
        }

        item {
            Column(Modifier.padding(horizontal = 24.dp)) {
                StepLabel("Folder")
                Row(
                    Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(12.dp))
                        .background(Ink.pen)
                        .border(1.dp, Ink.rule, RoundedCornerShape(12.dp))
                        .clickable(role = Role.Button, onClickLabel = "Pick another folder") { onPick(null) }
                        .padding(start = 14.dp, end = 4.dp, top = 10.dp, bottom = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Column(Modifier.weight(1f)) {
                        Text(
                            folderTitle(picked, home),
                            style = Type.title, color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        )
                        Text(shortPath(picked, home), style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    QuietButton("Change", color = Ink.text) { onPick(null) }
                }

                Spacer(Modifier.height(22.dp))
                StepLabel("Agent")
                when {
                    agents == null -> Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        repeat(3) { SkeletonBar(Modifier.width(84.dp), height = 42) }
                    }
                    agents.isEmpty() -> Text(
                        "No agent found on the Mac. Install tego, Claude Code or Codex there, or add one to Herdr's programs.",
                        style = Type.small, color = Ink.dust,
                    )
                    else -> FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        agents.forEach { a -> AgentChip(a.name, selected = a.id == agent) { onAgent(a.id) } }
                    }
                }

                Spacer(Modifier.height(22.dp))
                StepLabel("What to do")
                BasicTextField(
                    value = prompt,
                    onValueChange = onPrompt,
                    textStyle = Type.body.copy(color = Ink.text),
                    cursorBrush = SolidColor(Ink.yellow),
                    keyboardOptions = KeyboardOptions(capitalization = KeyboardCapitalization.Sentences),
                    modifier = Modifier.fillMaxWidth(),
                    decorationBox = { inner ->
                        Box(
                            Modifier
                                .fillMaxWidth()
                                .heightIn(min = 132.dp)
                                .clip(RoundedCornerShape(12.dp))
                                .background(Ink.pen)
                                .border(1.dp, Ink.rule, RoundedCornerShape(12.dp))
                                .padding(horizontal = 14.dp, vertical = 13.dp),
                        ) {
                            if (prompt.isEmpty()) {
                                Text("Fix the flaky login test, then run the whole suite", style = Type.body, color = Ink.dust.copy(alpha = 0.7f))
                            }
                            inner()
                        }
                    },
                )

                problem?.let { Spacer(Modifier.height(16.dp)); Notice(it) }
                Spacer(Modifier.height(18.dp))
                val agentName = agents?.firstOrNull { it.id == agent }?.name
                PrimaryButton(
                    when {
                        busy -> "Starting…"
                        agentName != null -> "Start $agentName"
                        else -> "Start"
                    },
                    enabled = !busy && agent != null && prompt.isNotBlank(),
                    modifier = Modifier.fillMaxWidth(),
                    onClick = onStart,
                )
            }
        }
    }
}

/** Step one: recent folders, then projects; the filter narrows both, or takes a path. */
private fun LazyListScope.folderPicker(folders: TaskFolders?, filter: String, onFilter: (String) -> Unit, onPick: (String) -> Unit) {
    item {
        Column(Modifier.padding(horizontal = 24.dp)) {
            Field("Folder", filter, onFilter, "Filter, or type a path: ~/Code/app", onDone = {
                typedPath(filter, folders?.home.orEmpty())?.let(onPick)
            })
            Spacer(Modifier.height(8.dp))
        }
    }
    if (folders == null) {
        item {
            Column(Modifier.padding(horizontal = 24.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(18.dp)) {
                repeat(5) {
                    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        SkeletonBar(Modifier.fillMaxWidth(0.4f), height = 18)
                        SkeletonBar(Modifier.fillMaxWidth(0.7f), height = 12)
                    }
                }
            }
        }
        return
    }
    val q = filter.trim().lowercase()
    fun match(f: TaskFolder) = q.isEmpty() || folderTitle(f.path, folders.home).lowercase().contains(q) || shortPath(f.path, folders.home).lowercase().contains(q)
    val recent = folders.recent.filter(::match)
    val projects = folders.projects.filter(::match)
    typedPath(filter, folders.home)?.let { path ->
        item { FolderRow(folderTitle(path, folders.home), "Use ${shortPath(path, folders.home)}") { onPick(path) } }
    }
    if (recent.isNotEmpty()) {
        item { SectionLabel("Recent") }
        items(recent, key = { "r:" + it.path }) { f -> FolderRow(folderTitle(f.path, folders.home), shortPath(f.path, folders.home)) { onPick(f.path) } }
    }
    if (projects.isNotEmpty()) {
        item { SectionLabel("Projects") }
        items(projects, key = { "p:" + it.path }) { f -> FolderRow(folderTitle(f.path, folders.home), shortPath(f.path, folders.home)) { onPick(f.path) } }
    }
    if (recent.isEmpty() && projects.isEmpty() && typedPath(filter, folders.home) == null) {
        item {
            Text(
                if (q.isEmpty()) "No folders found on the Mac. Type a path above." else "No folder matches \u201C${filter.trim()}\u201D. Type a path, like ~/Code/app.",
                style = Type.small, color = Ink.dust, modifier = Modifier.padding(horizontal = 24.dp, vertical = 12.dp),
            )
        }
    }
}

@Composable
private fun StepLabel(text: String) {
    Text(text, style = Type.label, color = Ink.dust)
    Spacer(Modifier.height(8.dp))
}

@Composable
private fun SectionLabel(text: String) {
    Text(
        text.uppercase(), style = Type.tagSmall, color = Ink.dust.copy(alpha = 0.8f),
        modifier = Modifier.padding(start = 24.dp, end = 24.dp, top = 14.dp, bottom = 4.dp),
    )
}

/** A folder: its name big, where it is small. */
@Composable
private fun FolderRow(name: String, path: String, onClick: () -> Unit) {
    Column(
        Modifier
            .fillMaxWidth()
            .heightIn(min = 56.dp)
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 24.dp, vertical = 9.dp),
    ) {
        Text(name, style = Type.title, color = Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(path, style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
private fun AgentChip(name: String, selected: Boolean, onClick: () -> Unit) {
    Box(
        Modifier
            .heightIn(min = 44.dp)
            .clip(RoundedCornerShape(10.dp))
            .background(if (selected) Ink.text else Ink.pen)
            .border(1.dp, if (selected) Ink.text else Ink.rule, RoundedCornerShape(10.dp))
            .clickable(role = Role.RadioButton, onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 10.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(name, style = Type.label.copy(fontSize = Type.body.fontSize), color = if (selected) Ink.night else Ink.text, maxLines = 1)
    }
}
