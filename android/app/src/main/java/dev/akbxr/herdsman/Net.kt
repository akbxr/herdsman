package dev.akbxr.herdsman

import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.State
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext

/**
 * What stands between this phone and the Mac, as far as the phone can tell:
 * no network at all, or no VPN (the Mac is reached over Tailscale, which runs
 * as the phone's VPN). Past that, a Mac that doesn't answer is asleep, off,
 * or not running herdsman: the phone can't tell which.
 */
enum class Link { Offline, NoVpn, Up }

object Net {
    const val TAILSCALE = "com.tailscale.ipn"

    fun link(context: Context): Link {
        val cm = context.getSystemService(ConnectivityManager::class.java) ?: return Link.Up
        @Suppress("DEPRECATION")  // allNetworks: the VPN isn't always the default network's transport
        val caps = cm.allNetworks.mapNotNull { cm.getNetworkCapabilities(it) }
        val online = caps.any { it.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) && !it.hasTransport(NetworkCapabilities.TRANSPORT_VPN) }
        return when {
            caps.any { it.hasTransport(NetworkCapabilities.TRANSPORT_VPN) } -> Link.Up
            !online -> Link.Offline
            else -> Link.NoVpn
        }
    }

    /** Opens the Tailscale app, if it is installed; says whether it did. */
    fun openTailscale(context: Context): Boolean {
        val i = context.packageManager.getLaunchIntentForPackage(TAILSCALE) ?: return false
        context.startActivity(i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        return true
    }

    fun hasTailscale(context: Context) = context.packageManager.getLaunchIntentForPackage(TAILSCALE) != null

    /** Calls back on every change to the networks (main thread not guaranteed). */
    fun watch(context: Context, onChange: () -> Unit): () -> Unit {
        val cm = context.getSystemService(ConnectivityManager::class.java) ?: return {}
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = onChange()
            override fun onLost(network: Network) = onChange()
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) = onChange()
        }
        // Every network, VPNs included (a plain request leaves VPNs out).
        val req = NetworkRequest.Builder()
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            .build()
        return runCatching {
            cm.registerNetworkCallback(req, cb)
            val stop: () -> Unit = { runCatching { cm.unregisterNetworkCallback(cb) } }
            stop
        }.getOrElse { {} }
    }
}

/** The link now, kept up to date while on screen. */
@Composable
fun rememberLink(): State<Link> {
    val context = LocalContext.current
    val state = remember { mutableStateOf(Net.link(context)) }
    DisposableEffect(context) {
        val main = android.os.Handler(android.os.Looper.getMainLooper())
        val stop = Net.watch(context) { main.post { state.value = Net.link(context) } }
        state.value = Net.link(context)
        onDispose { stop() }
    }
    return state
}

/** What to say when the Mac can't be reached, given what the phone knows. */
fun Throwable.advice(link: Link): String = when {
    this is ApiError -> advice()
    link == Link.Offline -> "This phone is offline. It reconnects when it's back."
    link == Link.NoVpn -> "Tailscale is off on this phone. Turn it on to reach your Mac."
    this is java.net.SocketTimeoutException || this is java.net.NoRouteToHostException ->
        "Your Mac isn't answering. It may be asleep or offline. Still trying…"
    this is java.net.ConnectException && message.orEmpty().contains("refused", ignoreCase = true) ->
        "Your Mac is there, but herdsman isn't running on it. Run herdsman install on the Mac."
    this is java.net.ConnectException -> "Your Mac isn't answering. It may be asleep or offline. Still trying…"
    else -> advice()
}
