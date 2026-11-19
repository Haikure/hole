import org.jetbrains.compose.desktop.application.dsl.TargetFormat
import java.awt.image.BufferedImage
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder
import javax.imageio.ImageIO

plugins {
    kotlin("jvm") version "2.4.0"
    kotlin("plugin.serialization") version "2.4.0"
    id("org.jetbrains.kotlin.plugin.compose") version "2.4.0"
    id("org.jetbrains.compose") version "1.12.2"
}

val repositoryRoot = rootDir.resolve("../..").canonicalFile
val releaseVersions = groovy.json.JsonSlurper().parse(repositoryRoot.resolve("versions.json")) as Map<*, *>
version = (((releaseVersions["frontends"] as Map<*, *>)["desktop_gui"] as Map<*, *>)["version"] as String)
val nativeName = if (System.getProperty("os.name").startsWith("Windows")) "holecore.dll" else "libholecore.so"
val desktopBash = providers.environmentVariable("HOLE_DESKTOP_BASH").orNull ?: if (nativeName.endsWith(".dll")) {
    File(System.getenv("ProgramFiles") ?: "C:/Program Files", "Git/bin/bash.exe").absolutePath
} else "bash"
val desktopJavaHome = System.getProperty("java.home")
val desktopJavaVersion = JavaVersion.current()
require(desktopJavaVersion.isCompatibleWith(JavaVersion.VERSION_21)) {
    "桌面构建使用完整 JDK 21+（支持 JDK 25）；请使用 ./build.sh desktop 自动选择，或设置 JAVA_HOME。"
}

kotlin {
    // Use the JDK running Gradle, including JDK 25. Do not require an installed JDK 21.
    compilerOptions { jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_21) }
    sourceSets.named("main") {
        // One source, three clients. Android-only entry points/adapters are not compiled here.
        kotlin.srcDir(repositoryRoot.resolve("android/app/src/main/kotlin"))
        kotlin.srcDir(repositoryRoot.resolve("android/corebridge/src/main/kotlin"))
        kotlin.include("dev/hole/desktop/**", "dev/hole/app/BuildConfig.kt", "dev/hole/app/ConfigUiState.kt")
        kotlin.include("dev/hole/app/ui/*.kt", "dev/hole/app/config/*.kt", "dev/hole/corebridge/*.kt")
        kotlin.exclude("**/Android*.kt", "**/PlatformUi.kt", "**/WearLayout.kt", "**/ConfigRepository.kt", "**/SecretCipher.kt")
        kotlin.exclude("**/BackgroundScreen.kt", "**/ConfigTransferScreen.kt", "**/ReleaseNotes.kt", "**/ReleaseUpdateScreen.kt")
        kotlin.exclude("dev/hole/corebridge/CoreClient.kt", "dev/hole/corebridge/VoiceAudio.kt")
    }
}
java {
    sourceCompatibility = JavaVersion.VERSION_21
    targetCompatibility = JavaVersion.VERSION_21
}

dependencies {
    implementation(compose.desktop.currentOs)
    implementation("org.jetbrains.compose.material3:material3:1.12.0-alpha03")
    implementation("org.jetbrains.compose.material:material-icons-extended:1.7.3")
    implementation("org.jetbrains.androidx.lifecycle:lifecycle-runtime-compose:2.11.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-swing:1.11.0")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.9.0")
    implementation("org.json:json:20250517")
    testImplementation(kotlin("test-junit"))
}

val buildNative by tasks.registering(Exec::class) {
    workingDir(repositoryRoot)
    commandLine(desktopBash, "desktop/scripts/build-native.sh")
    inputs.files(fileTree(repositoryRoot.resolve("core")) { include("**/*.go"); exclude("**/*_test.go") })
    inputs.files(fileTree(repositoryRoot.resolve("mobile")) { include("**/*.go"); exclude("**/*_test.go") })
    inputs.files(fileTree(repositoryRoot.resolve("desktop/native")))
    inputs.files(repositoryRoot.resolve("go.mod"), repositoryRoot.resolve("go.sum"), repositoryRoot.resolve("versions.json"),
        repositoryRoot.resolve("scripts/build_meta.py"), repositoryRoot.resolve("scripts/build-env.sh"),
        repositoryRoot.resolve("android/corebridge/gobuild/go.mod"), repositoryRoot.resolve("android/corebridge/gobuild/go.sum"),
        repositoryRoot.resolve("core/compat/anet/go.mod"), repositoryRoot.resolve("desktop/scripts/build-native.sh"),
        repositoryRoot.resolve("desktop/scripts/toolchain.sh"), repositoryRoot.resolve("desktop/scripts/toolchain.py"))
    inputs.property("coreBuildID", providers.exec {
        workingDir(repositoryRoot)
        commandLine("python3", "scripts/build_meta.py", "build-id")
    }.standardOutput.asText.map { it.trim() })
    inputs.property("javaHome", desktopJavaHome)
    inputs.property("desktopBash", desktopBash)
    // JNI, Kotlin, the runtime image and packaging must use the same JDK.
    environment("HOLE_DESKTOP_JAVA_HOME", desktopJavaHome)
    outputs.file(layout.buildDirectory.file("native/$nativeName"))
}

