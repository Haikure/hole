#include "HolePlugin.h"

#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QJsonDocument>
#include <QJsonParseError>
#include <QSaveFile>
#include <QQmlContext>
#include <QQmlEngine>

#include <dlfcn.h>

namespace {
HolePlugin* g_plugin = nullptr;

QString pluginDirectoryFromAddress(void* address) {
    Dl_info info{};
    if (dladdr(address, &info) == 0 || info.dli_fname == nullptr) return {};
    return QFileInfo(QString::fromLocal8Bit(info.dli_fname)).absolutePath();
}
}

HolePlugin::HolePlugin(QObject* parent) : QObject(parent) {
    m_pluginDirectory = pluginDirectoryFromAddress(reinterpret_cast<void*>(&init_plugin));
    loadConfig();
}

HolePlugin::~HolePlugin() {
    if (!m_process) return;
    if (m_process->state() != QProcess::NotRunning) {
        sendRequest(QStringLiteral("shutdown"));
        if (!m_process->waitForFinished(2000)) m_process->kill();
    }
    delete m_process;
}

void HolePlugin::setServerUrl(const QString& value) {
    if (m_serverUrl == value) return;
    m_serverUrl = value.trimmed();
    emit configChanged();
}

void HolePlugin::setConfigJson(const QString& value) {
    if (m_configJson == value) return;
    m_configJson = value;
    emit configChanged();
}

void HolePlugin::setMappingStateJson(const QString& value) {
    if (m_mappingStateJson == value) return;
    m_mappingStateJson = value;
    emit configChanged();
}

void HolePlugin::setState(const QString& value) {
    if (m_state == value) return;
    m_state = value;
    emit stateChanged();
}

void HolePlugin::setError(const QString& value) {
    if (m_lastError == value) return;
    m_lastError = value;
    emit lastErrorChanged();
}

QString HolePlugin::configPath() const {
    return QDir(m_pluginDirectory).filePath(QStringLiteral("config.json"));
}

bool HolePlugin::saveConfig() {
    QJsonParseError error{};
    const QJsonDocument config = QJsonDocument::fromJson(m_configJson.toUtf8(), &error);
    if (error.error != QJsonParseError::NoError || !config.isObject() || m_serverUrl.isEmpty()) {
        setError(QStringLiteral("配置或信令地址无效，无法保存"));
        return false;
    }

    QJsonObject root;
    root.insert(QStringLiteral("server_url"), m_serverUrl);
    root.insert(QStringLiteral("config"), config.object());
    if (!m_mappingStateJson.trimmed().isEmpty()) {
        QJsonParseError mappingError{};
        const QJsonDocument mappingState = QJsonDocument::fromJson(m_mappingStateJson.toUtf8(), &mappingError);
        if (mappingError.error != QJsonParseError::NoError || !mappingState.isObject()) {
            setError(QStringLiteral("映射编辑状态无效，无法保存"));
            return false;
        }
        root.insert(QStringLiteral("mapping_state"), mappingState.object());
    }
    QSaveFile file(configPath());
    if (!file.open(QIODevice::WriteOnly) || file.write(QJsonDocument(root).toJson(QJsonDocument::Compact)) < 0 || !file.commit()) {
        setError(QStringLiteral("无法保存插件配置"));
        return false;
    }
    return true;
}

bool HolePlugin::loadConfig() {
    QFile file(configPath());
    if (!file.exists()) return true;
    if (!file.open(QIODevice::ReadOnly)) {
        setError(QStringLiteral("无法读取插件配置"));
        return false;
    }

    QJsonParseError error{};
    const QJsonDocument document = QJsonDocument::fromJson(file.readAll(), &error);
    if (error.error != QJsonParseError::NoError || !document.isObject()) {
        setError(QStringLiteral("插件配置格式无效"));
        return false;
    }
    const QJsonObject root = document.object();
    const QJsonValue serverUrl = root.value(QStringLiteral("server_url"));
    const QJsonValue config = root.value(QStringLiteral("config"));
    if (!serverUrl.isString() || !config.isObject()) {
        setError(QStringLiteral("插件配置缺少必要字段"));
        return false;
    }
    m_serverUrl = serverUrl.toString();
    m_configJson = QString::fromUtf8(QJsonDocument(config.toObject()).toJson(QJsonDocument::Compact));
    const QJsonValue mappingState = root.value(QStringLiteral("mapping_state"));
    m_mappingStateJson = mappingState.isObject()
        ? QString::fromUtf8(QJsonDocument(mappingState.toObject()).toJson(QJsonDocument::Compact))
        : QString();
    emit configChanged();
    return true;
}

