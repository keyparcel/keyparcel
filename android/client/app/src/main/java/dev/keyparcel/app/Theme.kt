package dev.keyparcel.app

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Typography
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight

// The shared artwork's petrol, amber and paper palette; keep native error colors.
private val colors = lightColorScheme(
    primary = Color(0xff103f4d),
    onPrimary = Color(0xfff7f5f0),
    primaryContainer = Color(0xffdae9e8),
    onPrimaryContainer = Color(0xff103f4d),
    inversePrimary = Color(0xffa7cdd0),
    secondary = Color(0xff76521b),
    onSecondary = Color.White,
    secondaryContainer = Color(0xfff4dfbc),
    onSecondaryContainer = Color(0xff382a12),
    tertiary = Color(0xff76521b),
    onTertiary = Color.White,
    tertiaryContainer = Color(0xfff4dfbc),
    onTertiaryContainer = Color(0xff382a12),
    background = Color(0xfff7f5f0),
    onBackground = Color(0xff1d292b),
    surface = Color(0xfff7f5f0),
    onSurface = Color(0xff1d292b),
    surfaceVariant = Color(0xffe2e8e4),
    onSurfaceVariant = Color(0xff455256),
    surfaceTint = Color(0xff103f4d),
    surfaceDim = Color(0xffd9dcd6),
    surfaceBright = Color(0xfffcfaf5),
    surfaceContainerLowest = Color.White,
    surfaceContainerLow = Color(0xfff2f3ed),
    surfaceContainer = Color(0xffecefe9),
    surfaceContainerHigh = Color(0xffe6eae4),
    surfaceContainerHighest = Color(0xffdfe5df),
    inverseSurface = Color(0xff24363b),
    inverseOnSurface = Color(0xfff7f5f0),
    outline = Color(0xff68777b),
    outlineVariant = Color(0xffbcc8c6),
)
private val typography = Typography().let {
    it.copy(titleLarge = it.titleLarge.copy(fontWeight = FontWeight.SemiBold),
        titleMedium = it.titleMedium.copy(fontWeight = FontWeight.SemiBold))
}

@Composable
internal fun KeyparcelTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = colors, typography = typography, content = content)
}
