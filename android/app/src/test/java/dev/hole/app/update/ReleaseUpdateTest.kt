package dev.hole.app.update

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotNull
import kotlin.test.assertNull

class ReleaseUpdateTest {
    @Test
    fun parserSelectsPhoneApkAndKeepsReleaseNotes() {
        val release = GitHubReleaseParser.parseLatest(
            """
            {
              "tag_name": "v0.4.0",
              "name": "Hole 0.4.0",
              "body": "Release notes",
              "html_url": "https://github.com/Haikure/hole/releases/tag/v0.4.0",
              "published_at": "2026-10-16T10:00:00Z",
              "assets": [
                {"name": "hole-0.4.0-wear-release.apk", "size": 100, "browser_download_url": "https://github.com/Haikure/hole/releases/download/v0.4.0/hole-0.4.0-wear-release.apk"},
                {"name": "hole-0.4.0-debug.apk", "size": 100, "browser_download_url": "https://github.com/Haikure/hole/releases/download/v0.4.0/hole-0.4.0-debug.apk"},
                {"name": "hole-0.4.0-release.apk", "size": 200, "browser_download_url": "https://github.com/Haikure/hole/releases/download/v0.4.0/hole-0.4.0-release.apk"}
              ]
            }
            """.trimIndent(),
        )

        assertEquals("v0.4.0", release.tag)
        assertEquals("Release notes", release.notes)
        assertEquals("hole-0.4.0-release.apk", assertNotNull(release.appApk).name)
        assertEquals(200L, release.appApk?.size)
    }

    @Test
    fun semanticVersionComparisonHandlesNumbersAndPrereleases() {
        assertEquals(1, compareReleaseVersions("v1.10.0", "1.9.9"))
        assertEquals(1, compareReleaseVersions("v1.0.0", "1.0.0-rc.1"))
        assertEquals(-1, compareReleaseVersions("1.0.0-alpha.2", "1.0.0-alpha.10"))
        assertEquals(0, compareReleaseVersions("v0.3.7+build.4", "0.3.7"))
        assertNull(compareReleaseVersions("main", "0.3.7"))
    }

    @Test
    fun acceleratorPresetsRouteOnlySupportedRequests() {
        val ghProxy = ReleaseAccelerators.resolve(ReleaseAccelerators.GH_PROXY, "")
        assertEquals(true, ghProxy.proxyApi)
        assertEquals(
            "https://gh-proxy.com/https://api.github.com/repos/Haikure/hole/releases/latest",
            githubRequestUrl("https://api.github.com/repos/Haikure/hole/releases/latest", ghProxy.prefix),
        )

        val ghFast = ReleaseAccelerators.resolve(ReleaseAccelerators.GHFAST, "")
        assertEquals(false, ghFast.proxyApi)
        assertEquals("https://ghfast.top/", ghFast.prefix)
    }

    @Test
    fun customPrefixMustBeHttpsAndCanRouteGithubUrls() {
        val prefix = validateAcceleratorPrefix("  https://mirror.example/github  ")
        assertEquals("https://mirror.example/github/", prefix)
        assertEquals(
            "https://mirror.example/github/https://github.com/Haikure/hole/releases/download/v1/hole.apk",
            githubRequestUrl("https://github.com/Haikure/hole/releases/download/v1/hole.apk", prefix.orEmpty()),
        )
        assertFailsWith<IllegalArgumentException> { validateAcceleratorPrefix("http://mirror.example/") }
        assertFailsWith<IllegalArgumentException> { validateAcceleratorPrefix("https://mirror.example/?target=github") }
        assertFailsWith<IllegalArgumentException> {
            githubRequestUrl("https://evil.example/file.apk", "https://mirror.example/")
        }
    }
}
