package dev.akbxr.herdsman

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLEncoder

/** What a terminal's program reported (OSC 7501 via Herdr). */
enum class State(val word: String) {
    NeedsYou("Needs you"), Working("Working"), Done("Done"), Idle("Idle");

    companion object {
        fun of(raw: String?): State = when (raw) {
            "blocked" -> NeedsYou
            "working" -> Working
            "done" -> Done
            else -> Idle
        }
    }
}

data class Agent(
    val terminal: String,   // t12, machine/t12, or herdr:wF:p2 (a herdr CLI pane)
    val app: String,        // tego, claude-code, … or "shell"
    val label: String?,     // its name in Herdr, if set
    val state: State,
    val tabId: String,
    val tabName: String,
    val tabActive: Boolean,
    val spaceName: String,
    val pane: String = "", // its pane in Herdr's layout,
    val spaceId: String = "",  // to show its tab on the Mac when another space shows
    val machine: String = "",  // the machine it runs on ("": this Mac)
    val source: String = "app",  // app: the Herdr app's window; herdr: the herdr CLI
    val session: String? = null,  // the title of the session its agent works in
    val cwd: String = "",  // its folder, when known (herdr CLI panes)
) {
    val title get() = label ?: app
    /**
     * The name before the session's title: the label, unless it only says
     * the session again (Claude Code names its terminal after it), then the program.
     */
    val name get() = label?.takeUnless { l -> session != null && (l.contains(session, true) || session.contains(l, true)) } ?: app
    /** The number stamped on the tag: the address without its machine (a herdr pane's: p2). */
    val tagNumber get() = if (isHerdrPane(terminal)) terminal.substringAfterLast(':') else terminal.substringAfterLast('/')
    /** A pane of the herdr CLI, driven through POST /api/herdr (Control.kt). */
    val cli get() = source == "herdr" || isHerdrPane(terminal)
}

/** "herdr:wF:p2": a pane of the herdr CLI, not a terminal of the Herdr app. */
fun isHerdrPane(terminal: String): Boolean = terminal.startsWith("herdr:")

/** The machine in a terminal's address: "vps" for "vps/t7", "" for "t7" (this Mac) and herdr CLI panes. */
fun machineOf(terminal: String): String = if (isHerdrPane(terminal)) "" else terminal.substringBeforeLast('/', "")

/** A workspace of the herdr CLI: a new task can start in it (NewTask.kt). */
data class Workspace(val id: String, val label: String, val focused: Boolean = false)

/** A tab on the Mac, in its space; [active]: the tab its space shows. */
data class Tab(
    val id: String,
    val name: String,
    val active: Boolean,
    val spaceId: String = "",
    val spaceName: String = "",
    val spaceActive: Boolean = true,  // the space the window shows
    val machine: String = "",  // its space's machine ("": this Mac)
)

/**
 * A machine Herdr drives over ssh, or this Mac ([local], name ""). [state]:
 * connecting, connected, needs_login, unreachable; [why]: what went wrong.
 */
data class Machine(
    val name: String,
    val label: String = name,
    val host: String = "",
    val state: String = "connected",
    val why: String = "",
    val local: Boolean = false,
) {
    val connected get() = state == "connected"
    /** Can be asked to connect again: it lost its connection or wants a password. */
    val reconnectable get() = state == "needs_login" || state == "unreachable"

    companion object {
        fun from(o: JSONObject) = Machine(
            name = o.str("name"),
            label = o.str("label").ifEmpty { o.str("name").ifEmpty { "This Mac" } },
            host = o.str("host"),
            state = o.str("state").ifEmpty { "connected" },
            why = o.str("why"),
            local = o.optBoolean("local"),
        )
    }
}

