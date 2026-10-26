package dev.hole.app

import android.content.Context
import androidx.core.content.edit

/** Local window preferences; enabling the ball never requests an engine run. */
class FloatingBallPreferences(context: Context) {
    private val preferences = context.applicationContext.getSharedPreferences("floating_ball", Context.MODE_PRIVATE)

    fun isEnabled(): Boolean = preferences.getBoolean("enabled", false)
    fun setEnabled(enabled: Boolean) { preferences.edit(commit = true) { putBoolean("enabled", enabled) } }
    fun sizeDp(): Int = preferences.getInt("size_dp", DEFAULT_SIZE_DP).coerceIn(MIN_SIZE_DP, MAX_SIZE_DP)
    fun setSizeDp(size: Int) { preferences.edit { putInt("size_dp", size.coerceIn(MIN_SIZE_DP, MAX_SIZE_DP)) } }
    fun position(): Pair<Float, Float> =
        preferences.getFloat("position_x", 1f).coerceIn(0f, 1f) to
            preferences.getFloat("position_y", 0.35f).coerceIn(0f, 1f)

    fun setPosition(x: Float, y: Float) {
        preferences.edit {
            putFloat("position_x", x.coerceIn(0f, 1f))
            putFloat("position_y", y.coerceIn(0f, 1f))
        }
    }

    companion object {
        const val MIN_SIZE_DP = 32
        const val MAX_SIZE_DP = 96
        const val DEFAULT_SIZE_DP = 48
    }
}
