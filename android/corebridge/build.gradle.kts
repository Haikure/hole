plugins {
    alias(libs.plugins.android.library)
}

android {
    namespace = "dev.hole.corebridge"
    compileSdk = 37
    buildToolsVersion = "36.0.0"
    defaultConfig { minSdk = 26 }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    testOptions.unitTests.isIncludeAndroidResources = true
}

kotlin {
    compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17) }
}

val aar = layout.projectDirectory.file("libs/holecore.aar")
val bindGoCore by tasks.registering(Exec::class) {
    group = "build"
    description = "Build armeabi-v7a / arm64-v8a AAR from the current shared Go Core."
    workingDir(rootProject.projectDir.parentFile)
    commandLine("bash", rootProject.file("scripts/build-core.sh").absolutePath, aar.asFile.absolutePath)
    inputs.files(fileTree(rootProject.file("../core")) { include("**/*.go"); exclude("**/*_test.go") })
    inputs.files(fileTree(rootProject.file("../mobile")) { include("**/*.go"); exclude("**/*_test.go") })
    inputs.files(rootProject.file("../go.mod"), rootProject.file("../go.sum"))
    inputs.files(fileTree("gobuild") { include("*.go", "go.mod", "go.sum") })
    inputs.file(rootProject.file("scripts/build-core.sh"))
    inputs.files(rootProject.file("../scripts/build-env.sh"), rootProject.file("../scripts/build_meta.py"))
    inputs.file(rootProject.file("../core/compat/anet/go.mod"))
    inputs.property("coreVersion", providers.exec {
        commandLine("python3", rootProject.file("../scripts/build_meta.py").absolutePath, "version")
    }.standardOutput.asText.map { it.trim() })
    outputs.file(aar)
}

tasks.named("preBuild") { dependsOn(bindGoCore) }

dependencies {
    // The APK includes the generated AAR directly. compileOnly avoids attempting
    // to embed a local AAR inside this Android library's own AAR.
    compileOnly(files(aar).builtBy(bindGoCore))
    implementation(libs.kotlinx.coroutines.android)
    testImplementation(files(aar).builtBy(bindGoCore))
    testImplementation(libs.kotlin.test.junit)
    testImplementation(libs.kotlinx.coroutines.test)
    testImplementation(libs.robolectric)
}
