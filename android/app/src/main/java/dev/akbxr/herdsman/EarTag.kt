package dev.akbxr.herdsman

import android.provider.Settings
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.keyframes
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.RoundRect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.PathOperation
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

fun State.tagColor(): Color = when (this) {
    State.NeedsYou -> Ink.yellow
    State.Working -> Ink.blue
    State.Done -> Ink.mint
    State.Idle -> Ink.dust
}

// Proportions of one tag, as fractions of its width.
private const val HEIGHT = 1.14f    // height / width
private const val NECK_W = 0.40f
private const val BODY_TOP = 0.40f  // where the neck meets the body
private const val FLARE = 0.12f     // the curve from neck into body
private const val HOLE_R = 0.075f

/**
 * The tag's outline: a rounded body, and a tall neck that flares into it
 * with a round top, the hole punched through the neck.
 */
private fun tagPath(w: Float, h: Float): Path {
    val neckW = w * NECK_W
    val left = (w - neckW) / 2
    val right = left + neckW
    val top = w * BODY_TOP
    val f = w * FLARE
    val r = w * 0.2f
    val outline = Path().apply {
        addRoundRect(RoundRect(Rect(0f, top, w, h), CornerRadius(r)))
        addRoundRect(RoundRect(Rect(left, 0f, right, top + r), CornerRadius(neckW / 2)))
    }
    // Concave fillets where the neck meets the body's top edge.
    val fillets = Path().apply {
        moveTo(left - f, top + 0.5f); quadraticTo(left, top, left, top - f); lineTo(left, top + r); lineTo(left - f, top + r); close()
        moveTo(right + f, top + 0.5f); quadraticTo(right, top, right, top - f); lineTo(right, top + r); lineTo(right + f, top + r); close()
    }
    val hole = Path().apply { addOval(Rect(Offset(w / 2, neckW / 2), w * HOLE_R)) }
    val solid = Path.combine(PathOperation.Union, outline, fillets)
    return Path.combine(PathOperation.Difference, solid, hole)
}

/**
 * An ear tag stamped with a terminal's number. Its color is the agent's
 * state; when it turns yellow (needs you) it swings once on its hole.
 */
@Composable
fun EarTag(agent: Agent, width: Dp = 58.dp, modifier: Modifier = Modifier) {
    val color = agent.state.tagColor()
    val angle = remember(agent.terminal) { Animatable(0f) }
    val context = LocalContext.current
    val animationsOn = remember {
        Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) > 0f
    }
    LaunchedEffect(agent.state) {
        // Swing only on a change to "needs you" seen while the app is open,
        // not for a tag that already needed you when it first showed.
        val before = seen[agent.terminal]
        seen[agent.terminal] = agent.state
        if (agent.state == State.NeedsYou && animationsOn && before != null && before != State.NeedsYou) {
            angle.animateTo(0f, keyframes {
                durationMillis = 1100
                16f at 140
                -11f at 360
                7f at 560
                -3.5f at 760
                1.2f at 930
            })
        }
    }

    Box(
        modifier
            .size(width, width * HEIGHT)
            .graphicsLayer {
                rotationZ = angle.value
                // The tag hangs from its hole.
                transformOrigin = TransformOrigin(0.5f, (NECK_W / 2) / HEIGHT)
            },
    ) {
        Canvas(Modifier.fillMaxSize()) {
            val w = size.width
            val h = size.height
            drawPath(tagPath(w, h), color)
            // Molded rim: a faint inset line around the body, like pressed plastic.
            val inset = w * 0.075f
            drawRoundRect(
                color = Color.Black.copy(alpha = 0.14f),
                topLeft = Offset(inset, w * BODY_TOP + inset),
                size = Size(w - inset * 2, h - w * BODY_TOP - inset * 2),
                cornerRadius = CornerRadius(w * 0.13f),
                style = Stroke(width = w * 0.025f),
            )
        }
        // Too small to stamp: the number is beside it (agent screen heading).
        if (width >= 40.dp) Text(
            agent.tagNumber,
            style = if (agent.tagNumber.length > 4) Type.tagSmall else Type.tag,
            color = Ink.night,
            textAlign = TextAlign.Center,
            maxLines = 1,
            // Centered in the body, below the neck.
            modifier = Modifier.align(Alignment.BottomCenter).padding(bottom = width * (HEIGHT - BODY_TOP) / 2 - width * 0.135f),
        )
    }
}

// Which state each tag was last shown in, so a tag that already needed you
// when the app opened doesn't swing, but one that changes while you watch does.
private val seen = mutableMapOf<String, State>()