data class Herd(
    val layout: String,
    val agents: List<Agent>,
    val tabs: List<Tab>,  // the Herdr app's only (the Screen tab shows them)
    val machines: List<Machine> = emptyList(),  // the other machines added in Herdr
    val app: Boolean = true,  // the Herdr app runs (the Mac sent its status or spaces)
    val cli: Boolean = false,  // the herdr CLI runs (the Mac sent its snapshot)
    val workspaces: List<Workspace> = emptyList(),  // the herdr CLI's, focused first
) {
    val appAgents get() = agents.filter { !it.cli }
    val cliAgents get() = agents.filter { it.cli }

    companion object {
        /**
         * Reads GET /api/state: the Herdr window's spaces, tabs and terminals,
         * its machines, and the herdr CLI's panes. Either may be missing.
         */
        fun from(root: JSONObject): Herd {
            val layout = root.optJSONObject("status")?.str("layout") ?: ""
            val agents = mutableListOf<Agent>()
            val tabs = mutableListOf<Tab>()
            val spaces = root.optJSONObject("ls")?.optJSONArray("spaces") ?: JSONArray()
            for (i in 0 until spaces.length()) {
                val space = spaces.getJSONObject(i)
                val activeSpace = space.optBoolean("active")
                val spaceMachine = space.str("machine")
                val tabList = space.optJSONArray("tabs") ?: JSONArray()
                for (j in 0 until tabList.length()) {
                    val tab = tabList.getJSONObject(j)
                    val tabId = tab.optString("id")
                    val tabName = tab.optString("name")
                    tabs += Tab(tabId, tabName, tab.optBoolean("active"), space.optString("id"), space.optString("name"), activeSpace, spaceMachine)
                    val terms = tab.optJSONArray("terminals") ?: JSONArray()
                    for (k in 0 until terms.length()) {
                        val t = terms.getJSONObject(k)
                        val terminal = t.optString("terminal")
                        agents += Agent(
                            terminal = terminal,
                            pane = t.optString("pane"),
                            app = t.optString("app").ifBlank { "shell" }.takeUnless { it == "null" } ?: "shell",
                            label = t.optString("name").takeUnless { it.isBlank() || it == "null" },
                            state = State.of(t.optString("status").takeUnless { it == "null" }),
                            tabId = tabId,
                            tabName = tabName,
                            tabActive = tab.optBoolean("active") && activeSpace,
                            spaceName = space.optString("name"),
                            spaceId = space.optString("id"),
                            machine = machineOf(terminal).ifEmpty { spaceMachine },
                        )
                    }
                }
            }
            val list = root.optJSONArray("machines") ?: JSONArray()
            val machines = (0 until list.length()).map { Machine.from(list.getJSONObject(it)) }.filter { !it.local && it.name.isNotEmpty() }
            val app = root.optJSONObject("status") != null || root.optJSONObject("ls") != null
            val cli = root.optJSONObject("herdr")
            val workspaces = cli?.let { readHerdr(it, agents) }.orEmpty()
            // The sessions' titles, by terminal (the server reads them from the transcripts).
            root.optJSONObject("titles")?.let { titles ->
                agents.replaceAll { a -> titles.str(a.terminal).trim().takeIf { it.isNotEmpty() }?.let { a.copy(session = it) } ?: a }
            }
            return Herd(layout, agents, tabs, machines, app, cli != null, workspaces)
        }

        /**
         * herdr's session.snapshot: each pane becomes an [Agent] at
         * herdr:<pane_id>, its program from [agents] (none: a shell). Adds
         * them to [into] by workspace, tab and pane; returns the workspaces.
         */
        private fun readHerdr(snap: JSONObject, into: MutableList<Agent>): List<Workspace> {
            fun objects(key: String) = snap.optJSONArray(key)?.let { a -> (0 until a.length()).mapNotNull { a.optJSONObject(it) } }.orEmpty()
            val focusedWs = snap.str("focused_workspace_id")
            val focusedTab = snap.str("focused_tab_id")
            val spaces = objects("workspaces").associateBy { it.str("workspace_id") }
            val tabs = objects("tabs").associateBy { it.str("tab_id") }
            val programs = objects("agents").associateBy { it.str("pane_id") }
            fun wsFocused(id: String) = id.isNotEmpty() && (id == focusedWs || spaces[id]?.optBoolean("focused") == true)
            fun tabFocused(id: String) = id.isNotEmpty() && (id == focusedTab || tabs[id]?.optBoolean("focused") == true)
            fun name(o: JSONObject?, id: String) = o?.str("label")?.takeIf { it.isNotBlank() }
                ?: o?.optInt("number", 0)?.takeIf { it > 0 }?.toString() ?: id.substringAfterLast(':')
            // Workspaces and tabs in herdr's order: by number, then as listed.
            val wsOrder = spaces.keys.withIndex().associate { (i, id) -> id to (spaces[id]?.optInt("number", 0) ?: 0) * 10_000 + i }
            val tabOrder = tabs.keys.withIndex().associate { (i, id) -> id to (tabs[id]?.optInt("number", 0) ?: 0) * 10_000 + i }
            objects("panes").withIndex()
                .sortedWith(compareBy({ wsOrder[it.value.str("workspace_id")] ?: Int.MAX_VALUE }, { tabOrder[it.value.str("tab_id")] ?: Int.MAX_VALUE }, { it.index }))
                .forEach { (_, p) ->
                    val paneId = p.str("pane_id").ifEmpty { return@forEach }
                    val program = programs[paneId]
                    val tabId = p.str("tab_id").ifEmpty { program?.str("tab_id").orEmpty() }
                    val wsId = p.str("workspace_id").ifEmpty { program?.str("workspace_id").orEmpty() }
                    val app = program?.str("agent")?.takeIf { it.isNotBlank() } ?: "shell"
                    val status = p.str("agent_status").ifEmpty { program?.str("agent_status").orEmpty() }
                    into += Agent(
                        terminal = "herdr:$paneId",
                        app = app,
                        label = herdrTitle(p.str("terminal_title").ifEmpty { program?.str("terminal_title").orEmpty() }, app),
                        state = State.of(status),  // unknown: idle, like a terminal that reports nothing
                        tabId = tabId,
                        tabName = name(tabs[tabId], tabId),
                        tabActive = tabFocused(tabId) && wsFocused(wsId),
                        spaceName = name(spaces[wsId], wsId),
                        pane = paneId,
                        spaceId = wsId,
                        source = "herdr",
                        cwd = p.str("foreground_cwd").ifEmpty { p.str("cwd") },
                    )
                }
            return spaces.keys.sortedWith(compareBy({ !wsFocused(it) }, { wsOrder[it] })).map { Workspace(it, name(spaces[it], it), wsFocused(it)) }
        }

        /**
         * A pane's terminal title as its name, when it says more than the
         * program: spinners and marks before it go ("✳ Fix login" is "Fix login").
         */
        internal fun herdrTitle(raw: String, app: String): String? {
            val t = raw.trim().dropWhile { !it.isLetterOrDigit() && it !in "~/." }.trim()
            if (t.isEmpty()) return null
            val plain = t.lowercase()
            if (plain == app.lowercase() || plain == agentTitle(app).lowercase()) return null
            return t
        }
    }
}

