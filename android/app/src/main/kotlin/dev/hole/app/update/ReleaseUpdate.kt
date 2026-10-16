package dev.hole.app.update

import java.math.BigInteger
import java.net.URI
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull

data class ReleaseAsset(
    val name: String,
    val downloadUrl: String,
    val size: Long,
)

data class GitHubRelease(
    val tag: String,
    val name: String,
    val notes: String,
    val pageUrl: String,
    val publishedAt: String,
    val assets: List<ReleaseAsset>,
) {
    val appApk: ReleaseAsset? get() = selectPhoneApk(assets)
}

data class ResolvedAccelerator(val prefix: String, val proxyApi: Boolean)

data class AcceleratorPreset(
    val id: String,
    val title: String,
    val summary: String,
    val prefix: String,
    val proxyApi: Boolean,
)

object ReleaseAccelerators {
    const val DIRECT = "direct"
    const val GH_PROXY = "gh-proxy"
    const val GHFAST = "ghfast"
    const val GHPROXY_NET = "ghproxy-net"
    const val CUSTOM = "custom"

    val presets = listOf(
        AcceleratorPreset(DIRECT, "直连 GitHub", "发行信息与 APK 均直连", "", proxyApi = false),
        AcceleratorPreset(GH_PROXY, "GH-Proxy", "gh-proxy.com · 加速发行信息与 APK", "https://gh-proxy.com/", proxyApi = true),
        AcceleratorPreset(GHFAST, "GHFast", "ghfast.top · 加速 APK，发行信息直连", "https://ghfast.top/", proxyApi = false),
        AcceleratorPreset(GHPROXY_NET, "ghproxy.net", "加速 APK，发行信息直连", "https://ghproxy.net/", proxyApi = false),
        AcceleratorPreset(CUSTOM, "自定义", "填写自有或其他服务的 HTTPS 前缀", "", proxyApi = true),
    )

    fun resolve(id: String, customPrefix: String): ResolvedAccelerator {
        val preset = presets.firstOrNull { it.id == id } ?: presets.first()
        val prefix = if (preset.id == CUSTOM) validateAcceleratorPrefix(customPrefix).orEmpty() else preset.prefix
        return ResolvedAccelerator(prefix, preset.proxyApi)
    }
}

object GitHubReleaseParser {
    private val json = Json { ignoreUnknownKeys = true }

    fun parseLatest(content: String): GitHubRelease {
        val root = json.parseToJsonElement(content) as? JsonObject
            ?: throw IllegalArgumentException("GitHub 返回的发行版数据格式无效")
        val tag = root.string("tag_name")
            ?: throw IllegalArgumentException("发行版缺少版本标签")
        val pageUrl = root.string("html_url")
            ?.takeIf(::isGitHubHttpsUrl)
            ?: throw IllegalArgumentException("发行版页面地址无效")
        val assets = (root["assets"] as? JsonArray).orEmpty().mapNotNull { element ->
            val asset = element as? JsonObject ?: return@mapNotNull null
            val name = asset.string("name") ?: return@mapNotNull null
            val downloadUrl = asset.string("browser_download_url")
                ?.takeIf(::isGitHubHttpsUrl) ?: return@mapNotNull null
            ReleaseAsset(name, downloadUrl, asset.long("size") ?: 0L)
        }
        return GitHubRelease(
            tag = tag,
            name = root.string("name")?.takeIf(String::isNotBlank) ?: tag,
            notes = root.string("body").orEmpty(),
            pageUrl = pageUrl,
            publishedAt = root.string("published_at").orEmpty(),
            assets = assets,
        )
    }

    private fun JsonObject.string(key: String): String? =
        (this[key] as? JsonPrimitive)?.contentOrNull

    private fun JsonObject.long(key: String): Long? =
        (this[key] as? JsonPrimitive)?.longOrNull
}

