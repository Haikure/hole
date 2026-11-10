#include "HolePlugin.h"

#include <QQmlContext>
#include <QQmlEngine>

namespace {
HolePlugin* g_plugin = nullptr;
}

extern "C" void init_plugin() {
    if (!g_plugin) g_plugin = new HolePlugin();
}

extern "C" void attach_engine(QQmlEngine* engine) {
    if (!engine) return;
    if (!g_plugin) g_plugin = new HolePlugin();
    engine->rootContext()->setContextProperty(QStringLiteral("holePlugin"), g_plugin);
}

extern "C" void destroy_plugin() {
    delete g_plugin;
    g_plugin = nullptr;
}