QJsonObject HolePlugin::requestObject() const {
    QJsonParseError error{};
    const QJsonDocument config = QJsonDocument::fromJson(m_configJson.toUtf8(), &error);
    if (error.error != QJsonParseError::NoError || !config.isObject()) return {};

    QJsonObject request;
    request.insert(QStringLiteral("api_version"), 1);
    request.insert(QStringLiteral("server_url"), m_serverUrl);
    request.insert(QStringLiteral("config"), config.object());
    return request;
}

void HolePlugin::ensureProcess() {
    if (m_process && m_process->state() != QProcess::NotRunning) return;
    if (m_pluginDirectory.isEmpty()) {
        setError(QStringLiteral("无法确定插件目录"));
        return;
    }

    delete m_process;
    m_process = new QProcess(this);
    m_process->setProgram(QDir(m_pluginDirectory).filePath(QStringLiteral("hole-desktop-core")));
    m_process->setWorkingDirectory(m_pluginDirectory);
    m_process->setProcessChannelMode(QProcess::SeparateChannels);
    connect(m_process, &QProcess::started, this, &HolePlugin::onProcessStarted);
    connect(m_process, &QProcess::readyReadStandardOutput, this, &HolePlugin::readStandardOutput);
    connect(m_process, &QProcess::readyReadStandardError, this, &HolePlugin::readStandardError);
    connect(m_process, qOverload<int, QProcess::ExitStatus>(&QProcess::finished), this, &HolePlugin::onProcessFinished);
    connect(m_process, &QProcess::errorOccurred, this, [this](QProcess::ProcessError error) {
        if (error != QProcess::FailedToStart) return;
        setError(QStringLiteral("无法启动 Hole 核心程序"));
        setState(QStringLiteral("error"));
    });
    m_helloReceived = false;
    m_process->start();
}

void HolePlugin::onProcessStarted() {
    sendHello();
}

void HolePlugin::sendHello() {
    sendRequest(QStringLiteral("hello"));
}

void HolePlugin::sendStart(const QString& method) {
    const QJsonObject request = requestObject();
    if (request.isEmpty()) {
        setError(QStringLiteral("配置 JSON 无效"));
        return;
    }
    sendRequest(method, request);
}

void HolePlugin::sendRequest(const QString& method, const QJsonObject& params) {
    if (!m_process || m_process->state() == QProcess::NotRunning) return;
    QJsonObject request;
    const QString id = QStringLiteral("p%1").arg(++m_requestId);
    request.insert(QStringLiteral("jsonrpc"), QStringLiteral("2.0"));
    request.insert(QStringLiteral("id"), id);
    request.insert(QStringLiteral("method"), method);
    if (!params.isEmpty()) request.insert(QStringLiteral("params"), params);
    m_process->write(QJsonDocument(request).toJson(QJsonDocument::Compact));
    m_process->write("\n");
}

void HolePlugin::start() {
    if (m_running) return;
    if (!saveConfig()) return;
    m_pendingMethod = QStringLiteral("start");
    ensureProcess();
    if (m_process && m_helloReceived) {
        sendStart(m_pendingMethod);
    }
    setState(QStringLiteral("starting"));
}

void HolePlugin::stop() {
    if (!m_process || m_process->state() == QProcess::NotRunning) {
        setState(QStringLiteral("stopped"));
        return;
    }
    sendRequest(QStringLiteral("stop"));
    m_pendingMethod.clear();
    setState(QStringLiteral("stopping"));
}

void HolePlugin::applyConfig() {
    if (!saveConfig()) return;
    if (!m_process || m_process->state() == QProcess::NotRunning) {
        start();
        return;
    }
    m_pendingMethod = QStringLiteral("apply_config");
    sendStart(QStringLiteral("apply_config"));
}

void HolePlugin::refresh() {
    if (!m_process || m_process->state() == QProcess::NotRunning) {
        ensureProcess();
        return;
    }
    sendRequest(QStringLiteral("snapshot"));
}