fun selectPhoneApk(assets: List<ReleaseAsset>): ReleaseAsset? = assets
    .mapIndexedNotNull { index, asset ->
        val name = asset.name.lowercase()
        if (!name.endsWith(".apk") || listOf("wear", "watch", "debug", "arm32", "armeabi-v7a").any(name::contains)) {
            return@mapIndexedNotNull null
        }
        val score = when {
            PHONE_RELEASE_APK.matches(name) -> 0
            name.contains("arm64") -> 1
            name == "app-release.apk" || listOf("phone", "android", "mobile").any(name::contains) -> 2
            else -> 2
        }
        Triple(score, index, asset)
    }
    .minWithOrNull(compareBy<Triple<Int, Int, ReleaseAsset>> { it.first }.thenBy { it.second })
    ?.third

private val PHONE_RELEASE_APK = Regex("""^hole[-_]v?\d+(?:\.\d+){1,3}[-_]release\.apk$""")

fun compareReleaseVersions(left: String, right: String): Int? {
    val a = parseVersion(left) ?: return null
    val b = parseVersion(right) ?: return null
    val width = maxOf(a.core.size, b.core.size)
    for (index in 0 until width) {
        val order = a.core.getOrElse(index) { BigInteger.ZERO }
            .compareTo(b.core.getOrElse(index) { BigInteger.ZERO })
        if (order != 0) return order
    }
    if (a.prerelease == null) return if (b.prerelease == null) 0 else 1
    if (b.prerelease == null) return -1
    val partCount = maxOf(a.prerelease.size, b.prerelease.size)
    for (index in 0 until partCount) {
        if (index >= a.prerelease.size) return -1
        if (index >= b.prerelease.size) return 1
        val x = a.prerelease[index]
        val y = b.prerelease[index]
        val xNumber = x.toBigIntegerOrNull()
        val yNumber = y.toBigIntegerOrNull()
        val order = when {
            xNumber != null && yNumber != null -> xNumber.compareTo(yNumber)
            xNumber != null -> -1
            yNumber != null -> 1
            else -> x.compareTo(y)
        }
        if (order != 0) return order
    }
    return 0
}

private data class ParsedVersion(val core: List<BigInteger>, val prerelease: List<String>?)

private val VERSION = Regex(
    """^[vV]?(\d+(?:\.\d+){0,3})(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$""",
)

private fun parseVersion(value: String): ParsedVersion? {
    val match = VERSION.matchEntire(value.trim()) ?: return null
    val core = match.groupValues[1].split('.').map { it.toBigIntegerOrNull() ?: return null }
    val prerelease = match.groupValues[2].takeIf(String::isNotEmpty)?.split('.')
    return ParsedVersion(core, prerelease)
}

fun validateAcceleratorPrefix(value: String): String? {
    val prefix = value.trim()
    if (prefix.isEmpty()) return null
    val uri = runCatching { URI(prefix) }.getOrNull()
        ?: throw IllegalArgumentException("加速服务地址格式无效")
    require(uri.scheme.equals("https", ignoreCase = true) && !uri.host.isNullOrBlank()) {
        "加速服务必须使用 HTTPS 完整地址"
    }
    require(uri.userInfo == null && uri.query == null && uri.fragment == null) {
        "加速服务地址不能包含账号、查询参数或片段"
    }
    return prefix.trimEnd('/') + "/"
}

fun githubRequestUrl(url: String, acceleratorPrefix: String): String {
    require(isGitHubHttpsUrl(url)) { "只允许访问 GitHub HTTPS 地址" }
    val prefix = validateAcceleratorPrefix(acceleratorPrefix) ?: return url
    return prefix + url
}

private fun isGitHubHttpsUrl(value: String): Boolean {
    val uri = runCatching { URI(value) }.getOrNull() ?: return false
    val host = uri.host?.lowercase() ?: return false
    return uri.scheme.equals("https", ignoreCase = true) &&
        (host == "github.com" || host == "api.github.com")
}