/** A string field; JSON null and a missing field are "". */
internal fun JSONObject.str(key: String): String = if (isNull(key)) "" else optString(key)

/** The Mac said no; [terminal]: the one it points to instead (a session running already). */
open class ApiError(message: String, val terminal: String? = null) : IOException(message)

/** The terminal's model can't be switched from the phone (another machine's): the picker hides. */
class ModelsUnsupported(why: String) : ApiError(why.ifBlank { "The model can't be switched here." })

/** A program's status change, in its own words (OSC 7501 via herdr-core). */
data class AgentEvent(
    val seq: Long,       // the server's event number; answers name it (Answer.kt)
    val terminal: String,
    val state: String,   // idle, working, blocked, done, error; "" cleared; ended
    val kind: String,    // permission, question, auth (blocked only)
    val msg: String,
    val app: String,
    val where: String,   // Herdr's label: "~ / 1"
) {
    companion object {
        fun from(o: JSONObject) = AgentEvent(
            seq = o.optLong("seq"),
            terminal = o.optString("terminal"),
            state = o.optString("state"),
            kind = o.optString("kind"),
            msg = o.optString("msg"),
            app = o.optString("app"),
            where = o.optString("where"),
        )
    }
}

data class AgentEvents(val seq: Long, val events: List<AgentEvent>, val reset: Boolean, val current: List<AgentEvent>?)

class Api(val base: String, val token: String) {

    private fun open(path: String, method: String, timeoutMs: Int): HttpURLConnection {
        val c = URL(base.trimEnd('/') + path).openConnection() as HttpURLConnection
        c.requestMethod = method
        c.connectTimeout = 6000
        c.readTimeout = timeoutMs
        c.setRequestProperty("Authorization", "Bearer $token")
        return c
    }

    private suspend fun call(path: String, body: JSONObject? = null, timeoutMs: Int = 10_000): JSONObject =
        withContext(Dispatchers.IO) {
            val c = open(path, if (body == null) "GET" else "POST", timeoutMs)
            try {
                if (body != null) {
                    c.doOutput = true
                    c.setRequestProperty("Content-Type", "application/json")
                    c.outputStream.use { it.write(body.toString().toByteArray()) }
                }
                val code = c.responseCode
                val text = (if (code < 400) c.inputStream else c.errorStream)?.bufferedReader()?.use { it.readText() } ?: ""
                val json = runCatching { JSONObject(text) }.getOrElse { JSONObject().put("text", text) }
                if (code == 401) throw ApiError("The token was not accepted. Pair again with the token herdsman prints.")
                if (code >= 400) throw ApiError(json.optString("error", "The Mac answered $code."), json.optString("terminal").ifBlank { null })
                json
            } finally {
                c.disconnect()
            }
        }

    suspend fun ping(): String = call("/api/ping").optString("host", "your Mac")

    suspend fun herd(): Herd = call("/api/state").let { withContext(Dispatchers.Default) { Herd.from(it) } }