void HolePlugin::networkChanged() {
    if (m_process && m_process->state() != QProcess::NotRunning) sendRequest(QStringLiteral("network_changed"));
}

void HolePlugin::clearError() {
    setError({});
}

void HolePlugin::readStandardOutput() {
    m_outputBuffer.append(m_process->readAllStandardOutput());
    while (true) {
        const qsizetype newline = m_outputBuffer.indexOf('\n');
        if (newline < 0) break;
        const QByteArray line = m_outputBuffer.left(newline).trimmed();
        m_outputBuffer.remove(0, newline + 1);
        if (!line.isEmpty()) handleLine(line);
    }
}

void HolePlugin::readStandardError() {
    if (m_process) m_process->readAllStandardError();
}

void HolePlugin::handleLine(const QByteArray& line) {
    QJsonParseError error{};
    const QJsonDocument document = QJsonDocument::fromJson(line, &error);
    if (error.error != QJsonParseError::NoError || !document.isObject()) return;
    const QJsonObject object = document.object();

    if (object.value(QStringLiteral("method")).toString() == QStringLiteral("event")) {
        const QJsonObject event = object.value(QStringLiteral("params")).toObject();
        const QString kind = event.value(QStringLiteral("kind")).toString();
        const QString state = event.value(QStringLiteral("state")).toString();
        if (kind == QStringLiteral("engine") && !state.isEmpty()) {
            setState(state);
            const bool active = state == QStringLiteral("starting") || state == QStringLiteral("running") || state == QStringLiteral("recovering");
            if (m_running != active) {
                m_running = active;
                emit runningChanged();
            }
            if (event.contains(QStringLiteral("error"))) {
                const QJsonObject fault = event.value(QStringLiteral("error")).toObject();
                setError(fault.value(QStringLiteral("message")).toString(QStringLiteral("核心连接失败")));
            } else if (state == QStringLiteral("running")) {
                // A later successful engine event supersedes a stale error
                // from an earlier reconnect attempt.
                setError({});
            }
        }
        if (kind == QStringLiteral("config") && state == QStringLiteral("applying")) {
            // A live reconfiguration drains and rebuilds the transport. Keep
            // the UI out of the connected state until the core publishes its
            // next engine=running event after the new signal join.
            setState(QStringLiteral("reconfiguring"));
        }
        if (kind == QStringLiteral("session") && event.contains(QStringLiteral("error"))) setError(QStringLiteral("映射会话失败"));
        emit eventReceived(QString::fromUtf8(line));
        return;
    }

    const QJsonObject errorObject = object.value(QStringLiteral("error")).toObject();
    if (!errorObject.isEmpty()) {
        const QJsonObject data = errorObject.value(QStringLiteral("data")).toObject();
        setError(data.value(QStringLiteral("message")).toString(QStringLiteral("核心请求失败")));
        m_pendingMethod.clear();
        if (!m_running) setState(QStringLiteral("error"));
        return;
    }

    const QJsonObject result = object.value(QStringLiteral("result")).toObject();
    if (result.contains(QStringLiteral("bridge_version"))) {
        // The hello response is not the start response. Keep the pending
        // operation alive until its own reply arrives; otherwise the first
        // start request is lost and the UI gets a false running state.
        m_helloReceived = true;
        if (!m_pendingMethod.isEmpty()) sendStart(m_pendingMethod);
        return;
    }

    const QString method = m_pendingMethod;
    if (method == QStringLiteral("start") || method == QStringLiteral("apply_config")) {
        if (m_running != true) {
            m_running = true;
            emit runningChanged();
        }
        m_pendingMethod.clear();
    }
    if (result.contains(QStringLiteral("snapshot"))) {
        m_snapshotJson = QString::fromUtf8(QJsonDocument(result.value(QStringLiteral("snapshot")).toObject()).toJson(QJsonDocument::Compact));
        emit snapshotChanged();
    }
}

void HolePlugin::onProcessFinished(int, QProcess::ExitStatus) {
    m_helloReceived = false;
    m_pendingMethod.clear();
    if (m_running) {
        m_running = false;
        emit runningChanged();
    }
    setState(QStringLiteral("stopped"));
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
