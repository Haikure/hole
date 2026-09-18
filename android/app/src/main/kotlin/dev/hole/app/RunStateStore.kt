package dev.hole.app

import android.content.Context
import androidx.core.content.edit

/**
 * 持久化用户运行意图（run_requested），用于系统允许的服务重建。
 * 这里不是"是否真的在运行"：实际状态以核心快照为准。
 */
class RunStateStore(context: Context) {
    private val prefs = context.applicationContext
        .getSharedPreferences("run_state", Context.MODE_PRIVATE)

    fun isRequested(): Boolean = prefs.getBoolean(KEY_RUN_REQUESTED, false)

    /**
     * 同步提交：服务可能在返回前被系统回收，运行意图必须先落盘。
     * 这是有意的 commit，不是可以留给后台线程的 apply。
     */
    fun setRequested(value: Boolean) {
        prefs.edit(commit = true) { putBoolean(KEY_RUN_REQUESTED, value) }
    }

    fun resumeAfterBoot(): Boolean = prefs.getBoolean("resume_after_boot", false)
    fun setResumeAfterBoot(value: Boolean) { prefs.edit(commit = true) { putBoolean("resume_after_boot", value) } }
    fun lastResumeError(): String = prefs.getString("last_resume_error", "").orEmpty()
    fun setResumeError(value: String) { prefs.edit(commit = true) { putString("last_resume_error", value) } }

    private companion object {
        const val KEY_RUN_REQUESTED = "run_requested"
    }
}
