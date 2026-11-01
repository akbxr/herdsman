package dev.akbxr.herdsman

import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.CompositingStrategy
import androidx.compose.ui.graphics.BlendMode
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.draw.drawBehind
import androidx.compose.runtime.State
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.material3.Text
import kotlin.math.PI
import kotlin.math.sin

/**
 * The shimmer: a band of light sweeping left to right, for what is still
 * happening (an agent thinking, a command running) and for what is still on
 * its way (a skeleton while the chat or the changes load).
 *
 * Off in snapshots (LocalShimmer false): a frame of a moving band is noise.
 */
val LocalShimmer = compositionLocalOf { true }

private const val BAND = 360f   // px: the light band's width
private const val SWEEP_MS = 2400
private const val PULSE_MS = 1800

/**
 * Where the band is, as a State read only while drawing: the band moves on
 * every frame without recomposing (or relaying out) what it lights. Null when
 * the shimmer is off.
 */
@Composable
private fun sweep(): State<Float>? {
    if (!LocalShimmer.current) return null
    val t = rememberInfiniteTransition(label = "shimmer")
    return t.animateFloat(
        initialValue = -BAND,
        targetValue = 1400f,
        animationSpec = infiniteRepeatable(tween(SWEEP_MS, easing = LinearEasing), RepeatMode.Restart),
        label = "sweep",
    )
}

private fun band(x: Float, base: Color, light: Color) = Brush.linearGradient(
    colors = listOf(base, base, light, base, base),
    start = Offset(x - BAND, 0f),
    end = Offset(x + BAND, 0f),
)

/** A background whose light band moves: base color with a brighter band over it. */
@Composable
fun Modifier.shimmerBackground(base: Color, light: Color): Modifier {
    val x = sweep() ?: return this.background(base)
    return this.drawBehind { drawRect(band(x.value, base, light)) }
}

/** Text with the band moving through it ("Thinking…", "Running go test…"). */
@Composable
fun ShimmerText(
    text: String,
    style: TextStyle,
    base: Color,
    modifier: Modifier = Modifier,
    light: Color = Color.White,
    maxLines: Int = 1,
) {
    val x = sweep()
    Text(
        text,
        style = style,
        color = base,
        maxLines = maxLines,
        overflow = TextOverflow.Ellipsis,
        // The band painted over the glyphs only (SrcAtop on an offscreen layer),
        // in the draw phase: the text is laid out once, not on every frame.
        modifier = if (x == null) modifier else modifier
            .graphicsLayer { compositingStrategy = CompositingStrategy.Offscreen }
            .drawWithContent {
                drawContent()
                drawRect(band(x.value, Color.Transparent, light), blendMode = BlendMode.SrcAtop)
            },
    )
}

/**
 * A dot that breathes: a soft ring swells out of it and fades while the dot
 * itself dims a little and comes back. Still in snapshots (LocalShimmer
 * false). The ring is drawn outside the dot's bounds: it takes no room.
 */
@Composable
fun PulseDot(color: Color, modifier: Modifier = Modifier, size: Dp = 8.dp) {
    val animate = LocalShimmer.current
    Box(modifier.size(size), contentAlignment = androidx.compose.ui.Alignment.Center) {
        if (!animate) {
            Box(Modifier.size(size).clip(CircleShape).background(color))
            return@Box
        }
        val t = rememberInfiniteTransition(label = "pulse")
        // Read only in the layers' blocks: the dot redraws, nothing recomposes.
        val p = t.animateFloat(
            initialValue = 0f,
            targetValue = 1f,
            animationSpec = infiniteRepeatable(tween(PULSE_MS, easing = LinearEasing), RepeatMode.Restart),
            label = "pulse",
        )
        Box(
            Modifier
                .size(size)
                .graphicsLayer {
                    val ring = FastOutSlowInEasing.transform(p.value)
                    scaleX = 1f + 1.3f * ring
                    scaleY = 1f + 1.3f * ring
                    alpha = 0.45f * (1f - ring)
                }
                .clip(CircleShape)
                .background(color),
        )
        Box(
            Modifier
                .size(size)
                .graphicsLayer {
                    val breath = sin(PI.toFloat() * p.value) // 0 → 1 → 0 over a pulse
                    scaleX = 1f - 0.12f * breath
                    scaleY = 1f - 0.12f * breath
                    alpha = 1f - 0.3f * breath
                }
                .clip(CircleShape)
                .background(color),
        )
    }
}

/** A grey bar standing in for a line still loading. */
@Composable
fun SkeletonBar(modifier: Modifier = Modifier, height: Int = 14) {
    Box(
        modifier
            .height(height.dp)
            .clip(RoundedCornerShape(6.dp))
            .shimmerBackground(Ink.pen, Ink.rule),
    )
}

/** A few skeleton messages, while a conversation or a diff loads. */
@Composable
fun SkeletonList(modifier: Modifier = Modifier) {
    Column(modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Box(Modifier.fillMaxWidth(), contentAlignment = androidx.compose.ui.Alignment.CenterEnd) {
            SkeletonBar(Modifier.fillMaxWidth(0.55f), height = 38)   // your message, on the right
        }
        SkeletonBar(Modifier.fillMaxWidth(0.92f))
        SkeletonBar(Modifier.fillMaxWidth(0.8f))
        SkeletonBar(Modifier.fillMaxWidth(0.86f), height = 34)
        SkeletonBar(Modifier.fillMaxWidth(0.7f))
        SkeletonBar(Modifier.fillMaxWidth(0.9f))
    }
}

/** For snapshots: the same screens, the band left out. */
@Composable
fun NoShimmer(content: @Composable () -> Unit) = CompositionLocalProvider(LocalShimmer provides false, content = content)
