# gomobile resolves these Java classes and members from native code by name.
# Keep the complete small binding boundary, not the app or Compose libraries.
# These also match the generated AAR's consumer rules.
-keep class go.** { *; }
-keep class dev.hole.core.** { *; }