    /** This Mac first, then the machines added in Herdr (MachinesSheet). */
    suspend fun machines(): List<Machine> {
        val a = call("/api/machines").optJSONArray("machines") ?: return emptyList()
        return (0 until a.length()).map { Machine.from(a.getJSONObject(it)) }
    }

    /** Adds a machine by its ssh address (user@host, or an ssh config alias); [name]: how Herdr calls it (empty: its own pick). */
    suspend fun addMachine(host: String, name: String = ""): Machine {
        val body = JSONObject().put("host", host)
        if (name.isNotBlank()) body.put("name", name)
        return Machine.from(call("/api/machines", body, timeoutMs = 60_000).optJSONObject("machine") ?: JSONObject().put("name", name).put("host", host))
    }

    /** Herdr forgets the machine; its terminals keep running there. */
    suspend fun removeMachine(name: String) {
        call("/api/machines/remove", JSONObject().put("name", name), timeoutMs = 30_000)
    }

    /** Connects to the machine again; fails (409) when it needs a password typed on the Mac. */
    suspend fun connectMachine(name: String) {
        call("/api/machines/connect", JSONObject().put("name", name), timeoutMs = 60_000)
    }

    suspend fun read(terminal: String, lines: Int = 300): String {
        val q = URLEncoder.encode(terminal, "UTF-8")
        val text = call("/api/read?terminal=$q&lines=$lines").optString("text")
        // Trailing spaces pad every row to the terminal's width; drop them.
        return text.lines().joinToString("\n") { it.trimEnd() }.trimEnd()
    }

    suspend fun send(terminal: String, text: String, force: Boolean = false) {
        call("/api/send", JSONObject().put("terminal", terminal).put("text", text).put("force", force), timeoutMs = 45_000)
    }

    /**
     * The agent's conversation (Chat.kt): its last [limit] items, more as you
     * scroll back. With the version of the last one, an unchanged
     * conversation comes back without its items (same = true).
     */
    suspend fun chat(terminal: String, version: String = "", limit: Int = CHAT_PAGE): ChatLog {
        val q = "terminal=" + enc(terminal) + "&limit=$limit" + if (version.isNotEmpty()) "&version=" + enc(version) else ""
        val o = call("/api/chat?$q", timeoutMs = 30_000)
        // Off the main thread: a page of a long chat is a lot of JSON to walk.
        return withContext(Dispatchers.Default) { ChatLog.from(o, limit) }
    }

    /** A picture of the chat, scaled down on the Mac to fit [maxSide] pixels (ChatImages.kt). */
    suspend fun image(id: String, maxSide: Int): ByteArray = withContext(Dispatchers.IO) {
        val c = open("/api/image?id=" + enc(id) + "&w=$maxSide", "GET", 30_000)
        try {
            if (c.responseCode == 401) throw ApiError("The token was not accepted.")
            if (c.responseCode >= 400) throw ApiError("The Mac doesn't have this picture.")
            c.inputStream.use { it.readBytes() }
        } finally {
            c.disconnect()
        }
    }

    /** "/" in the message box: the agent's commands, custom commands and skills. */
    suspend fun commands(terminal: String): List<Command> {
        val a = call("/api/commands?terminal=" + enc(terminal)).optJSONArray("commands") ?: return emptyList()
        return (0 until a.length()).map { a.getJSONObject(it).let { o -> Command(o.optString("name"), o.optString("help")) } }
    }

    /** "@" in the message box: files of the folder the agent works in, best matches first. */
    suspend fun files(terminal: String, query: String): List<String> {
        val a = call("/api/files?terminal=" + enc(terminal) + "&q=" + enc(query), timeoutMs = 12_000).optJSONArray("files") ?: return emptyList()
        return (0 until a.length()).map { a.getString(it) }
    }

    private fun enc(s: String) = java.net.URLEncoder.encode(s, "UTF-8")

    suspend fun keys(terminal: String, vararg keys: String) {
        call("/api/keys", JSONObject().put("terminal", terminal).put("keys", JSONArray(keys.toList())))
    }

    /** Answers a waiting agent: allow, deny, or reply (text). seq: the event it answers. */
    suspend fun answer(terminal: String, seq: Long, answer: String, text: String = "", option: String = "") {
        call(
            "/api/answer",
            JSONObject().put("terminal", terminal).put("seq", seq).put("answer", answer).put("text", text).put("option", option),
            timeoutMs = 45_000,
        )
    }

