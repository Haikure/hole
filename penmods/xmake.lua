add_rules('mode.release', 'mode.debug')

set_project('hole_plugin')
set_version('0.1.0')
set_languages('cxx17')
set_warnings('all')
set_allowedarchs('linux|arm64-v8a')

set_license('GPL-3.0-only')

-- Qt is supplied by the PenMods target device and is discovered through --qt.
target('hole_plugin')
    add_rules('qt.shared')
    add_files('src/*.cpp')
    add_files('src/*.h')
    add_frameworks('QtCore', 'QtQml', 'QtQuick')
    add_links('dl')
    add_includedirs('src')
    set_targetdir('build/$(plat)/$(arch)/$(mode)')
    set_basename('hole_plugin')
