package dev.akbxr.herdsman

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * The ways to one Mac, fastest first (the local network, Tailscale, a
 * Cloudflare tunnel), and the one in use. Requests go to [active]; when it
 * stops answering, [repick] tries them all and moves to the best that
 * answers. Earlier (faster) ones are tried again on a network change and
 * every minute ([recheck]), and the Mac's own list is learned from its ping.
 *
 * One per Mac (token) in the process, so the screens and the background
 * watcher agree on where the Mac is.
 */
class Routes private constructor(initial: List<String>, val token: String) {
    var all: List<String> by mutableStateOf(initial)
        private set
    /** Where requests go now. Compose state: a terminal reconnects when it moves. */
    var active: String by mutableStateOf(initial.firstOrNull().orEmpty())
        private set
    /** Told when the list changes (to remember it). */
    var onLearned: ((List<String>) -> Unit)? = null

    private val picking = Mutex()

    /** Whether every way goes through Tailscale (then "Tailscale is off" is the advice). */
    val needsVpn get() = all.isNotEmpty() && all.all { isTailscale(it) }

    /**
     * Tries every route, best first, and moves [active] to the best that
     * answers; returns it, or null when none does. [failed]: the route a
     * request just failed on (another caller may have moved on already).
     */
    suspend fun repick(failed: String? = null): String? = picking.withLock {
        if (failed != null && failed != active) return active  // someone moved on meanwhile
        val best = probeAll(all, token)
        if (best != null) active = best
        best
    }

    /** On a network change, and every minute: back to a faster route if one answers again. */
    suspend fun recheck() {
        if (all.indexOf(active) == 0) return
        repick()
    }

    /** Use [route] from now on, if it is one of these (pairing found it answering). */
    fun prefer(route: String) {
        if (route in all) active = route
    }

    private fun replace(routes: List<String>) {
        all = routes
        if (active !in routes) active = routes.first()
    }

    /** The Mac's list (from its ping): kept, and tried from the next repick on. */
    fun learn(routes: List<String>) {
        val clean = routes.filter { it.startsWith("http://") || it.startsWith("https://") }.map { it.trimEnd('/') }.distinct()
        if (clean.isEmpty() || clean == all) return
        // A route in use that the Mac no longer lists (an address it lost) goes last, until it fails.
        all = if (active.isNotEmpty() && active !in clean) clean + active else clean
        onLearned?.invoke(all)
    }

    companion object {
        private val known = HashMap<String, Routes>()

        /** The process's routes for this Mac ([token]), made with [routes] the first time. */
        @Synchronized
        fun of(routes: List<String>, token: String): Routes {
            val r = known.getOrPut(token) { Routes(routes.ifEmpty { listOf("") }, token) }
            if (routes.isNotEmpty() && routes != r.all) r.replace(routes)  // paired again, with other routes
            return r
        }

        /** Fresh, not shared: for pairing (the Mac isn't ours yet). */
        fun lone(base: String, token: String) = Routes(listOf(base), token)

        fun isTailscale(route: String): Boolean {
            val host = runCatching { URL(route).host }.getOrNull() ?: return false
            val ip = host.split('.').mapNotNull { it.toIntOrNull() }
            return ip.size == 4 && ip[0] == 100 && ip[1] in 64..127
        }

        /** A private address (192.168.x.x, 10.x, 172.16-31.x): the local network. */
        fun isLocal(route: String): Boolean {
            val host = runCatching { URL(route).host }.getOrNull() ?: return false
            if (host.endsWith(".local")) return true
            val ip = host.split('.').mapNotNull { it.toIntOrNull() }
            if (ip.size != 4) return false
            return ip[0] == 10 || (ip[0] == 192 && ip[1] == 168) || (ip[0] == 172 && ip[1] in 16..31)
        }

        /** How long to wait for a route: a near one answers fast or not at all. */
        fun timeoutMs(route: String) = if (isLocal(route) || isTailscale(route)) 1500 else 6000

        /**
         * The best route that answers /api/ping with this token: all are
         * asked at once, and taken in order, so a far one waits only as long
         * as the near ones before it take to fail.
         */
        suspend fun probeAll(routes: List<String>, token: String, probe: suspend (String, String) -> Boolean = ::ping): String? =
            coroutineScope {
                val asks = routes.filter { it.isNotEmpty() }.map { r -> r to async { runCatching { probe(r, token) }.getOrDefault(false) } }
                asks.firstOrNull { (_, ok) -> ok.await() }?.first.also { asks.forEach { (_, d) -> d.cancel() } }
            }

        private suspend fun ping(route: String, token: String): Boolean = withContext(Dispatchers.IO) {
            val c = URL(route.trimEnd('/') + "/api/ping").openConnection() as HttpURLConnection
            try {
                c.connectTimeout = timeoutMs(route)
                c.readTimeout = timeoutMs(route) + 2000
                c.setRequestProperty("Authorization", "Bearer $token")
                c.responseCode == 200
            } finally {
                c.disconnect()
            }
        }

        /** The routes a ping answer lists, empty from a Mac before routes. */
        fun fromPing(o: JSONObject): List<String> =
            o.optJSONArray("routes")?.let { a -> (0 until a.length()).map { a.getString(it) } } ?: emptyList()
    }
}

/** Whether a failure means the route itself is unreachable (worth another route), not an answer. */
fun Throwable.unreachable(): Boolean = when (this) {
    is java.net.ConnectException, is java.net.UnknownHostException, is java.net.NoRouteToHostException -> true
    is java.net.SocketTimeoutException -> message.orEmpty().contains("connect", ignoreCase = true)
    is javax.net.ssl.SSLException -> true
    else -> false
}