    /** The menu a waiting agent shows on its screen; null when there is none. */
    suspend fun prompt(terminal: String): Prompt? =
        call("/api/prompt?terminal=" + enc(terminal)).optJSONObject("prompt")?.let(Prompt::from)?.takeIf { it.options.size >= 2 }

    /** The git changes in the agent's folder (Changes.kt); files null when unchanged since version. */
    suspend fun changes(terminal: String, version: String = ""): Changes {
        val q = "terminal=" + enc(terminal) + if (version.isNotEmpty()) "&version=" + enc(version) else ""
        val o = call("/api/changes?$q", timeoutMs = 20_000)
        return withContext(Dispatchers.Default) { Changes.from(o) }
    }

    /**
     * Commits exactly [paths] in the agent's repository with [message]
     * (Commit.kt). A failure's message is what git and its hooks printed.
     */
    suspend fun commit(terminal: String, message: String, paths: List<String>): CommitDone {
        val o = call(
            "/api/commit",
            JSONObject().put("terminal", terminal).put("message", message).put("paths", JSONArray(paths)),
            timeoutMs = 200_000,  // hooks can run tests
        )
        return CommitDone(o.optString("hash"), o.optString("subject"), o.optInt("files"))
    }

    /** One file back as the last commit has it; a new file is deleted. */
    suspend fun discard(terminal: String, path: String) {
        call("/api/discard", JSONObject().put("terminal", terminal).put("path", path), timeoutMs = 35_000)
    }

    /** A commit message written on the Mac from the files' names and statuses (empty [paths]: every changed file). */
    suspend fun suggestCommit(terminal: String, paths: List<String>): String =
        call("/api/commit/suggest?terminal=" + enc(terminal) + paths.joinToString("") { "&path=" + enc(it) }, timeoutMs = 20_000)
            .optString("message")

    /** "?machine=vps", or "" for this Mac. */
    private fun onMachine(machine: String) = if (machine.isEmpty()) "" else "?machine=" + enc(machine)

    /** Where a new task can start on [machine] ("": this Mac): recent folders, then git projects (NewTask.kt). */
    suspend fun folders(machine: String = ""): TaskFolders {
        val o = call("/api/folders" + onMachine(machine), timeoutMs = 15_000)
        fun list(key: String) = o.optJSONArray(key)?.let { a ->
            (0 until a.length()).map { a.getJSONObject(it).let { f -> TaskFolder(f.optString("path"), f.optString("name")) } }
        }.orEmpty()
        return TaskFolders(o.optString("home"), list("recent"), list("projects"))
    }

    /** The agents installed on [machine] ("": this Mac). */
    suspend fun agents(machine: String = ""): List<TaskAgent> {
        val a = call("/api/agents" + onMachine(machine), timeoutMs = 15_000).optJSONArray("agents") ?: return emptyList()
        return (0 until a.length()).map { a.getJSONObject(it).let { o -> TaskAgent(o.optString("id"), o.optString("name")) } }
    }

    /**
     * Opens a tab in [cwd] running [agent] on [prompt], with a [model] and
     * [thinking] level (null: the agent's own default), on [machine] ("":
     * this Mac); returns its terminal (vps/t7 on another machine). In the
     * herdr CLI ([cli]) instead of the Herdr app: in its [workspace] ("": the
     * one herdr picks), and the terminal is herdr:<pane>.
     */
    suspend fun startTask(
        cwd: String, agent: String, prompt: String, model: String? = null, thinking: String? = null, machine: String = "",
        cli: Boolean = false, workspace: String = "",
    ): String {
        val body = JSONObject().put("cwd", cwd).put("agent", agent).put("prompt", prompt)
        if (cli) {
            body.put("backend", "herdr")
            if (workspace.isNotEmpty()) body.put("workspace", workspace)
        } else if (machine.isNotEmpty()) body.put("machine", machine)
        if (model != null) body.put("model", model)
        if (thinking != null) body.put("thinking", thinking)
        return call("/api/task", body, timeoutMs = 30_000)
            .optString("terminal").ifBlank { throw ApiError("The Mac opened the tab but didn't say which terminal it is.") }
    }

    /** Earlier sessions agents can resume, newest first: in [cwd], or anywhere, on [machine] (Sessions.kt). */
    suspend fun sessions(cwd: String? = null, machine: String = ""): EarlierSessions {
        val q = listOfNotNull(cwd?.let { "cwd=" + enc(it) }, machine.takeIf { it.isNotEmpty() }?.let { "machine=" + enc(it) })
        val o = call("/api/sessions" + (if (q.isEmpty()) "" else "?" + q.joinToString("&")), timeoutMs = 15_000)
        val a = o.optJSONArray("sessions")
        val list = (0 until (a?.length() ?: 0)).map { i ->
            a!!.getJSONObject(i).let { s ->
                EarlierSession(
                    agent = s.optString("agent"), id = s.optString("id"), title = s.optString("title"),
                    cwd = s.optString("cwd"), time = s.optLong("time"), messages = s.optInt("messages"),
                    atLeast = s.optBoolean("estimate"), running = s.optString("running").ifBlank { null },
                )
            }
        }
        return EarlierSessions(o.optString("home"), list, o.str("note"))
    }

