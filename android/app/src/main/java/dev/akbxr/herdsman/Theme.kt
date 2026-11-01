package dev.akbxr.herdsman

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

// Tokens from DESIGN.md (Tokyo Night Storm, as on the Mac).
object Ink {
    val night = Color(0xFF1F2335)   // background
    val pen = Color(0xFF292E42)     // raised surfaces
    val rule = Color(0xFF3B4261)    // hairlines
    val text = Color(0xFFC0CAF5)
    val dust = Color(0xFF737AA2)    // secondary text, idle tags
    val yellow = Color(0xFFE0AF68)  // needs you: the one loud color
    val blue = Color(0xFF7AA2F7)    // working
    val mint = Color(0xFF9ECE6A)    // done
    val red = Color(0xFFF7768E)     // errors
}

val Martian = FontFamily(
    Font(R.font.martian_mono_medium, FontWeight.Medium),
    Font(R.font.martian_mono_bold, FontWeight.Bold),
)
val Atkinson = FontFamily(
    Font(R.font.atkinson_regular, FontWeight.Normal),
    Font(R.font.atkinson_bold, FontWeight.Bold),
)
val JetBrains = FontFamily(
    Font(R.font.jetbrains_mono_regular, FontWeight.Normal),
    Font(R.font.jetbrains_mono_bold, FontWeight.Bold),
)

object Type {
    val headline = TextStyle(fontFamily = Martian, fontWeight = FontWeight.Bold, fontSize = 26.sp, lineHeight = 32.sp, letterSpacing = (-0.5).sp)
    val tag = TextStyle(fontFamily = Martian, fontWeight = FontWeight.Bold, fontSize = 15.sp, letterSpacing = (-0.3).sp)
    val tagSmall = TextStyle(fontFamily = Martian, fontWeight = FontWeight.Bold, fontSize = 11.sp)
    val title = TextStyle(fontFamily = Atkinson, fontWeight = FontWeight.Bold, fontSize = 18.sp, lineHeight = 22.sp)
    val body = TextStyle(fontFamily = Atkinson, fontWeight = FontWeight.Normal, fontSize = 15.sp, lineHeight = 21.sp)
    val label = TextStyle(fontFamily = Atkinson, fontWeight = FontWeight.Bold, fontSize = 13.sp, letterSpacing = 0.2.sp)
    val small = TextStyle(fontFamily = Atkinson, fontWeight = FontWeight.Normal, fontSize = 13.sp, lineHeight = 18.sp)
    val terminal = TextStyle(fontFamily = JetBrains, fontWeight = FontWeight.Normal, fontSize = 10.5.sp, lineHeight = 14.sp)
}

@Composable
fun HerdrTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = darkColorScheme(
            primary = Ink.text,
            onPrimary = Ink.night,
            background = Ink.night,
            onBackground = Ink.text,
            surface = Ink.night,
            onSurface = Ink.text,
            surfaceVariant = Ink.pen,
            onSurfaceVariant = Ink.dust,
            outline = Ink.rule,
            error = Ink.red,
        ),
        content = content,
    )
}
