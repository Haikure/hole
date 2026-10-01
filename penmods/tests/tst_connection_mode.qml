import QtQuick 2.12
import QtTest 1.2
import "../components"

TestCase {
    name: "PenModsConnectionMode"
    when: windowShown
    visible: true
    width: 320
    height: 170

    QtObject {
        id: form
        property string preferred: "ice"
        property bool allowLegacy: true
        property int saves: 0
        function syncConfig() { saves++ }
    }
    ConnectionModeRow { id: row; width: 242; form: form }

    function init() {
        form.preferred = "ice"
        form.allowLegacy = true
        form.saves = 0
    }

    function test_existingConfig_data() {
        return [
            {tag: "auto", preferred: "ice", legacy: true, mode: "auto"},
            {tag: "ice", preferred: "ice", legacy: false, mode: "ice"},
            {tag: "ipv6", preferred: "ipv6", legacy: false, mode: "legacy"},
            {tag: "old-ipv6", preferred: "ipv6", legacy: true, mode: "legacy"}
        ]
    }
    function test_existingConfig(data) {
        form.preferred = data.preferred
        form.allowLegacy = data.legacy
        compare(row.selected, data.mode)
        compare(form.saves, 0)
    }

    function test_selectionWritesCoreFields_data() {
        return [
            {tag: "auto", mode: "auto", preferred: "ice", legacy: true},
            {tag: "ice", mode: "ice", preferred: "ice", legacy: false},
            {tag: "ipv6", mode: "legacy", preferred: "ipv6", legacy: false}
        ]
    }
    function test_selectionWritesCoreFields(data) {
        row.chosen(data.mode)
        compare(form.preferred, data.preferred)
        compare(form.allowLegacy, data.legacy)
        compare(form.saves, 1)
        compare(row.selected, data.mode)
    }
}