tasks.processResources {
    dependsOn(buildNative)
    from(repositoryRoot.resolve("desktop/gui/ui/assets/app-icon.png"))
}

compose.desktop {
    application {
        mainClass = "dev.hole.desktop.MainKt"
        javaHome = desktopJavaHome
        jvmArgs += listOf("--enable-native-access=ALL-UNNAMED", "-Dfile.encoding=UTF-8")
        nativeDistributions {
            appResourcesRootDir.set(layout.buildDirectory.dir("app-resources"))
            targetFormats(TargetFormat.Exe)
            packageName = "Hole"
            packageVersion = project.version.toString()
            description = "Hole 桌面客户端"
            vendor = "Hole"
            licenseFile.set(repositoryRoot.resolve("LICENSE"))
            modules("java.desktop", "java.net.http", "jdk.unsupported", "jdk.crypto.ec")
            windows {
                iconFile.set(layout.buildDirectory.file("app-resources/hole.ico"))
                console = false
                perUserInstall = true
                shortcut = true
                menu = true
                menuGroup = "Hole"
                upgradeUuid = "b16bd0ab-667c-47b8-8bd7-168b909b391b"
            }
            linux { iconFile.set(repositoryRoot.resolve("desktop/gui/ui/assets/app-icon.png")) }
        }
        buildTypes.release.proguard {
            version.set("7.8.0")
            configurationFiles.from(project.file("proguard-rules.pro"))
        }
    }
}

// Run and package the same native library; do not load a library from PATH.
tasks.withType<JavaExec>().configureEach {
    dependsOn(buildNative)
    systemProperty("hole.native.path", layout.buildDirectory.file("native/$nativeName").get().asFile.absolutePath)
    systemProperty("hole.desktop.version", project.version.toString())
}
tasks.test {
    dependsOn(buildNative)
    systemProperty("hole.native.path", layout.buildDirectory.file("native/$nativeName").get().asFile.absolutePath)
}
tasks.register<Sync>("prepareDesktopResources") {
    dependsOn(buildNative)
    from(layout.buildDirectory.dir("native")) { include(nativeName) }
    into(layout.buildDirectory.dir("app-resources/common"))
    inputs.file(repositoryRoot.resolve("desktop/gui/ui/assets/app-icon.png"))
    inputs.property("desktopVersion", project.version.toString())
    outputs.file(layout.buildDirectory.file("app-resources/hole.ico"))
    doLast {
        destinationDir.resolve("desktop-version.txt").writeText(project.version.toString())
        // Windows ICO can contain a PNG payload. Generate it from the shared app icon.
        val source = ImageIO.read(repositoryRoot.resolve("desktop/gui/ui/assets/app-icon.png"))
        val scaled = BufferedImage(256, 256, BufferedImage.TYPE_INT_ARGB)
        val graphics = scaled.createGraphics()
        try { graphics.drawImage(source, 0, 0, 256, 256, null) } finally { graphics.dispose() }
        val png = ByteArrayOutputStream().also { ImageIO.write(scaled, "png", it) }.toByteArray()
        val ico = ByteBuffer.allocate(22 + png.size).order(ByteOrder.LITTLE_ENDIAN)
        ico.putShort(0.toShort()).putShort(1.toShort()).putShort(1.toShort())
        ico.put(0.toByte()).put(0.toByte()).put(0.toByte()).put(0.toByte())
        ico.putShort(1.toShort()).putShort(32.toShort()).putInt(png.size).putInt(22).put(png)
        layout.buildDirectory.file("app-resources/hole.ico").get().asFile.writeBytes(ico.array())
    }
}
tasks.matching { it.name == "prepareAppResources" || it.name.contains("Distributable") || it.name.startsWith("package") || it.name.startsWith("validate") }.configureEach {
    dependsOn("prepareDesktopResources")
}
