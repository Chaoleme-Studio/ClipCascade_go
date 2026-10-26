package com.clipcascade.android

import android.accessibilityservice.AccessibilityService
import android.content.Intent
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.accessibility.AccessibilityEvent

class ClipCascadeAccessibilityService : AccessibilityService() {

    private val TAG = "ClipCascade_A11y"

    private val handler = Handler(Looper.getMainLooper())
    private var lastTriggerTime: Long = 0
    private val DEBOUNCE_DELAY_MS = 1000L // 1秒防抖，防止短时间多次触发

    // 延迟执行实际读取动作的 Runnable
    private val copyTriggerRunnable = Runnable {
        Log.i(TAG, "🟢 触发剪贴板同步检测流程...")
        startBackgroundClipboardSync()
    }

    override fun onServiceConnected() {
        super.onServiceConnected()
        Log.i(TAG, "🚀 ClipCascade 无障碍服务已启动")
        startBackgroundClipboardSync()
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent?) {
        if (event == null) return

        // 排除自身应用产生的事件，防止无限循环
        if (event.packageName?.toString() == packageName) {
            return
        }

        // 核心逻辑：监听系统“复制”相关的事件。
        // 由于不同手机厂商对“已复制到剪贴板”的提示不同（可能是 Toast，可能是系统 Announcement）
        // 我们通过多种事件类型综合判断。
        when (event.eventType) {
            // 类型1: 文本发生选择或改变，极大可能是复制前奏或复制动作
            AccessibilityEvent.TYPE_VIEW_TEXT_SELECTION_CHANGED,
            AccessibilityEvent.TYPE_VIEW_CLICKED -> {
                // 不严格检测，直接尝试进入防抖阶段触发读取
                debounceAndTriggerCopy()
            }
            
            // 类型2: 系统吐司 (Toast) 或弹窗通知状态改变
            AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED -> {
                val text = event.text.toString()
                // 大部分手机复制时会弹 Toast，例如 "已复制到剪贴板"
                if (text.contains("复制") || text.contains("copy") || text.contains("Copied")) {
                    Log.i(TAG, "🎯 识别到强特征复制 Toast，立即触发同步")
                    debounceAndTriggerCopy(forceImmediate = true)
                } else {
                     debounceAndTriggerCopy()
                }
            }
            
            // 类型3: 系统的无障碍语音播报
            AccessibilityEvent.TYPE_ANNOUNCEMENT -> {
                val text = event.text.toString()
                if (text.contains("复制") || text.contains("copy")) {
                    debounceAndTriggerCopy(forceImmediate = true)
                } else {
                     debounceAndTriggerCopy()
                }
            }
        }
    }

    /**
     * 防抖触发器：过滤高频连续事件，降低功耗
     */
    private fun debounceAndTriggerCopy(forceImmediate: Boolean = false) {
        val currentTime = System.currentTimeMillis()
        
        // 如果距离上次触发太近（小于防抖时间），且不是强制立刻触发，就跳过
        if (!forceImmediate && currentTime - lastTriggerTime < DEBOUNCE_DELAY_MS) {
            return
        }

        lastTriggerTime = currentTime
        
        // 移除之前还在等待中的任务
        handler.removeCallbacks(copyTriggerRunnable)
        
        if (forceImmediate) {
            // 延迟一点点时间（例如 300ms），等待系统把文本实际写入系统剪贴板
            handler.postDelayed(copyTriggerRunnable, 300)
        } else {
            // 普通猜测事件，延迟时间长一点，确保是真的复制完在操作
            handler.postDelayed(copyTriggerRunnable, 1200)
        }
    }

    override fun onInterrupt() {
        Log.i(TAG, "⏸️ 无障碍服务被中断")
    }

    override fun onUnbind(intent: Intent?): Boolean {
        Log.i(TAG, "🛑 无障碍服务正在解绑")
        handler.removeCallbacks(copyTriggerRunnable)
        return super.onUnbind(intent)
    }

    private fun startBackgroundClipboardSync() {
        val intent = Intent(this, ClipCascadeBackgroundService::class.java).apply {
            action = ClipCascadeBackgroundService.ACTION_SYNC_CLIPBOARD_NOW
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(intent)
        } else {
            startService(intent)
        }
    }
}
