package dev.akbxr.herdrremote

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp

/**
 * Whether the app is on screen. Polls key on it, so nothing asks the Mac
 * anything while the phone is in a pocket.
 */
@Composable
fun rememberOnScreen(): Boolean {
    val owner = LocalLifecycleOwner.current
    var on by remember { mutableStateOf(owner.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) }
    DisposableEffect(owner) {
        val observer = LifecycleEventObserver { _, _ -> on = owner.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED) }
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer) }
    }
    return on
}

/** The main action on a screen: ink on night, so yellow stays for "needs you". */
@Composable
fun PrimaryButton(text: String, enabled: Boolean = true, modifier: Modifier = Modifier, onClick: () -> Unit) {
    Box(
        modifier
            .heightIn(min = 48.dp)
            .clip(RoundedCornerShape(12.dp))
            .background(if (enabled) Ink.text else Ink.pen)
            .clickable(enabled = enabled, role = Role.Button, onClick = onClick)
            .padding(horizontal = 20.dp, vertical = 12.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(text, style = Type.label.copy(fontSize = Type.body.fontSize), color = if (enabled) Ink.night else Ink.dust)
    }
}

/** A quiet text button for secondary actions. */
@Composable
fun QuietButton(text: String, color: Color = Ink.dust, modifier: Modifier = Modifier, onClick: () -> Unit) {
    Box(
        modifier
            .heightIn(min = 40.dp)
            .clip(RoundedCornerShape(10.dp))
            .clickable(role = Role.Button, onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 8.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(text, style = Type.label, color = color)
    }
}

@Composable
fun Field(
    label: String,
    value: String,
    onValueChange: (String) -> Unit,
    placeholder: String,
    secret: Boolean = false,
    keyboard: KeyboardType = KeyboardType.Text,
    style: TextStyle = Type.body,
    transformation: VisualTransformation? = null,
    onDone: (() -> Unit)? = null,  // the keyboard's done key
) {
    Column(Modifier.fillMaxWidth()) {
        Text(label, style = Type.label, color = Ink.dust)
        Spacer(Modifier.height(6.dp))
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            singleLine = true,
            textStyle = style.copy(color = Ink.text),
            cursorBrush = SolidColor(Ink.yellow),
            visualTransformation = transformation ?: if (secret) PasswordVisualTransformation() else VisualTransformation.None,
            keyboardOptions = KeyboardOptions(
                keyboardType = keyboard,
                autoCorrectEnabled = false,
                imeAction = if (onDone != null) ImeAction.Done else ImeAction.Default,
            ),
            keyboardActions = KeyboardActions(onDone = { onDone?.invoke() }),
            modifier = Modifier.fillMaxWidth(),
            decorationBox = { inner ->
                Box(
                    Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(12.dp))
                        .background(Ink.pen)
                        .border(1.dp, Ink.rule, RoundedCornerShape(12.dp))
                        .padding(horizontal = 14.dp, vertical = 13.dp),
                ) {
                    if (value.isEmpty()) Text(placeholder, style = style, color = Ink.dust.copy(alpha = 0.7f))
                    inner()
                }
            },
        )
    }
}

/** A problem, said as what to do. */
@Composable
fun Notice(text: String, modifier: Modifier = Modifier, action: Pair<String, () -> Unit>? = null) {
    Row(
        modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(12.dp))
            .background(Ink.red.copy(alpha = 0.12f))
            .padding(horizontal = 14.dp, vertical = 12.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Box(Modifier.padding(top = 6.dp).width(6.dp).height(6.dp).clip(RoundedCornerShape(3.dp)).background(Ink.red))
        Spacer(Modifier.width(10.dp))
        Text(text, style = Type.small, color = Ink.text, modifier = Modifier.weight(1f))
        action?.let { (label, run) ->
            Spacer(Modifier.width(10.dp))
            Text(
                label, style = Type.label, color = Ink.text,
                modifier = Modifier.clip(RoundedCornerShape(8.dp)).clickable(onClick = run).padding(horizontal = 6.dp, vertical = 2.dp),
            )
        }
    }
}

/** Two sections at the bottom; the herd carries the needs-you count. */
@Composable
fun BottomBar(items: List<Pair<String, Boolean>>, badge: String?, onSelect: (Int) -> Unit) {
    Column {
        Box(Modifier.fillMaxWidth().height(1.dp).background(Ink.rule))
        Row(Modifier.fillMaxWidth().height(56.dp), horizontalArrangement = Arrangement.SpaceEvenly) {
            items.forEachIndexed { index, (label, selected) ->
                Box(
                    Modifier
                        .weight(1f)
                        .height(56.dp)
                        .clickable(role = Role.Tab) { onSelect(index) },
                    contentAlignment = Alignment.Center,
                ) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(label, style = Type.label.copy(fontSize = Type.body.fontSize), color = if (selected) Ink.text else Ink.dust)
                        if (index == 0 && badge != null) {
                            Spacer(Modifier.width(8.dp))
                            Box(
                                Modifier.clip(RoundedCornerShape(6.dp)).background(Ink.yellow).padding(horizontal = 6.dp, vertical = 1.dp),
                            ) { Text(badge, style = Type.tagSmall, color = Ink.night) }
                        }
                    }
                    if (selected) {
                        Box(
                            Modifier.align(Alignment.TopCenter).width(28.dp).height(2.dp).background(Ink.text),
                        )
                    }
                }
            }
        }
    }
}