    /**
     * Opens a tab in a session's folder resuming it; returns its terminal. A
     * session running already fails with [ApiError.terminal] set to its terminal.
     */
    /** Resumes a session in a new tab: of the Herdr app, or with [cli] of the herdr CLI (in [workspace], or its focused one). */
    suspend fun resume(agent: String, id: String, machine: String = "", cli: Boolean = false, workspace: String = ""): String =
        call(
            "/api/resume",
            JSONObject().put("agent", agent).put("id", id).apply {
                if (machine.isNotEmpty()) put("machine", machine)
                if (cli) put("backend", "herdr")
                if (cli && workspace.isNotEmpty()) put("workspace", workspace)
            },
            timeoutMs = 30_000,
        )
            .optString("terminal").ifBlank { throw ApiError("The Mac opened the tab but didn't say which terminal it is.") }

    /**
     * The models and thinking levels an agent offers (Models.kt); with the
     * [terminal] it runs in, [ModelChoices.supported] says whether it can be
     * switched there (not on another machine).
     */
    suspend fun models(agent: String, terminal: String? = null): ModelChoices =
        ModelChoices.from(call("/api/models?agent=" + enc(agent) + (terminal?.let { "&terminal=" + enc(it) } ?: ""), timeoutMs = 15_000))

    /**
     * Switches the running agent's model and/or thinking level with its own
     * slash commands; refused while it works or waits for you. Returns what
     * its screen says after.
     */
    suspend fun switchModel(terminal: String, agent: String, model: String?, thinking: String?): CurrentModel {
        val body = JSONObject().put("terminal", terminal).put("agent", agent)
        if (model != null) body.put("model", model)
        if (thinking != null) body.put("thinking", thinking)
        val o = call("/api/session", body, timeoutMs = 60_000)
        if (o.has("supported") && !o.optBoolean("supported", true)) throw ModelsUnsupported(o.str("why"))
        return CurrentModel.from(o.optJSONObject("current"))
    }

    suspend fun interrupt(terminal: String) {
        call("/api/interrupt", JSONObject().put("terminal", terminal), timeoutMs = 20_000)
    }

    suspend fun command(name: String, args: JSONObject? = null) {
        val body = JSONObject().put("command", name)
        if (args != null) body.put("args", args)
        call("/api/do", body, timeoutMs = 20_000)
    }

    /**
     * Agents' status changes after [after] (null: just the cursor and what
     * each terminal reports now). Waits up to [waitS] seconds for one.
     */
    suspend fun events(after: Long?, waitS: Int = 25): AgentEvents = withContext(Dispatchers.IO) {
        val q = if (after == null) "" else "?after=$after&wait=$waitS"
        val c = open("/api/events$q", "GET", (waitS + 15) * 1000)
        try {
            val ok = c.responseCode < 400
            val text = (if (ok) c.inputStream else c.errorStream)?.bufferedReader()?.use { it.readText() } ?: ""
            val json = runCatching { JSONObject(text) }.getOrElse { JSONObject() }
            if (c.responseCode == 401) throw ApiError("The Mac no longer knows this phone. Pair again.")
            if (!ok) throw ApiError(json.optString("error", "The Mac answered ${c.responseCode}."))
            fun list(key: String) = json.optJSONArray(key)?.let { a -> (0 until a.length()).map { AgentEvent.from(a.getJSONObject(it)) } }
            AgentEvents(json.optLong("seq"), list("events").orEmpty(), json.optBoolean("reset"), list("current"))
        } finally {
            c.disconnect()
        }
    }

    // Herdr's own commands, as the Mac's keys and menus do them.
    private fun argvJson(argv: List<String>?) = argv?.let { JSONArray(it) }

    suspend fun newTab(argv: List<String>?, name: String? = null) =
        command("tab.new", JSONObject().apply { name?.let { put("name", it) }; argvJson(argv)?.let { put("argv", it) } })

    suspend fun split(pane: String, direction: String, argv: List<String>?) =
        command("pane.split", JSONObject().put("direction", direction).put("pane", pane).apply { argvJson(argv)?.let { put("argv", it) } })

