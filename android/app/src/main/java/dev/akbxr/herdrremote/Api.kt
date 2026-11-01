package dev.akbxr.herdrremote

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
    val terminal: String,   // t12, or machine/t12
    val app: String,        // tego, claude-code, … or "shell"
    val label: String?,     // its name in Herdr, if set
    val state: State,
    val tabId: String,
    val tabName: String,
    val tabActive: Boolean,
    val spaceName: String,
    val pane: String = "", // its pane in Herdr's layout
) {
    val title get() = label ?: app
    /** The number stamped on the tag: the address without its machine. */
    val tagNumber get() = terminal.substringAfterLast('/')
}

data class Tab(val id: String, val name: String, val active: Boolean)

data class Herd(val layout: String, val agents: List<Agent>, val tabs: List<Tab>)

class ApiError(message: String) : IOException(message)

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
                if (code == 401) throw ApiError("The token was not accepted. Pair again with the token herdr-remote prints.")
                if (code >= 400) throw ApiError(json.optString("error", "The Mac answered $code."))
                json
            } finally {
                c.disconnect()
            }
        }

    suspend fun ping(): String = call("/api/ping").optString("host", "your Mac")

    suspend fun herd(): Herd {
        val root = call("/api/state")
        val layout = root.optJSONObject("status")?.optString("layout") ?: ""
        val agents = mutableListOf<Agent>()
        val tabs = mutableListOf<Tab>()
        val spaces = root.optJSONObject("ls")?.optJSONArray("spaces") ?: JSONArray()
        for (i in 0 until spaces.length()) {
            val space = spaces.getJSONObject(i)
            val activeSpace = space.optBoolean("active")
            val tabList = space.optJSONArray("tabs") ?: JSONArray()
            for (j in 0 until tabList.length()) {
                val tab = tabList.getJSONObject(j)
                val tabId = tab.optString("id")
                val tabName = tab.optString("name")
                if (activeSpace) tabs += Tab(tabId, tabName, tab.optBoolean("active"))
                val terms = tab.optJSONArray("terminals") ?: JSONArray()
                for (k in 0 until terms.length()) {
                    val t = terms.getJSONObject(k)
                    agents += Agent(
                        terminal = t.optString("terminal"),
                        pane = t.optString("pane"),
                        app = t.optString("app").ifBlank { "shell" }.takeUnless { it == "null" } ?: "shell",
                        label = t.optString("name").takeUnless { it.isBlank() || it == "null" },
                        state = State.of(t.optString("status").takeUnless { it == "null" }),
                        tabId = tabId,
                        tabName = tabName,
                        tabActive = tab.optBoolean("active") && activeSpace,
                        spaceName = space.optString("name"),
                    )
                }
            }
        }
        return Herd(layout, agents, tabs)
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
        return ChatLog.from(call("/api/chat?$q", timeoutMs = 30_000), limit)
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
        return Changes.from(call("/api/changes?$q", timeoutMs = 20_000))
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
    suspend fun showTab(tabId: String) = command("tab.select", JSONObject().put("id", tabId))

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
    /** Reads herdr-remote://pair?url=…&token=…&host=… (what the QR code holds). */
    fun parseLink(text: String): PairInfo? {
        val uri = runCatching { android.net.Uri.parse(text.trim()) }.getOrNull() ?: return null
        if (uri.scheme != "herdr-remote" || uri.host != "pair") return null
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
        "Can't reach your Mac. Check that herdr-remote is running and Tailscale is on."
    is java.net.MalformedURLException -> "That address doesn't look right. It starts with http://"
    else -> message ?: "Something went wrong."
}
