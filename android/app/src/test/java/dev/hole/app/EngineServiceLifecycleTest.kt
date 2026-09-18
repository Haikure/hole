package dev.hole.app

import kotlin.test.Test
import kotlin.test.assertNotNull
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * EngineService 生命周期回归测试，SDK 固定为 26（Android 8.0，minSdk）。
 *
 * 背景：A2 曾在属性初始化里访问 applicationContext：
 *     private val configRepository = ConfigRepository(applicationContext)
 * Kotlin 的属性初始化在构造函数内执行，而框架是在构造之后才 attachBaseContext，
 * 此时 mBase 仍为 null → 构造期 NullPointerException → 服务实例化失败 → 应用启动即崩溃。
 * 该缺陷与系统版本无关，只在 Android 8 上被观察到。
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [26])
class EngineServiceLifecycleTest {

    @Test
    fun constructsWithoutAttachedContext() {
        // 构造期不得访问 Context；这里复刻框架的实例化顺序（先构造，后 attach）。
        val service = EngineService::class.java.getDeclaredConstructor().newInstance()
        assertNotNull(service)
    }

    @Test
    fun createsAndReachesOnCreateOnApi26() {
        val controller = Robolectric.buildService(EngineService::class.java)
        controller.create()
        assertNotNull(controller.get())
    }
}
