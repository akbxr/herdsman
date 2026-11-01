package dev.akbxr.herdrremote

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
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.material3.Text

/**
 * The shimmer: a band of light sweeping left to right, for what is still
 * happening (an agent thinking, a command running) and for what is still on
 * its way (a skeleton while the chat or the changes load).
 *
 * Off in snapshots (LocalShimmer false): a frame of a moving band is noise.
 */
val LocalShimmer = compositionLocalOf { true }

private const val BAND = 360f   // px: the light band's width
private const val SWEEP_MS = 1400

@Composable
private fun sweep(): Float {
    if (!LocalShimmer.current) return -BAND
    val t = rememberInfiniteTransition(label = "shimmer")
    val x by t.animateFloat(
        initialValue = -BAND,
        targetValue = 1400f,
        animationSpec = infiniteRepeatable(tween(SWEEP_MS, easing = LinearEasing), RepeatMode.Restart),
        label = "sweep",
    )
    return x
}

/** A brush whose light band moves: base color with a brighter band over it. */
@Composable
fun shimmerBrush(base: Color, light: Color): Brush {
    val x = sweep()
    return Brush.linearGradient(
        colors = listOf(base, base, light, base, base),
        start = Offset(x - BAND, 0f),
        end = Offset(x + BAND, 0f),
    )
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
    Text(
        text,
        style = style.copy(brush = shimmerBrush(base, light)),
        maxLines = maxLines,
        overflow = TextOverflow.Ellipsis,
        modifier = modifier,
    )
}

/** A grey bar standing in for a line still loading. */
@Composable
fun SkeletonBar(modifier: Modifier = Modifier, height: Int = 14) {
    Box(
        modifier
            .height(height.dp)
            .clip(RoundedCornerShape(6.dp))
            .background(shimmerBrush(Ink.pen, Ink.rule)),
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
