plugins {
    alias(libs.plugins.android.application)
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

val signingValues = listOf("HOLE_SIGNING_STORE_FILE", "HOLE_SIGNING_STORE_PASSWORD", "HOLE_SIGNING_KEY_ALIAS", "HOLE_SIGNING_KEY_PASSWORD")
    .associateWith { providers.environmentVariable(it).orNull }
val releaseSigningConfigured = signingValues.values.all { !it.isNullOrBlank() }

// Compose's release mapping task visits file dependencies during execution.
// A string builtBy(":corebridge:bindGoCore") re-enters Gradle project
// configuration at that point (state error/deadlock on Gradle 9.4.1).
// Resolve a TaskProvider during configuration and keep the real dependency.
val coreBridgeProject = evaluationDependsOn(":corebridge")
val coreBinding = coreBridgeProject.tasks.named("bindGoCore")

android {
    namespace = "dev.hole.app"
    compileSdk = 37
    buildToolsVersion = "36.0.0"
    ndkVersion = "28.2.13676358"
    defaultConfig {
        applicationId = "dev.hole.app"
        minSdk = 26
        targetSdk = 36
        versionCode = 8
        versionName = "0.3.6"
        ndk { abiFilters += listOf("arm64-v8a") }
    }
    buildFeatures { compose = true; buildConfig = true }
    signingConfigs {
        if (releaseSigningConfigured) create("release") {
            storeFile = file(requireNotNull(signingValues["HOLE_SIGNING_STORE_FILE"]))
            storePassword = signingValues["HOLE_SIGNING_STORE_PASSWORD"]
            keyAlias = signingValues["HOLE_SIGNING_KEY_ALIAS"]
            keyPassword = signingValues["HOLE_SIGNING_KEY_PASSWORD"]
        }
    }
    buildTypes {
        getByName("release") {
            if (releaseSigningConfigured) signingConfig = signingConfigs.getByName("release")
            isDebuggable = false
            isMinifyEnabled = true
            isShrinkResources = true
            ndk { debugSymbolLevel = "NONE" }
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    testOptions {
        unitTests {
            // Robolectric 需要真实资源与 Manifest（服务生命周期测试）。
            isIncludeAndroidResources = true
        }
    }
    packaging { jniLibs.useLegacyPackaging = false }
}

val verifyReleaseSigning by tasks.registering {
    doLast {
        require(releaseSigningConfigured) { "Release 构建需要设置四个 HOLE_SIGNING_* 环境变量；覆盖安装时指向现有签名文件。" }
        require(file(requireNotNull(signingValues["HOLE_SIGNING_STORE_FILE"])).isFile) { "签名文件不存在" }
    }
}
tasks.matching { it.name in setOf("packageRelease", "assembleRelease", "bundleRelease") }.configureEach { dependsOn(verifyReleaseSigning) }

kotlin {
    compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17) }
}

dependencies {
    implementation(project(":corebridge"))
    implementation(files(rootProject.file("corebridge/libs/holecore.aar")).builtBy(coreBinding))
    implementation(platform(libs.compose.bom))
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.lifecycle.runtime.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.compose.ui)
    implementation(libs.compose.ui.tooling.preview)
    implementation(libs.compose.material3)
    implementation(libs.compose.material.icons.core)
    implementation(libs.miuix.ui)
    implementation(libs.miuix.preference)
    implementation(libs.miuix.icons)
    implementation(libs.kotlinx.coroutines.android)
    implementation(libs.kotlinx.serialization.json)
    testImplementation(libs.kotlin.test.junit)
    testImplementation(libs.robolectric)
    testImplementation(libs.androidx.test.runner)
    testImplementation(libs.compose.ui.test.junit4)
    debugImplementation(libs.compose.ui.tooling)
    debugImplementation(libs.compose.ui.test.manifest)
}
