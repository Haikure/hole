plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.android.library) apply false
    // AGP 9 内置 Kotlin；这里只锁定 KGP 版本，不再向 Android 模块应用旧插件。
    alias(libs.plugins.kotlin.android) apply false
    alias(libs.plugins.kotlin.compose) apply false
}

subprojects {
    tasks.withType<Test>().configureEach {
        // Robolectric 的 API 36 ApplicationSharedMemory 使用宿主 FileDescriptor 接口。
        // 仅开放给测试 JVM，不改变 Android 应用权限或运行时参数。
        jvmArgs("--add-exports=java.base/jdk.internal.access=ALL-UNNAMED")
    }
}
