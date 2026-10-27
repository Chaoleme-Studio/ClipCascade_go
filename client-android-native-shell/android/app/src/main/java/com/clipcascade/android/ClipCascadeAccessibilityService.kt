package com.clipcascade.android

import android.accessibilityservice.AccessibilityService
import android.content.Intent
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.view.accessibility.AccessibilityEvent
import java.util.Locale

class ClipCascadeAccessibilityService : AccessibilityService() {

    private val TAG = "ClipCascade_A11y"

    private val handler = Handler(Looper.getMainLooper())
    private var lastTriggerTime: Long? = null
    private val DEBOUNCE_DELAY_MS = 1000L // 1秒防抖，防止短时间多次触发
    private val copyActionLabels = setOf(
        "复制", "复制链接", "复制链接地址", "复制文本", "复制文字", "复制内容", "复制地址", "复制网址", "复制图片", "复制代码", "复制到剪贴板",
        "複製", "複製連結", "複製文字", "複製內容", "複製網址", "複製圖片", "複製到剪貼簿",
        "copy", "copy link", "copy link address", "copy text", "copy image", "copy address", "copy url", "copy code", "copy to clipboard",
        "剪切", "剪下", "cut"
    )
    private val copiedFeedback = Regex(
        "已(?:成功)?复制|复制成功|复制完成|已(?:成功)?複製|複製成功|複製完成|\\bcopied\\b",
        RegexOption.IGNORE_CASE
    )

    // 延迟执行实际读取动作的 Runnable
    private val copyTriggerRunnable = Runnable {
        Log.i(TAG, "🟢 触发剪贴板同步检测流程...")
        startBackgroundService(ClipCascadeBackgroundService.ACTION_SYNC_CLIPBOARD_NOW)
    }

    override fun onServiceConnected() {
        super.onServiceConnected()
        Log.i(TAG, "🚀 ClipCascade 无障碍服务已启动")
        // 启动同步引擎即可；连接无障碍服务本身不是复制动作。
        startBackgroundService()
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent?) {
        if (event == null) return

        // 自身事件会造成回环；输入框中的文字不能被当作复制按钮标签。
        if (event.packageName?.toString() == packageName || event.isPassword ||
            event.className?.toString()?.endsWith("EditText") == true) {
            return
        }

        val labels = event.text.map { it.toString().trim() } +
            listOfNotNull(event.contentDescription?.toString()?.trim())
        val isCopyEvent = when (event.eventType) {
            AccessibilityEvent.TYPE_VIEW_CLICKED -> {
                labels.any { it.lowercase(Locale.ROOT) in copyActionLabels }
            }
            AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED,
            AccessibilityEvent.TYPE_ANNOUNCEMENT -> {
                labels.any(copiedFeedback::containsMatchIn)
            }
            else -> false
        }
        if (isCopyEvent) {
            debounceAndTriggerCopy(isCopyAction = event.eventType == AccessibilityEvent.TYPE_VIEW_CLICKED)
        }
    }

    /**
     * 防抖触发器：过滤高频连续事件，降低功耗
     */
    private fun debounceAndTriggerCopy(isCopyAction: Boolean) {
        val currentTime = SystemClock.uptimeMillis()
        val previousTrigger = lastTriggerTime
        
        // 忽略同一次复制随后发出的 Toast；新的明确复制动作仍须读取最新内容。
        if (!isCopyAction && previousTrigger != null && currentTime - previousTrigger < DEBOUNCE_DELAY_MS) {
            return
        }

        lastTriggerTime = currentTime
        
        // 移除之前还在等待中的任务
        handler.removeCallbacks(copyTriggerRunnable)
        
        // 等待系统完成剪贴板写入；普通点击、打字、光标移动不再安排读取。
        handler.postDelayed(copyTriggerRunnable, 300)
    }

    override fun onInterrupt() {
        Log.i(TAG, "⏸️ 无障碍服务被中断")
        handler.removeCallbacks(copyTriggerRunnable)
    }

    override fun onUnbind(intent: Intent?): Boolean {
        Log.i(TAG, "🛑 无障碍服务正在解绑")
        handler.removeCallbacks(copyTriggerRunnable)
        return super.onUnbind(intent)
    }

    private fun startBackgroundService(action: String? = null) {
        val intent = Intent(this, ClipCascadeBackgroundService::class.java).apply {
            this.action = action
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(intent)
        } else {
            startService(intent)
        }
    }
}
