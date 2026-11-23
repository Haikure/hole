#include "HolePlugin.h"

#include <QJsonDocument>
#include <QJsonArray>
#include <QFile>
#include <QDir>
#include <QElapsedTimer>
#include <QScopeGuard>
#include <QSignalSpy>
#include <QTemporaryDir>
#include <QtTest>
#include <memory>

// The bridge tests do not attach a host QML engine. Keep dladdr's entry point
// inside this test executable so configuration lookup stays in the test cache.
extern "C" void init_plugin() {}

class HolePluginTest : public QObject {
    Q_OBJECT
    std::unique_ptr<HolePlugin> plugin;

    void coreEvent(const QString& kind, const QString& state) {
        plugin->handleLine(QJsonDocument(QJsonObject{
            {"method", "event"}, {"params", QJsonObject{{"kind", kind}, {"state", state}}}
        }).toJson(QJsonDocument::Compact));
    }

    void snapshot(quint64 id, const QString& state, bool requested) {
        plugin->handleLine(QJsonDocument(QJsonObject{
            {"id", QStringLiteral("p%1").arg(id)},
            {"result", QJsonObject{{"snapshot", QJsonObject{
                {"engine_state", state}, {"run_requested", requested}
            }}}}
        }).toJson(QJsonDocument::Compact));
    }

    QJsonObject outgoingRequest(const QString& id) {
        QByteArray buffer;
        QElapsedTimer deadline;
        deadline.start();
        while (deadline.elapsed() < 1000) {
            buffer.append(plugin->m_process->readAllStandardOutput());
            int newline;
            while ((newline = buffer.indexOf('\n')) >= 0) {
                const QJsonObject request = QJsonDocument::fromJson(buffer.left(newline)).object();
                buffer.remove(0, newline + 1);
                if (request.value(QStringLiteral("id")).toString() == id) return request;
            }
            plugin->m_process->waitForReadyRead(20);
        }
        return {};
    }

private slots:
    void savedConnectionStrategyIsRemovedWithoutLosingEditorState() {
        const QString path = plugin->configPath();
        const bool existed = QFile::exists(path);
        QByteArray original;
        if (existed) {
            QFile file(path);
            QVERIFY(file.open(QIODevice::ReadOnly));
            original = file.readAll();
        }
        const auto restore = qScopeGuard([&] {
            if (existed) {
                QFile file(path);
                if (file.open(QIODevice::WriteOnly)) file.write(original);
            } else QFile::remove(path);
        });
        for (const QString& mode : {QStringLiteral("auto"), QStringLiteral("ice"), QStringLiteral("legacy")}) {
            QJsonObject config{
                {"room", "saved-room"}, {"password", "saved-password"}, {"token", "saved-token"},
                {"transport", QJsonObject{{"preferred", mode}, {"allow_insecure_signal", true}}},
                {"provide", QJsonArray{QJsonObject{{"id", "ssh"}, {"service", "tcp://127.0.0.1:22"}}}}
            };
            const QJsonObject mapping{{"provide", QJsonArray{QJsonObject{{"mappingId", "draft"}, {"active", false}}}}};
            const QJsonObject turn{{"mode", "manual"}, {"credential", "saved-turn"}};
            QFile file(path);
            QVERIFY(file.open(QIODevice::WriteOnly));
            QVERIFY(file.write(QJsonDocument(QJsonObject{
                {"server_url", "wss://example.invalid/ws"}, {"config", config},
                {"mapping_state", mapping}, {"turn_state", turn}
            }).toJson()) > 0);
            file.close();
            QVERIFY(plugin->loadConfig());
            config["transport"] = QJsonObject{{"allow_insecure_signal", true}};
            QCOMPARE(QJsonDocument::fromJson(plugin->configJson().toUtf8()).object(), config);
            QVERIFY(file.open(QIODevice::ReadOnly));
            const QJsonObject saved = QJsonDocument::fromJson(file.readAll()).object();
            QCOMPARE(saved["config"].toObject(), config);
            QCOMPARE(saved["mapping_state"].toObject(), mapping);
            QCOMPARE(saved["turn_state"].toObject(), turn);
        }
    }

    void init() {
        plugin = std::make_unique<HolePlugin>();
        // Capture outgoing RPCs without starting the network core.
        plugin->m_process = new QProcess();
        plugin->m_process->start(QStringLiteral("/bin/cat"), QStringList{});
        QVERIFY(plugin->m_process->waitForStarted());
        plugin->m_helloReceived = true;
        coreEvent("engine", "running");
    }

    void cleanup() {
        plugin->m_process->kill();
        plugin->m_process->waitForFinished();
        plugin.reset();
    }

