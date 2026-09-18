import QtQuick 2.12
import "qrc:/qml/commons"
import "."

// Bridges Field taps to the host input page injected through qmlCreateComponent.
// The host contract (YPagePopHelper container, YInputPage, qmlGlobal) is the one
// BiliPocket uses; the plugin keeps no copy of the input page and never falls
// back to the system keyboard.
//
// Only one request is in flight at a time: `requestToken` invalidates callbacks
// from an earlier open(), so a late incubator cannot steal the current target.
YPagePopHelper {
    id: helper

    property bool pageOpen: false
    property bool opening: false
    property bool shuttingDown: false
    property var acceptHandler: null
    property var livePage: null
    property int requestToken: 0

    z: 1000
    isShowing: helper.pageOpen

    function hostGlobal() {
        try {
            return qmlGlobal
        } catch (error) {
            return null
        }
    }

    function setHostShowing(value) {
        var host = helper.hostGlobal()
        if (host) host.inputPageShowing = value
    }

    function open(prefill, placeholder, accepted) {
        if (helper.pageOpen || helper.opening || helper.shuttingDown) return

        helper.opening = true
        var component = qmlCreateComponent("YInputPage")
        if (!component) {
            helper.opening = false
            console.warn("hole_plugin: 宿主未提供 YInputPage")
            return
        }
        if (component.status === Component.Error) {
            helper.opening = false
            console.warn("hole_plugin: 无法创建 YInputPage：" + component.errorString())
            return
        }

        helper.acceptHandler = accepted
        helper.requestToken += 1
        var token = helper.requestToken
        var text = prefill === undefined || prefill === null ? "" : String(prefill)
        var hint = placeholder === undefined || placeholder === null ? "" : String(placeholder)

        // qmlCreateComponent() returns a Component, not the YInputPage itself.
        // Always incubate it into the helper container; passing the Component to
        // attachPage() makes every synchronous creation look like a broken page.
        helper.createPage(component, token, text, hint)
    }

    function createPage(component, token, text, placeholder) {
        var incubator = component.incubateObject(helper.containerItem)
        if (!incubator) {
            helper.teardown()
            return
        }
        if (incubator.status === Component.Ready) {
            helper.attachPage(incubator.object, token, text, placeholder)
            return
        }
        if (incubator.status === Component.Error) {
            helper.teardown()
            return
        }
        incubator.onStatusChanged = function (status) {
            if (status === Component.Ready) {
                helper.attachPage(incubator.object, token, text, placeholder)
            } else if (status === Component.Error) {
                helper.teardown()
            }
        }
    }

    function attachPage(inputPage, token, text, placeholder) {
        if (!inputPage || helper.shuttingDown || token !== helper.requestToken) {
            helper.scheduleDestroy(inputPage)
            return
        }
        helper.livePage = inputPage
        var backConnected = helper.connectSignal(inputPage, "backButtonClicked", helper.cancelRequest)
        var finishConnected = helper.connectSignal(inputPage, "inputFinished", function (content) {
            helper.acceptText(content)
        })
        if (!backConnected || !finishConnected) {
            console.warn("hole_plugin: YInputPage 接口不完整：backButtonClicked="
                         + backConnected + ", inputFinished=" + finishConnected)
            helper.teardown()
            return
        }
        if (typeof inputPage.enterText !== "function") {
            console.warn("hole_plugin: YInputPage 缺少 enterText 方法")
            helper.teardown()
            return
        }
        if (typeof inputPage.show !== "function") {
            console.warn("hole_plugin: YInputPage 缺少 show 方法")
            helper.teardown()
            return
        }
        if (typeof inputPage.placeHolderText !== "undefined")
            inputPage.placeHolderText = placeholder
        inputPage.enterText(text)
        inputPage.show()
        helper.opening = false
        helper.pageOpen = true
        helper.setHostShowing(true)
    }

    function connectSignal(target, name, handler) {
        if (!target) return false
        var signal = target[name]
        if (!signal || typeof signal.connect !== "function") return false
        signal.connect(handler)
        return true
    }

    function acceptText(content) {
        var handler = helper.acceptHandler
        helper.teardown()
        if (handler) handler(content)
    }

    function cancelRequest() {
        helper.teardown()
    }

    function teardown() {
        helper.pageOpen = false
        helper.opening = false
        helper.acceptHandler = null
        helper.requestToken += 1
        helper.setHostShowing(false)
        var inputPage = helper.livePage
        helper.livePage = null
        // Tearing the page down from inside its own signal handler is unsafe, so
        // hand it to the next event loop turn. The callback must not dereference
        // `helper`: the component may be gone before the callback runs.
        helper.scheduleDestroy(inputPage)
    }

    function scheduleDestroy(inputPage) {
        if (!inputPage) return
        Qt.callLater(function () {
            if (inputPage && typeof inputPage.todoDestroy === "function") inputPage.todoDestroy()
        })
    }

    Component.onDestruction: {
        helper.shuttingDown = true
        helper.teardown()
    }
}
