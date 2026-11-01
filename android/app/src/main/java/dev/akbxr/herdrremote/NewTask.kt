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
import androidx.compose.material.icons.filled.KeyboardArrowDown
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
 * A new task: pick where (the Herdr app or the herdr CLI, when both run), a
 * machine (with others added) or a herdr workspace, a folder, an agent, say
 * what to do. The Mac opens a tab there running the agent with the task;
 * [onStarted] gets its terminal. [app], [cli]: which of the two run.
 */
@Composable
fun NewTaskScreen(
    api: Api, host: String, machines: List<Machine> = emptyList(), onBack: () -> Unit, onStarted: (String) -> Unit,
    app: Boolean = true, cli: Boolean = false, workspaces: List<Workspace> = emptyList(),
) {
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
    // The model and level for the agent (Models.kt): null, its own default.
    // Remembered per agent as they are picked.
    var model by remember { mutableStateOf<String?>(null) }
    var thinking by remember { mutableStateOf<String?>(null) }
    var choices by remember { mutableStateOf<Map<String, ModelChoices>>(emptyMap()) }
    var choicesProblem by remember { mutableStateOf<String?>(null) }
    var modelSheet by remember { mutableStateOf(false) }
    // Earlier sessions in the picked folder (Sessions.kt): none when the Mac
    // can't say (an older herdr-remote).
    var sessions by remember { mutableStateOf<List<EarlierSession>?>(null) }
    var resuming by remember { mutableStateOf<String?>(null) }
    var resumeProblem by remember { mutableStateOf<String?>(null) }
    var sessionsNote by remember { mutableStateOf("") }
    // Where it runs: "" this Mac, or a machine added in Herdr (gone: this Mac again).
    // In the herdr CLI: when it alone runs, or picked (the Herdr app is the default).
    var pickedCli by rememberSaveable { mutableStateOf(false) }
    val inCli = cli && (!app || pickedCli)
    var pickedMachine by rememberSaveable { mutableStateOf("") }
    val machine = if (inCli) "" else pickedMachine.takeIf { m -> m.isEmpty() || machines.any { it.name == m } } ?: ""
    // herdr's workspace: the focused one (first) unless picked.
    var pickedWorkspace by rememberSaveable { mutableStateOf("") }
    val workspace = workspaces.firstOrNull { it.id == pickedWorkspace }?.id ?: workspaces.firstOrNull()?.id ?: ""

    LaunchedEffect(api, machine, picked, attempt, inCli) {
        sessions = null
        sessionsNote = ""
        resumeProblem = null
        // Resuming opens a tab in the Herdr app: not offered for the herdr CLI.
        if (inCli) return@LaunchedEffect
        val cwd = picked ?: return@LaunchedEffect
        val found = runCatching { api.sessions(cwd, machine) }.getOrNull()
        sessions = found?.list.orEmpty()
        sessionsNote = found?.note.orEmpty()
    }

    LaunchedEffect(api, agent, attempt) {
        val id = agent ?: return@LaunchedEffect
        model = pairing.taskModel(id)
        thinking = pairing.taskThinking(id)
        if (!takesModel(id)) return@LaunchedEffect
        choicesProblem = null
        val c = choices[id] ?: runCatching { api.models(id) }
            .onSuccess { choices = choices + (id to it) }
            .onFailure { choicesProblem = it.advice() }
            .getOrNull() ?: return@LaunchedEffect
        // What the agent no longer offers: its default again.
        if (c.model(model) == null) model = null
        if (c.level(thinking) == null) thinking = null
    }

    LaunchedEffect(api, machine, attempt) {
        loadProblem = null
        runCatching {
            coroutineScope {
                val f = async { api.folders(machine) }
                val a = async { api.agents(machine) }
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

    val agentId = agent
    if (modelSheet && agentId != null) {
        val name = agents?.firstOrNull { it.id == agentId }?.name ?: agentTitle(agentId)
        fun keep(m: String?, t: String?) { model = m; thinking = t; pairing.setTaskModel(agentId, m, t) }
        ModelSheet(onDismiss = { modelSheet = false }) {
            ModelSheetContent(
                title = "$name's model",
                subtitle = "Kept for $name's next tasks",
                choices = choices[agentId], loadProblem = choicesProblem,
                model = model, thinking = thinking,
                onModel = { keep(it, thinking) }, onThinking = { keep(model, it) },
                allowDefault = true,
            ) { DoneFooter { modelSheet = false } }
        }
    }

    NewTaskForm(
        host = host,
        backends = app && cli,
        cli = inCli,
        onCli = { c ->
            // The herdr CLI runs on this Mac: another machine's folders go.
            if (c && machine.isNotEmpty()) { pickedMachine = ""; folders = null; agents = null; picked = null; filter = "" }
            pickedCli = c
            problem = null
        },
        workspaces = if (inCli) workspaces else emptyList(),
        workspace = workspace,
        onWorkspace = { pickedWorkspace = it },
        machines = if (inCli) emptyList() else machines,
        machine = machine,
        onMachine = { m ->
            if (m != machine) {
                // Another machine's folders and agents: none of this one's.
                pickedMachine = m
                folders = null
                agents = null
                picked = null
                filter = ""
                problem = null
            }
        },
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
        modelChoices = agent?.let { choices[it] },
        model = model,
        thinking = thinking,
        onModels = { modelSheet = true },
        prompt = prompt,
        onPrompt = { prompt = it },
        busy = busy,
        problem = problem,
        sessions = sessions,
        sessionsNote = sessionsNote,
        resuming = resuming,
        resumeProblem = resumeProblem,
        onSession = pick@{ s ->
            if (busy || resuming != null) return@pick
            resuming = s.id
            resumeProblem = null
            scope.launch {
                runCatching { resumeOrOpen(api, s, onStarted, machine) }.onFailure { resumeProblem = it.advice() }
                resuming = null
            }
        },
        onBack = onBack,
        onStart = start@{
            val cwd = picked ?: return@start
            val id = agent ?: return@start
            if (busy) return@start
            busy = true
            problem = null
            scope.launch {
                val withModel = takesModel(id)
                runCatching { api.startTask(cwd, id, prompt.trim(), model.takeIf { withModel }, thinking.takeIf { withModel }, machine, inCli, workspace.takeIf { inCli }.orEmpty()) }
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
    backends: Boolean = false,  // the Herdr app and the herdr CLI both run: a chooser
    cli: Boolean = false,       // it starts in the herdr CLI
    onCli: (Boolean) -> Unit = {},
    workspaces: List<Workspace> = emptyList(),  // herdr's, for the herdr CLI; one or none: no chooser
    workspace: String = "",
    onWorkspace: (String) -> Unit = {},
    machines: List<Machine> = emptyList(),  // the others added in Herdr; none: no chooser
    machine: String = "",
    onMachine: (String) -> Unit = {},
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
    modelChoices: ModelChoices? = null,
    model: String? = null,
    thinking: String? = null,
    onModels: () -> Unit = {},
    prompt: String,
    onPrompt: (String) -> Unit,
    busy: Boolean,
    problem: String?,
    sessions: List<EarlierSession>? = null,
    sessionsNote: String = "",  // why there are none (the machine's word)
    resuming: String? = null,
    resumeProblem: String? = null,
    onSession: (EarlierSession) -> Unit = {},
    now: Long = System.currentTimeMillis(),
    onBack: () -> Unit,
    onStart: () -> Unit,
) {
    val home = folders?.home.orEmpty()
    var allSessions by rememberSaveable(picked) { mutableStateOf(false) }
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 32.dp)) {
        item {
            Row(Modifier.fillMaxWidth().padding(start = 4.dp, end = 12.dp, top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(
                    Modifier.size(48.dp).clip(RoundedCornerShape(12.dp)).clickable(role = Role.Button, onClick = onBack),
                    contentAlignment = Alignment.Center,
                ) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back to the herd", tint = Ink.text) }
                Text(
                    (if (cli) "A new tab in herdr on " else "A new tab on ") + host.removeSuffix(".local").ifBlank { "your Mac" },
                    style = Type.small, color = Ink.dust, maxLines = 1, overflow = TextOverflow.Ellipsis,
                )
            }
            Column(Modifier.padding(horizontal = 24.dp)) {
                Spacer(Modifier.height(4.dp))
                Text("New task", style = Type.headline, color = Ink.text)
                Spacer(Modifier.height(6.dp))
                val where = if (cli) "in herdr" else "on " + (machines.firstOrNull { it.name == machine }?.label ?: "the Mac")
                Text("Pick a folder and an agent, say what to do. It starts $where and opens here.", style = Type.body, color = Ink.dust)
                Spacer(Modifier.height(20.dp))
                if (backends) {
                    StepLabel("Run in")
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        AgentChip("Herdr app", selected = !cli) { onCli(false) }
                        AgentChip("herdr CLI", selected = cli) { onCli(true) }
                    }
                    Spacer(Modifier.height(22.dp))
                }
                if (cli && workspaces.size > 1) {
                    StepLabel("Workspace")
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        workspaces.forEach { w -> AgentChip(w.label, selected = w.id == workspace) { onWorkspace(w.id) } }
                    }
                    Spacer(Modifier.height(22.dp))
                }
                if (machines.isNotEmpty()) {
                    StepLabel("Machine")
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        MachineChip("This Mac", null, selected = machine.isEmpty()) { onMachine("") }
                        machines.forEach { m ->
                            MachineChip(m.label, m.state.takeIf { m.state != "connected" }, selected = m.name == machine) { onMachine(m.name) }
                        }
                    }
                    Spacer(Modifier.height(22.dp))
                }
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
            }
        }

        // None, and the machine says why (e.g. none kept there).
        if (sessions?.isEmpty() == true && sessionsNote.isNotBlank()) {
            item {
                Column(Modifier.padding(start = 24.dp, end = 24.dp, top = 22.dp)) {
                    StepLabel("Earlier sessions here")
                    Text(sessionsNote, style = Type.small, color = Ink.dust)
                }
            }
        }

        // Earlier sessions here: pick one up instead of starting afresh.
        if (!sessions.isNullOrEmpty()) {
            item {
                Column(Modifier.padding(start = 24.dp, end = 24.dp, top = 22.dp)) {
                    StepLabel("Earlier sessions here")
                }
            }
            val shown = if (allSessions || sessions.size <= SESSIONS_SHOWN + 1) sessions else sessions.take(SESSIONS_SHOWN)
            items(shown, key = { "s:" + it.agent + ":" + it.id }) { s ->
                SessionRow(s, now, folder = null, resuming = resuming == s.id, onClick = { onSession(s) })
            }
            if (shown.size < sessions.size) {
                item {
                    QuietButton(
                        "Show ${sessions.size - shown.size} more", color = Ink.text,
                        modifier = Modifier.padding(start = 12.dp),
                    ) { allSessions = true }
                }
            }
            resumeProblem?.let { item { Notice(it, Modifier.padding(start = 24.dp, end = 24.dp, top = 8.dp)) } }
        }

        item {
            Column(Modifier.padding(horizontal = 24.dp)) {
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
                if (takesModel(agent) && modelChoices?.supported != false) {
                    Spacer(Modifier.height(10.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        PickerBox(
                            "Model", modelChoices?.model(model)?.label ?: model ?: "Default",
                            Modifier.weight(1.4f), onClick = onModels,
                        )
                        PickerBox(
                            modelChoices?.thinkingName ?: "Thinking", modelChoices?.level(thinking)?.label ?: thinking ?: "Default",
                            Modifier.weight(1f), onClick = onModels,
                        )
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

/** Earlier sessions shown in a folder before "Show more". */
private const val SESSIONS_SHOWN = 3

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

/**
 * A machine to start on; [state] set (not connected): shown with it, and
 * it can't be picked.
 */
@Composable
private fun MachineChip(label: String, state: String?, selected: Boolean, onClick: () -> Unit) {
    val enabled = state == null
    Row(
        Modifier
            .heightIn(min = 44.dp)
            .clip(RoundedCornerShape(10.dp))
            .background(if (selected) Ink.text else if (enabled) Ink.pen else Ink.night)
            .border(1.dp, if (selected) Ink.text else Ink.rule, RoundedCornerShape(10.dp))
            .clickable(enabled = enabled, role = Role.RadioButton, onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (state != null) {
            MachineDot(state)
            Spacer(Modifier.width(8.dp))
        }
        Text(
            if (state == null) label else "$label · ${machineStateWord(state)}",
            style = Type.label.copy(fontSize = Type.body.fontSize),
            color = when { selected -> Ink.night; enabled -> Ink.text; else -> Ink.dust }, maxLines = 1,
        )
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

/** A choice shown as a field: what it is small, its value big; it opens a sheet. */
@Composable
private fun PickerBox(label: String, value: String, modifier: Modifier = Modifier, onClick: () -> Unit) {
    Row(
        modifier
            .clip(RoundedCornerShape(12.dp))
            .background(Ink.pen)
            .border(1.dp, Ink.rule, RoundedCornerShape(12.dp))
            .clickable(role = Role.Button, onClickLabel = "Change the ${label.lowercase()}", onClick = onClick)
            .padding(start = 14.dp, end = 8.dp, top = 8.dp, bottom = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(label, style = Type.small, color = Ink.dust, maxLines = 1)
            Text(
                value, style = Type.label.copy(fontSize = Type.body.fontSize),
                color = if (value == "Default") Ink.dust else Ink.text, maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
        }
        Icon(Icons.Filled.KeyboardArrowDown, contentDescription = null, tint = Ink.dust, modifier = Modifier.size(20.dp))
    }
}