    void onlineApplyWithoutRejoin() {
        coreEvent("config", "applying");
        QCOMPARE(plugin->state(), QStringLiteral("reconfiguring"));
        QVERIFY(plugin->running());
        const auto before = plugin->m_requestId;
        coreEvent("config", "applied");
        QVERIFY(plugin->m_requestId > before); // Completion requests a snapshot.
        snapshot(plugin->m_requestId, "running", true);
        QCOMPARE(plugin->state(), QStringLiteral("running"));
        QVERIFY(plugin->running());
    }

    void lostCompletionRecoversFromPoll() {
        coreEvent("config", "applying");
        plugin->refresh();
        snapshot(plugin->m_requestId, "running", true);
        QCOMPARE(plugin->state(), QStringLiteral("running"));
    }

    void completionDoesNotClaimRecoveredConnection() {
        coreEvent("config", "applying");
        coreEvent("engine", "recovering");
        coreEvent("config", "applied");
        snapshot(plugin->m_requestId, "recovering", true);
        QCOMPARE(plugin->state(), QStringLiteral("recovering"));
        QVERIFY(plugin->running());
    }

    void staleSnapshotsDoNotFinishNewApply() {
        plugin->refresh();
        const auto old = plugin->m_requestId;
        plugin->sendRequest("apply_config");
        coreEvent("config", "applying");
        snapshot(old, "running", true);
        QCOMPARE(plugin->state(), QStringLiteral("reconfiguring"));
        QVERIFY(plugin->snapshotJson().isEmpty());
        snapshot(plugin->m_requestId, "reconfiguring", true);
        QCOMPARE(plugin->state(), QStringLiteral("reconfiguring"));
        coreEvent("config", "applied");
        snapshot(plugin->m_requestId, "running", true);
        QCOMPARE(plugin->state(), QStringLiteral("running"));
    }

    void reconfiguringKeepsRunRequested() {
        coreEvent("engine", "reconfiguring");
        QVERIFY(plugin->running());
        snapshot(plugin->m_requestId, "reconfiguring", true);
        QVERIFY(plugin->running());
    }

    void stoppedSnapshotRepairsLostStopEvent() {
        plugin->refresh();
        snapshot(plugin->m_requestId, "stopped", false);
        QCOMPARE(plugin->state(), QStringLiteral("stopped"));
        QVERIFY(!plugin->running());
    }

    void rejectedApplyRefreshesAndReportsFailure() {
        plugin->m_pendingMethod = "apply_config";
        plugin->sendRequest("apply_config");
        const auto request = plugin->m_pendingRequestId;
        QSignalSpy failed(plugin.get(), &HolePlugin::configApplyFailed);
        plugin->handleLine(QJsonDocument(QJsonObject{
            {"id", request}, {"error", QJsonObject{{"data", QJsonObject{{"message", "invalid mapping"}}}}}
        }).toJson(QJsonDocument::Compact));
        QCOMPARE(failed.count(), 1);
        QCOMPARE(failed.at(0).at(0).toString(), QStringLiteral("invalid mapping"));
        QVERIFY(plugin->m_pendingRequestId.isEmpty());
        snapshot(plugin->m_requestId, "running", true);
        QCOMPARE(plugin->lastError(), QStringLiteral("invalid mapping"));
        QCOMPARE(plugin->state(), QStringLiteral("running"));
    }

    void punchExportNegotiatesCapability() {
        QVERIFY(!plugin->natPunchProcessExportAvailable());
        QVERIFY(plugin->exportNATPunchProcess("pair").isEmpty());
        plugin->handleLine(QJsonDocument(QJsonObject{
            {"id", "hello"}, {"result", QJsonObject{{"bridge_version", 1},
                {"methods", QJsonArray{"nat_punch_process", "export_nat_punch_process"}}}}
        }).toJson(QJsonDocument::Compact));
        QVERIFY(plugin->natPunchProcessExportAvailable());
        plugin->handleLine(QJsonDocument(QJsonObject{
            {"id", "old-hello"}, {"result", QJsonObject{{"bridge_version", 1},
                {"methods", QJsonArray{"nat_punch_process"}}}}
        }).toJson(QJsonDocument::Compact));
        QVERIFY(!plugin->natPunchProcessExportAvailable());
    }

