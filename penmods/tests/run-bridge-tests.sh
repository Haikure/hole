#!/usr/bin/env bash
set -euo pipefail
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$ROOT/scripts/build-env.sh"
TEST_BUILD="$HOLE_CACHE_ROOT/penmods-tests"
mkdir -p "$TEST_BUILD"
MOC=${MOC:-$(qmake -query QT_HOST_BINS)/moc}
"$MOC" "$ROOT/penmods/src/HolePlugin.h" -o "$TEST_BUILD/moc_HolePlugin.cpp"
"$MOC" "$ROOT/penmods/tests/tst_HolePlugin.cpp" -o "$TEST_BUILD/tst_HolePlugin.moc"
read -r -a QT_FLAGS <<< "$(pkg-config --cflags --libs Qt5Core Qt5Test)"
"${CXX:-c++}" -std=c++17 -O2 -fPIC -Wall -Wextra \
    -I"$ROOT/penmods/src" -I"$TEST_BUILD" \
    "$ROOT/penmods/src/HolePlugin.cpp" "$TEST_BUILD/moc_HolePlugin.cpp" \
    "$ROOT/penmods/tests/tst_HolePlugin.cpp" "${QT_FLAGS[@]}" -ldl \
    -o "$TEST_BUILD/tst_HolePlugin"
"$TEST_BUILD/tst_HolePlugin"
