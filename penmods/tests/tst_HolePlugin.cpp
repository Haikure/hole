#include "HolePlugin.h"

#include <QJsonDocument>
#include <QJsonArray>
#include <QFile>
#include <QScopeGuard>
#include <QSignalSpy>
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
};

QTEST_GUILESS_MAIN(HolePluginTest)
#include "tst_HolePlugin.moc"