    void punchExportDoesNotInterruptProcessReadingOrConnection() {
        QTemporaryDir directory;
        QVERIFY(directory.isValid());
        plugin->m_pluginDirectory = directory.path();
        plugin->m_natPunchProcessAvailable = plugin->m_natPunchProcessExportAvailable = true;
        const QString read = plugin->requestNATPunchProcess("pair", "24", "7", "discovery");
        QVERIFY(!read.isEmpty());
        QSignalSpy ready(plugin.get(), &HolePlugin::natPunchProcessExportReady);
        const QString id = plugin->exportNATPunchProcess("pair");
        QVERIFY(!id.isEmpty());
        QVERIFY(plugin->exportNATPunchProcess("pair").isEmpty());
        const QJsonObject request = outgoingRequest(id);
        QCOMPARE(request.value("method").toString(), QStringLiteral("export_nat_punch_process"));
        const QJsonObject params = request.value("params").toObject();
        QCOMPARE(params.value("transport_id").toString(), QStringLiteral("pair"));
        QCOMPARE(params.value("directory").toString(), QDir(directory.path()).filePath("exports"));
        QCOMPARE(params.size(), 2);
        const QString path = QDir(directory.path()).filePath("exports/hole-nat-punch-record.json");
        plugin->handleLine(QJsonDocument(QJsonObject{
            {"id", id}, {"result", QJsonObject{{"path", path}}}
        }).toJson(QJsonDocument::Compact));
        QCOMPARE(ready.count(), 1);
        QCOMPARE(ready.at(0).at(0).toString(), id);
        QCOMPARE(ready.at(0).at(1).toString(), path);
        QCOMPARE(plugin->m_natPunchProcessRequestId, read);
        QVERIFY(plugin->m_natPunchExportRequestId.isEmpty());
        QVERIFY(!plugin->m_natPunchExportTimer.isActive());
        QCOMPARE(plugin->state(), QStringLiteral("running"));
        QVERIFY(plugin->running());
        QVERIFY(plugin->lastError().isEmpty());
    }

    void punchExportTimeoutDiscardsLateReplyAndAllowsRetry() {
        plugin->m_natPunchProcessExportAvailable = true;
        QSignalSpy ready(plugin.get(), &HolePlugin::natPunchProcessExportReady);
        QSignalSpy failed(plugin.get(), &HolePlugin::natPunchProcessExportFailed);
        const QString first = plugin->exportNATPunchProcess("pair");
        QVERIFY(!first.isEmpty());
        plugin->m_natPunchExportTimer.stop();
        QVERIFY(QMetaObject::invokeMethod(&plugin->m_natPunchExportTimer, "timeout"));
        QCOMPARE(failed.count(), 1);
        QCOMPARE(failed.at(0).at(0).toString(), first);
        const QString second = plugin->exportNATPunchProcess("pair");
        QVERIFY(!second.isEmpty());
        plugin->handleLine(QJsonDocument(QJsonObject{
            {"id", first}, {"result", QJsonObject{{"path", "/tmp/late.json"}}}
        }).toJson(QJsonDocument::Compact));
        QCOMPARE(ready.count(), 0);
        QCOMPARE(plugin->m_natPunchExportRequestId, second);
        plugin->handleLine(QJsonDocument(QJsonObject{
            {"id", second}, {"error", QJsonObject{{"data", QJsonObject{{"message", "storage full"}}}}}
        }).toJson(QJsonDocument::Compact));
        QCOMPARE(failed.count(), 2);
        QCOMPARE(failed.at(1).at(1).toString(), QStringLiteral("storage full"));
        QVERIFY(plugin->m_natPunchExportRequestId.isEmpty());
        QCOMPARE(plugin->state(), QStringLiteral("running"));
        QVERIFY(plugin->lastError().isEmpty());
    }

    void punchExportReportsCoreExitAndMissingFilePath() {
        plugin->m_natPunchProcessExportAvailable = true;
        QSignalSpy failed(plugin.get(), &HolePlugin::natPunchProcessExportFailed);
        const QString id = plugin->exportNATPunchProcess("pair");
        QVERIFY(!id.isEmpty());
        plugin->handleLine(QJsonDocument(QJsonObject{{"id", id}, {"result", QJsonObject{}}}).toJson(QJsonDocument::Compact));
        QCOMPARE(failed.count(), 1);
        QVERIFY(plugin->m_natPunchExportRequestId.isEmpty());
        const QString next = plugin->exportNATPunchProcess("pair");
        QVERIFY(!next.isEmpty());
        plugin->clearProcessState();
        QCOMPARE(failed.count(), 2);
        QCOMPARE(failed.at(1).at(0).toString(), next);
        QVERIFY(!plugin->natPunchProcessExportAvailable());
        QVERIFY(plugin->m_natPunchExportRequests.isEmpty());
        QVERIFY(!plugin->m_natPunchExportTimer.isActive());
    }
};

QTEST_GUILESS_MAIN(HolePluginTest)
#include "tst_HolePlugin.moc"
