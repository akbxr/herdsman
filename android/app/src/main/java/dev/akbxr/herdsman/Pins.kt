package dev.akbxr.herdsman

import android.content.Context
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.addPathNodes
import androidx.compose.ui.unit.dp
import org.json.JSONArray

/**
 * What you pinned to the top of the herd: agents (by their terminal) and
 * tabs (every terminal in them), in the order you pinned them. Keys:
 * "a:<terminal>" and "t:<machine>/<tab id>" (a herdr CLI tab "t:herdr:<tab id>").
 */
internal fun pinKey(agent: Agent): String = "a:" + agent.terminal

internal fun tabPinKey(agent: Agent): String =
    if (agent.cli) "t:herdr:" + agent.tabId else "t:" + agent.machine + "/" + agent.tabId

/**
 * The pinned among [agents], in the order of their pins (an agent pinned on
 * its own goes where its own pin is), the rest of [agents] in their order.
 */
internal fun splitPinned(agents: List<Agent>, pins: List<String>): Pair<List<Agent>, List<Agent>> {
    if (pins.isEmpty()) return emptyList<Agent>() to agents
    val rank = pins.withIndex().associate { (i, k) -> k to i }
    fun rankOf(a: Agent): Int? = listOfNotNull(rank[pinKey(a)], rank[tabPinKey(a)]).minOrNull()
    val pinned = agents.filter { rankOf(it) != null }
    // Stable: agents of one pinned tab stay in the order they came.
    return pinned.sortedBy { rankOf(it) } to agents.filter { rankOf(it) == null }
}

/** The pins of the herd of one Mac (its host name), kept on the phone. */
class Pins(context: Context, private val host: String) {
    private val prefs = context.getSharedPreferences("pins", Context.MODE_PRIVATE)

    var keys: List<String> by mutableStateOf(load())
        private set

    fun agentPinned(agent: Agent) = pinKey(agent) in keys
    fun tabPinned(agent: Agent) = tabPinKey(agent) in keys

    fun toggleAgent(agent: Agent) = toggle(pinKey(agent))
    fun toggleTab(agent: Agent) = toggle(tabPinKey(agent))

    private fun toggle(key: String) {
        keys = if (key in keys) keys - key else (keys + key).takeLast(MAX)
        prefs.edit().putString(host, JSONArray(keys).toString()).apply()
    }

    private fun load(): List<String> = runCatching {
        val a = JSONArray(prefs.getString(host, null) ?: return emptyList())
        List(a.length()) { a.getString(it) }
    }.getOrDefault(emptyList())

    private companion object {
        const val MAX = 64  // the oldest go: pins of terminals long closed
    }
}

/** A push pin (Material's push_pin), for pinned rows and the Pinned heading. */
val PinIcon: ImageVector by lazy {
    ImageVector.Builder("Pin", 24.dp, 24.dp, 24f, 24f).addPath(
        addPathNodes(
            "M16,9V4l1,0c0.55,0 1,-0.45 1,-1v0c0,-0.55 -0.45,-1 -1,-1H7C6.45,2 6,2.45 6,3v0c0,0.55 0.45,1 1,1l1,0v5" +
                "c0,1.66 -1.34,3 -3,3h0v2h5.97v7l1,1l1,-1v-7H19v-2h0C17.34,12 16,10.66 16,9z",
        ),
        fill = SolidColor(Color.Black),
    ).build()
}