    suspend fun zoom(pane: String) = command("pane.zoom", JSONObject().put("pane", pane))
    suspend fun closePane(pane: String) = command("pane.close", JSONObject().put("pane", pane))
    suspend fun closeTab(tabId: String) = command("tab.close", JSONObject().put("id", tabId).put("confirmed", true))
    suspend fun renameTab(tabId: String, name: String) = command("tab.rename", JSONObject().put("id", tabId).put("name", name))
    /** Shows a tab in the Herdr window, its space first when another one shows. */
    suspend fun showTab(tabId: String, spaceId: String = "") {
        if (spaceId.isNotEmpty()) command("space.select", JSONObject().put("id", spaceId))
        command("tab.select", JSONObject().put("id", tabId))
    }

    // The herdr CLI's own methods, with herdr's parameter names (tab_id,
    // pane_id, workspace_id, label, cwd, direction). Closing needs confirmed.
    suspend fun herdr(method: String, params: JSONObject = JSONObject(), confirmed: Boolean = false): JSONObject {
        val body = JSONObject().put("method", method).put("params", params)
        if (confirmed) body.put("confirmed", true)
        return call("/api/herdr", body, timeoutMs = 20_000)
    }

    /** Shows a pane's tab in herdr, its workspace first. */
    suspend fun herdrShow(tabId: String, workspaceId: String) {
        if (workspaceId.isNotEmpty()) herdr("workspace.focus", JSONObject().put("workspace_id", workspaceId))
        herdr("tab.focus", JSONObject().put("tab_id", tabId))
    }
    suspend fun herdrSplit(paneId: String, direction: String) =
        herdr("pane.split", JSONObject().put("target_pane_id", paneId).put("direction", direction))  // herdr's name for the pane split
    suspend fun herdrRenameTab(tabId: String, label: String) = herdr("tab.rename", JSONObject().put("tab_id", tabId).put("label", label))
    suspend fun herdrClosePane(paneId: String) = herdr("pane.close", JSONObject().put("pane_id", paneId), confirmed = true)
    suspend fun herdrCloseTab(tabId: String) = herdr("tab.close", JSONObject().put("tab_id", tabId), confirmed = true)
    /** A new tab with a shell in [workspaceId] ("": herdr's focused one). */
    suspend fun herdrNewTab(workspaceId: String = "") =
        herdr("tab.create", JSONObject().apply { if (workspaceId.isNotEmpty()) put("workspace_id", workspaceId) })

    /**
     * Sends a file from the phone; herdr-core places it beside the terminal.
     * Returns the path the program there can open.
     */
    suspend fun upload(
        context: Context, uri: android.net.Uri, name: String, size: Long, terminal: String,
        onProgress: (Float) -> Unit,
    ): String = withContext(Dispatchers.IO) {
        val q = "terminal=" + URLEncoder.encode(terminal, "UTF-8") + "&name=" + URLEncoder.encode(name, "UTF-8")
        val c = open("/api/upload?$q", "POST", 10 * 60_000)
        try {
            c.doOutput = true
            c.setRequestProperty("Content-Type", "application/octet-stream")
            if (size > 0) c.setFixedLengthStreamingMode(size) else c.setChunkedStreamingMode(256 * 1024)
            val input = context.contentResolver.openInputStream(uri) ?: throw ApiError("The phone couldn't open $name.")
            input.use { src ->
                c.outputStream.use { out ->
                    val buf = ByteArray(256 * 1024)
                    var sent = 0L
                    while (true) {
                        val n = src.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        sent += n
                        if (size > 0) onProgress((sent.toFloat() / size).coerceIn(0f, 1f))
                    }
                }
            }
            val ok = c.responseCode < 400
            val text = (if (ok) c.inputStream else c.errorStream)?.bufferedReader()?.use { it.readText() } ?: ""
            val json = runCatching { JSONObject(text) }.getOrElse { JSONObject() }
            if (!ok) throw ApiError(json.optString("error", "The Mac didn't take $name."))
            json.getString("path")
        } finally {
            c.disconnect()
        }
    }

    suspend fun shot(maxSide: Int = 1600): Bitmap = withContext(Dispatchers.IO) {
        val c = open("/api/shot?max=$maxSide", "GET", 20_000)
        try {
            if (c.responseCode == 401) throw ApiError("The token was not accepted.")
            if (c.responseCode >= 400) throw ApiError("The Mac could not take a picture of the window.")
            c.inputStream.use { BitmapFactory.decodeStream(it) } ?: throw ApiError("The picture came back empty.")
        } finally {
            c.disconnect()
        }
    }
}

/** Everything needed to reach a Mac: from the QR code, or a traded code. */
data class PairInfo(val address: String, val token: String, val host: String)

