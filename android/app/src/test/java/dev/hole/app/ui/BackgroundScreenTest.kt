package dev.hole.app.ui

import android.app.Activity
import android.content.Intent
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.assertIsOn
import androidx.compose.ui.test.junit4.v2.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performSemanticsAction
import dev.hole.app.FloatingBallPreferences
import dev.hole.app.FloatingBallService
import dev.hole.app.RunStateStore
import dev.hole.app.config.ThemeMode
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import org.robolectric.annotation.LooperMode
import org.robolectric.shadows.ShadowSettings
import kotlin.test.assertEquals
import kotlin.test.assertFalse

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
@LooperMode(LooperMode.Mode.PAUSED)
class BackgroundScreenTest {
    @get:Rule val compose = createAndroidComposeRule<ComponentActivity>()
    private val context get() = RuntimeEnvironment.getApplication()

    @Test fun missingPermissionOpensSystemAuthorizationAndKeepsSwitchOff() {
        FloatingBallPreferences(context).setEnabled(false)
        ShadowSettings.setCanDrawOverlays(false)
        compose.setContent { HoleTheme(ThemeMode.LIGHT, dynamic = false) { BackgroundScreen(onBack = {}) } }
        compose.onNodeWithContentDescription("开启悬浮球").performClick().assertIsOff()
        compose.runOnIdle {
            val intent = shadowOf(context).nextStartedActivity
            assertEquals(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, intent.action)
            assertEquals("package:${context.packageName}", intent.dataString)
            assertFalse(FloatingBallPreferences(context).isEnabled())
        }
    }

    @Test fun enablingAndResizingOnlyStartsTheFloatingBallService() {
        FloatingBallPreferences(context).apply { setEnabled(false); setSizeDp(48) }
        RunStateStore(context).setRequested(false)
        ShadowSettings.setCanDrawOverlays(true)
        compose.setContent { HoleTheme(ThemeMode.LIGHT, dynamic = false) { BackgroundScreen(onBack = {}) } }
        compose.onNodeWithContentDescription("开启悬浮球").performClick()
        compose.onNodeWithContentDescription("悬浮球大小").performSemanticsAction(SemanticsActions.SetProgress) { it(80f) }
        compose.runOnIdle {
            assertEquals(80, FloatingBallPreferences(context).sizeDp())
            assertEquals(FloatingBallService::class.java.name, shadowOf(context).nextStartedService.component!!.className)
            assertFalse(RunStateStore(context).isRequested())
        }
        compose.onNodeWithContentDescription("开启悬浮球").performClick().assertIsOff()
        compose.runOnIdle { assertFalse(FloatingBallPreferences(context).isEnabled()) }
    }

    @Test fun returningWithPermissionEnablesTheBallButDenialKeepsItOff() {
        FloatingBallPreferences(context).setEnabled(false)
        ShadowSettings.setCanDrawOverlays(false)
        compose.setContent { HoleTheme(ThemeMode.LIGHT, dynamic = false) { BackgroundScreen(onBack = {}) } }
        compose.onNodeWithContentDescription("开启悬浮球").performClick()
        compose.runOnIdle {
            val request = shadowOf(compose.activity).nextStartedActivityForResult
            shadowOf(compose.activity).receiveResult(request.intent, Activity.RESULT_CANCELED, Intent())
        }
        compose.onNodeWithContentDescription("开启悬浮球").assertIsOff().performClick()
        compose.runOnIdle {
            val request = shadowOf(compose.activity).nextStartedActivityForResult
            ShadowSettings.setCanDrawOverlays(true)
            // Overlay settings often return RESULT_CANCELED even after a grant.
            shadowOf(compose.activity).receiveResult(request.intent, Activity.RESULT_CANCELED, Intent())
        }
        compose.onNodeWithContentDescription("开启悬浮球").assertIsOn()
        compose.runOnIdle {
            assertEquals(FloatingBallService::class.java.name, shadowOf(context).nextStartedService.component!!.className)
        }
    }
}
