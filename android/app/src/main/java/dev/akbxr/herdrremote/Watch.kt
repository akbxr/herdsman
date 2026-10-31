package dev.akbxr.herdrremote

import android.Manifest
import android.annotation.SuppressLint
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.provider.Settings
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.ServiceCompat
import androidx.core.content.ContextCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * Notifications while the app is closed: a foreground service long-polls
 * the Mac's /api/events and turns status changes into notifications, in
 * the programs' own words. No push service: the phone asks the Mac
 * directly over Tailscale, so it needs a quiet ongoing notification.
 */
object Watch {
    /** The app is on screen: it shows the herd itself, so no notifications. */
    @Volatile var appVisible = false

    fun start(context: Context) {
        val p = Pairing(context)
        if (!p.notify || p.address.isEmpty()) return
        ContextCompat.startForegroundService(context, Intent(context, WatchService::class.java))
    }

    fun stop(context: Context) {
        context.stopService(Intent(context, WatchService::class.java))
        NotificationManagerCompat.from(context).cancelAll()
    }

    fun canNotify(context: Context) = Build.VERSION.SDK_INT < 33 ||
        ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED

    /** Whether Android lets it keep running in the background. */
    fun unrestricted(context: Context): Boolean = runCatching {
        (context.getSystemService(Context.POWER_SERVICE) as PowerManager).isIgnoringBatteryOptimizations(context.packageName)
    }.getOrDefault(true)  // where Android can't say (snapshots), don't nag

    /** Asks Android to let it run in the background (one system dialog). */
    @SuppressLint("BatteryLife")  // the app's whole job here is to wait on the Mac
    fun askUnrestricted(context: Context) {
        val ask = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:${context.packageName}"))
        runCatching { context.startActivity(ask.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }.onFailure {
            context.startActivity(
                Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${context.packageName}"))
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            )
        }
    }

    internal const val CH_WATCH = "watch"
    internal const val CH_NEEDS = "needs-you"
    internal const val CH_FINISHED = "finished"
    internal const val ONGOING_ID = 1
    const val EXTRA_TERMINAL = "terminal"

    fun channels(context: Context) {
        val nm = context.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannels(listOf(
            NotificationChannel(CH_NEEDS, "Needs you", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "An agent is waiting for an answer or an approval."
            },
            NotificationChannel(CH_FINISHED, "Finished", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "An agent finished its work, or stopped with an error."
            },
            NotificationChannel(CH_WATCH, "Watching your Mac", NotificationManager.IMPORTANCE_MIN).apply {
                description = "Shown while Herdr Remote waits for news from your Mac."
                setShowBadge(false)
            },
        ))
    }
}

class WatchService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var loop: Job? = null
    private val notified = mutableMapOf<String, String>()  // terminal → "state msg" last shown
    private lateinit var host: String

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        Watch.channels(this)
        host = Pairing(this).host.removeSuffix(".local").ifEmpty { "your Mac" }
        ServiceCompat.startForeground(
            this, Watch.ONGOING_ID, ongoing("Watching $host"),
            if (Build.VERSION.SDK_INT >= 34) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else 0,
        )
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (loop?.isActive != true) loop = scope.launch { watch() }
        return START_STICKY
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    private suspend fun watch() {
        val p = Pairing(this)
        if (!p.notify || p.address.isEmpty()) { stopSelf(); return }
        val api = Api(p.address, p.token)
        var cursor: Long? = null
        var failures = 0
        while (scope.isActive) {
            try {
                val r = api.events(cursor)
                if (failures > 0) updateOngoing("Watching $host")
                failures = 0
                if (cursor == null || r.reset) {
                    // What needs you right now, once; then only changes.
                    r.current.orEmpty().forEach(::show)
                } else {
                    r.events.forEach(::show)
                }
                cursor = r.seq
            } catch (e: Exception) {
                if (e is ApiError && e.message?.contains("Pair again") == true) {
                    updateOngoing("This phone isn't paired with $host anymore. Open the app to pair again.")
                    stopSelf()
                    return
                }
                failures++
                if (failures == 2) updateOngoing("Can't reach $host. Trying again…")
                delay(minOf(60_000L, 2_000L * failures))
            }
        }
    }

    /** Turns one status change into a notification, or takes one away. */
    private fun show(e: AgentEvent) {
        val nm = NotificationManagerCompat.from(this)
        val id = e.terminal.hashCode()
        val key = e.state + " " + e.msg
        when (e.state) {
            "blocked", "done", "error" -> {
                if (notified[e.terminal] == key) return
                notified[e.terminal] = key
                if (Watch.appVisible || !Watch.canNotify(this)) return
                @SuppressLint("MissingPermission")  // checked by canNotify
                nm.notify(id, build(e))
            }
            else -> {
                // Working again, idle, cleared or ended: whatever was shown is out of date.
                notified.remove(e.terminal)
                nm.cancel(id)
            }
        }
    }

    private fun build(e: AgentEvent): Notification {
        val name = e.app.ifBlank { e.terminal }
        val (channel, title, color) = when (e.state) {
            "blocked" -> Triple(Watch.CH_NEEDS, "$name needs you", 0xFFE0AF68.toInt())
            "error" -> Triple(Watch.CH_FINISHED, "$name stopped with an error", 0xFFF7768E.toInt())
            else -> Triple(Watch.CH_FINISHED, "$name finished", 0xFF9ECE6A.toInt())
        }
        val text = e.msg.ifBlank {
            when (e.kind) {
                "permission" -> "Waiting for your approval."
                "question" -> "Waiting for your answer."
                "auth" -> "Waiting for you to sign in."
                else -> if (e.state == "blocked") "Waiting for you." else ""
            }
        }
        val open = PendingIntent.getActivity(
            this, e.terminal.hashCode(),
            Intent(this, MainActivity::class.java)
                .putExtra(Watch.EXTRA_TERMINAL, e.terminal)
                .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        return NotificationCompat.Builder(this, channel)
            .setSmallIcon(R.drawable.ic_stat_tag)
            .setColor(color)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setSubText(listOf(e.terminal, e.where).filter { it.isNotBlank() }.joinToString(" · "))
            .setCategory(if (e.state == "blocked") NotificationCompat.CATEGORY_REMINDER else NotificationCompat.CATEGORY_STATUS)
            .setPriority(if (e.state == "blocked") NotificationCompat.PRIORITY_HIGH else NotificationCompat.PRIORITY_DEFAULT)
            .setContentIntent(open)
            .setAutoCancel(true)
            .setGroup("agents")
            .build()
    }

    private fun ongoing(text: String): Notification = NotificationCompat.Builder(this, Watch.CH_WATCH)
        .setSmallIcon(R.drawable.ic_stat_tag)
        .setContentTitle("Herdr Remote")
        .setContentText(text)
        .setOngoing(true)
        .setSilent(true)
        .setPriority(NotificationCompat.PRIORITY_MIN)
        .setContentIntent(PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE))
        .build()

    @SuppressLint("MissingPermission")  // the ongoing notification belongs to the foreground service
    private fun updateOngoing(text: String) {
        if (Watch.canNotify(this)) NotificationManagerCompat.from(this).notify(Watch.ONGOING_ID, ongoing(text))
    }
}

/** Starts watching again after the phone restarts, if it was on. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED || intent.action == Intent.ACTION_MY_PACKAGE_REPLACED) {
            Watch.start(context)
        }
    }
}
