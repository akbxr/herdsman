package dev.akbxr.herdsman

import android.annotation.SuppressLint
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.app.RemoteInput
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.util.concurrent.atomic.AtomicBoolean

/**
 * The buttons on an agent's notification: Allow and Deny when it asks for
 * approval, Reply when it asks a question, finished or failed (your answer, or
 * your next instruction). They answer without opening the app; the Mac only
 * types while the agent still waits on what the notification showed.
 *
 * Allow and Reply ask for the phone to be unlocked first: they type on the Mac.
 */
object Answer {
    const val ACTION = "dev.akbxr.herdsman.ANSWER"
    const val KEY_REPLY = "reply"
    private const val EXTRA_TERMINAL = "terminal"
    private const val EXTRA_SEQ = "seq"
    private const val EXTRA_ANSWER = "answer"
    private const val EXTRA_TITLE = "title"
    private const val EXTRA_OPTION = "option"

    /** Choices that say no: they don't need the phone unlocked. */
    private fun declines(option: String) = Regex("""^(no|deny|reject|cancel|don't)\b""", RegexOption.IGNORE_CASE).containsMatchIn(option)

    /**
     * The buttons for a notification about e. With the menu on the agent's
     * screen ([prompt]), its choices (Android shows three at most); without,
     * Allow and Deny for an approval, Reply otherwise.
     */
    fun actions(context: Context, e: AgentEvent, title: String, prompt: Prompt? = null): List<NotificationCompat.Action> {
        fun intent(answer: String, mutable: Boolean = false, option: String = "", slot: Int = 0): PendingIntent {
            val i = Intent(context, AnswerReceiver::class.java)
                .setAction("$ACTION.$answer.$slot")
                .putExtra(EXTRA_TERMINAL, e.terminal)
                .putExtra(EXTRA_SEQ, e.seq)
                .putExtra(EXTRA_ANSWER, answer)
                .putExtra(EXTRA_TITLE, title)
                .putExtra(EXTRA_OPTION, option)
            // One request code per terminal, answer and button: a later notification replaces the extras.
            val code = (e.terminal + answer + slot).hashCode()
            val flags = PendingIntent.FLAG_UPDATE_CURRENT or
                (if (mutable) PendingIntent.FLAG_MUTABLE else PendingIntent.FLAG_IMMUTABLE)
            return PendingIntent.getBroadcast(context, code, i, flags)
        }
        val reply = NotificationCompat.Action.Builder(
            R.drawable.ic_stat_tag,
            if (e.state == "blocked") "Reply" else "Reply to ${e.app.ifBlank { "it" }}",
            intent("reply", mutable = true),  // RemoteInput fills the text in
        )
            .addRemoteInput(RemoteInput.Builder(KEY_REPLY).setLabel(if (e.state == "blocked") "Your answer" else "What next?").build())
            .setSemanticAction(NotificationCompat.Action.SEMANTIC_ACTION_REPLY)
            .setAllowGeneratedReplies(false)
            .setAuthenticationRequired(true)
            .build()
        if (e.state == "blocked" && prompt != null && prompt.options.size >= 2) {
            return prompt.options.take(3).mapIndexed { slot, option ->
                NotificationCompat.Action.Builder(R.drawable.ic_stat_tag, option, intent("option", option = option, slot = slot))
                    .setAuthenticationRequired(!declines(option))
                    .build()
            }
        }
        return when {
            e.state == "blocked" && e.kind == "permission" -> listOf(
                NotificationCompat.Action.Builder(R.drawable.ic_stat_tag, "Allow", intent("allow"))
                    .setAuthenticationRequired(true)
                    .build(),
                NotificationCompat.Action.Builder(R.drawable.ic_stat_tag, "Deny", intent("deny")).build(),
            )
            e.state == "blocked" && e.kind == "auth" -> emptyList()  // signing in needs the screen
            else -> listOf(reply)
        }
    }

    internal fun handle(context: Context, intent: Intent, done: () -> Unit) {
        val terminal = intent.getStringExtra(EXTRA_TERMINAL).orEmpty()
        val seq = intent.getLongExtra(EXTRA_SEQ, 0)
        val answer = intent.getStringExtra(EXTRA_ANSWER).orEmpty()
        val title = intent.getStringExtra(EXTRA_TITLE).orEmpty()
        val option = intent.getStringExtra(EXTRA_OPTION).orEmpty()
        val text = RemoteInput.getResultsFromIntent(intent)?.getCharSequence(KEY_REPLY)?.toString().orEmpty()
        val p = Pairing(context)
        if (terminal.isEmpty() || p.address.isEmpty() || (answer == "reply" && text.isBlank())) { done(); return }
        val nm = NotificationManagerCompat.from(context)
        val id = terminal.hashCode()
        val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        // A receiver gets about 10 s; a reply can wait longer for the Mac to
        // type it. The watch service keeps the process alive past that.
        val finished = AtomicBoolean(false)
        val finish = { if (finished.compareAndSet(false, true)) done() }
        scope.launch { delay(8_000); finish() }
        scope.launch {
            try {
                Api(p.liveRoutes()).answer(terminal, seq, answer, text, option)
                // The agent moves on and the watch takes the notification away;
                // this is for a reply, whose notification would spin until then.
                nm.cancel(id)
            } catch (e: Exception) {
                @SuppressLint("MissingPermission")  // it posted the notification answered here
                if (Watch.canNotify(context)) nm.notify(id, problem(context, terminal, title, e.advice()))
            } finally {
                finish()
            }
        }
    }

    /** Replaces the notification when the answer didn't get through. */
    private fun problem(context: Context, terminal: String, title: String, why: String) =
        NotificationCompat.Builder(context, Watch.CH_NEEDS)
            .setSmallIcon(R.drawable.ic_stat_tag)
            .setColor(0xFFF7768E.toInt())
            .setContentTitle(title.ifBlank { terminal })
            .setContentText("Not answered: $why")
            .setStyle(NotificationCompat.BigTextStyle().bigText("Not answered: $why"))
            .setSubText(terminal)
            .setSilent(true)
            .setAutoCancel(true)
            .setContentIntent(
                PendingIntent.getActivity(
                    context, terminal.hashCode(),
                    Intent(context, MainActivity::class.java)
                        .putExtra(Watch.EXTRA_TERMINAL, terminal)
                        .addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP),
                    PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
                ),
            )
            .build()
}

class AnswerReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action?.startsWith(Answer.ACTION) != true) return
        val pending = goAsync()  // up to about 10 s; the call times out before
        Answer.handle(context, intent) { pending.finish() }
    }
}