/** The two ways to pair: the QR code's link, or address + six-digit code. */
object PairFlow {
    /** Reads herdsman://pair?url=…&token=…&host=… (what the QR code holds). */
    fun parseLink(text: String): PairInfo? {
        val uri = runCatching { android.net.Uri.parse(text.trim()) }.getOrNull() ?: return null
        if (uri.scheme != "herdsman" || uri.host != "pair") return null
        val url = uri.getQueryParameter("url")?.takeIf { it.startsWith("http://") || it.startsWith("https://") } ?: return null
        val token = uri.getQueryParameter("token")?.takeIf { it.length >= 32 } ?: return null
        return PairInfo(url, token, uri.getQueryParameter("host").orEmpty())
    }

    /** "100.64.1.2" → "http://100.64.1.2:7710": the scheme and port are optional when typing. */
    fun normalizeAddress(typed: String): String {
        var a = typed.trim().trimEnd('/')
        if (!a.startsWith("http://") && !a.startsWith("https://")) a = "http://$a"
        val hostPart = a.substringAfter("://")
        if (!hostPart.contains(':')) a += ":7710"
        return a
    }

    /** Trades the six-digit code the Mac shows for its token. */
    suspend fun redeem(address: String, code: String): PairInfo = withContext(Dispatchers.IO) {
        val c = URL(address.trimEnd('/') + "/api/pair").openConnection() as HttpURLConnection
        try {
            c.requestMethod = "POST"
            c.connectTimeout = 6000
            c.readTimeout = 10_000
            c.doOutput = true
            c.setRequestProperty("Content-Type", "application/json")
            c.outputStream.use { it.write(JSONObject().put("code", code).toString().toByteArray()) }
            val ok = c.responseCode < 400
            val text = (if (ok) c.inputStream else c.errorStream)?.bufferedReader()?.use { it.readText() } ?: ""
            val json = runCatching { JSONObject(text) }.getOrElse { JSONObject() }
            if (!ok) throw ApiError(json.optString("error", "The Mac refused the code.").replaceFirstChar { it.uppercase() } + ".")
            PairInfo(address, json.getString("token"), json.optString("host"))
        } finally {
            c.disconnect()
        }
    }
}

/** Where the Mac is, remembered between launches. */
class Pairing(context: Context) {
    private val prefs = context.getSharedPreferences("pairing", Context.MODE_PRIVATE)
    var address: String
        get() = prefs.getString("address", "") ?: ""
        set(v) = prefs.edit().putString("address", v).apply()
    var token: String
        get() = prefs.getString("token", "") ?: ""
        set(v) = prefs.edit().putString("token", v).apply()
    var host: String
        get() = prefs.getString("host", "") ?: ""
        set(v) = prefs.edit().putString("host", v).apply()

    /** Notifications from the background watcher (Watch.kt). */
    var notify: Boolean
        get() = prefs.getBoolean("notify", false)
        set(v) = prefs.edit().putBoolean("notify", v).apply()

    /** The agent the last new task ran (NewTask.kt): picked again next time. */
    var lastAgent: String
        get() = prefs.getString("last-agent", "") ?: ""
        set(v) = prefs.edit().putString("last-agent", v).apply()

    /** The model and thinking level last picked for a new task with [agent] (NewTask.kt); null: its default. */
    fun taskModel(agent: String): String? = prefs.getString("task-model-$agent", null)
    fun taskThinking(agent: String): String? = prefs.getString("task-thinking-$agent", null)
    fun setTaskModel(agent: String, model: String?, thinking: String?) =
        prefs.edit().putString("task-model-$agent", model).putString("task-thinking-$agent", thinking).apply()

    /** "Not now" on the run-in-the-background hint: don't show it again. */
    var backgroundHintDismissed: Boolean
        get() = prefs.getBoolean("background-hint-dismissed", false)
        set(v) = prefs.edit().putBoolean("background-hint-dismissed", v).apply()

    // forget() clears notify too: nothing to watch without a Mac.
    fun forget() = prefs.edit().clear().apply()
}

/** Turns a failure into what the person should do about it. */
fun Throwable.advice(): String = when (this) {
    is ApiError -> message ?: "Something went wrong on the Mac."
    is java.net.SocketTimeoutException -> "Your Mac took too long to answer. Check that it is awake."
    is java.net.ConnectException, is java.net.UnknownHostException, is java.net.NoRouteToHostException ->
        "Can't reach your Mac. Check that herdsman is running and Tailscale is on."
    is java.net.MalformedURLException -> "That address doesn't look right. It starts with http://"
    else -> message ?: "Something went wrong."
}
