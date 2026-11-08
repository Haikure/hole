.pragma library

function format(value) {
    if (!value || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/.test(value)) return value
    // Go emits nanoseconds; Qt's Date parser accepts millisecond precision.
    var normalized = value.replace(/\.(\d+)(?=Z|[+-]\d{2}:\d{2}$)/, function(_, digits) {
        return "." + (digits + "000").slice(0, 3)
    })
    var stamp = new Date(normalized)
    return isNaN(stamp.getTime()) ? value : Qt.formatDateTime(stamp, "yyyy-MM-dd hh:mm:ss")
}
