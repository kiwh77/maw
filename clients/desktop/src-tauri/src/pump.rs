//! 事件流订阅与系统通知投递。
//!
//! 只做三件事：订阅 hub 的事件流、把每一帧交给 `bridge` 的判据、按结论弹窗或只留站内。
//! 所有判断都在 `bridge` 里（有单测），这里只有 I/O，因此不承担语义。
//!
//! 为什么用 `curl` 而不是加一个 HTTP 依赖：`main.rs` 探测 hub 已经用 `curl`（MVP 约定），
//! 事件流只需要「按行读」，`curl -sN` 正好够用，不值得为此引入异步运行时。
//!
//! **可恢复**靠游标，不靠连接：SSE 只保证「有变化」的提示，一旦断线、
//! 或积压超过单次突发上限（`catchup` 帧），都必须按 `/api/feed?since=` 补拉。
//! 因此本模块维护 `last_since`，每次连上先补拉一次再收帧；补拉不到就不推进游标。
//!
//! **首次启动静默对齐**：第一次连上时游标从 0 补到当前水位，但只记账不弹窗。
//! 理由与 hub 的提醒策略一致——历史积压不是「刚刚发生的介入需求」，
//! 全量重放会把用户训练成无脑关掉弹窗；站内记录本来就在，不靠弹窗补。
//!
//! 断线与重连：读循环结束就退避 3 秒重连，并丢弃半截帧。重连不碰任何进程——
//! Runner 的生命周期属于 hub 与 CLI，桌面壳既不启动也不停止它。

use std::io::{BufRead, BufReader};
use std::process::{Command, Stdio};
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use tauri::plugin::PermissionState;
use tauri::AppHandle;
use tauri_plugin_notification::NotificationExt;

use crate::bridge::{
    feed,
    frame::{Frame, Parser},
    gate::{labels, Decision, Gate, Permission},
    redact::scrub,
    reminder::{classify_frame, Fact},
    DEDUP_WINDOW, HUB_ORIGIN,
};

/// 重连退避。
const RECONNECT_BACKOFF: Duration = Duration::from_secs(3);
/// 单条 curl 连接的最长存活时间：到点重连一次，避免长时间半死连接。
const STREAM_MAX_AGE: &str = "1800";
/// 补拉单次最多追多少页：够追平正常积压，又不至于卡死在一个坏 hub 上。
const MAX_CATCHUP_PAGES: usize = 32;

/// 订阅线程自己的游标状态。
struct Cursor {
    /// 已处理到的事件自增身份（`/api/feed?since=`）。
    last_since: i64,
    /// 是否已做过首次静默对齐。
    primed: bool,
}

/// 启动订阅线程。只调用一次（放在 setup 里），失败靠重连自愈。
pub fn spawn(app: AppHandle) {
    thread::spawn(move || run(app));
}

fn run(app: AppHandle) {
    let mut gate = Gate::default();
    let mut cursor = Cursor { last_since: 0, primed: false };
    loop {
        subscribe_once(&app, &mut gate, &mut cursor);
        thread::sleep(RECONNECT_BACKOFF);
    }
}

fn subscribe_once(app: &AppHandle, gate: &mut Gate, cursor: &mut Cursor) {
    let endpoint = format!("{HUB_ORIGIN}/api/events");
    let mut child = match Command::new("curl")
        .args(["-sN", "--max-time", STREAM_MAX_AGE, &endpoint])
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
    {
        Ok(child) => child,
        Err(err) => {
            // 记一行就够：hub 没起来时这行会重复出现，不该刷屏也不该带 URL 之外的上下文。
            eprintln!(
                "desktop bridge: cannot start event stream: {}",
                scrub(&err.to_string())
            );
            return;
        }
    };

    let Some(stdout) = child.stdout.take() else {
        let _ = child.kill();
        let _ = child.wait();
        return;
    };

    // 连上先补拉：断线期间、以及首次启动的积压都在这里对齐。
    catch_up(app, gate, cursor);

    let mut parser = Parser::default();
    for line in BufReader::new(stdout).lines().map_while(Result::ok) {
        for frame in parser.push_line(&line) {
            handle_frame(app, gate, cursor, &frame);
        }
    }
    // 半截帧不是事实：断线即丢弃，避免和新连接的字节拼在一起。
    parser.reset();
    let _ = child.kill();
    let _ = child.wait();
}

fn handle_frame(app: &AppHandle, gate: &mut Gate, cursor: &mut Cursor, frame: &Frame) {
    match frame.event.as_str() {
        // 积压超限：hub 明确要求按 since 补拉，不许跳到 watermark。
        // 提示里的 since 可能比本地游标旧（重复提示），取两者较大者。
        "catchup" => {
            if let Some(since) = feed::catchup_since(&frame.data) {
                cursor.last_since = cursor.last_since.max(since);
            }
            catch_up(app, gate, cursor);
        }
        // 连接就绪：没有正文，什么也不做。
        "ready" => {}
        // 失效提示：事实靠事件正文与补拉，这里不解释。
        "board-changed" => {}
        _ => {
            let Some(fact) = classify_frame(frame) else {
                return; // 表外事件：不弹、不记、不猜
            };
            // 推送里的正文也要推进游标：否则重连后会把它们当积压再弹一遍。
            if let Ok(v) = serde_json::from_str::<serde_json::Value>(frame.data.trim()) {
                if let Some(id) = feed::event_id(&v) {
                    cursor.last_since = cursor.last_since.max(id);
                }
            }
            dispatch(app, gate, &fact);
        }
    }
}

/// 按游标补拉，直到没有积压或达到页数上限。
///
/// 首次对齐（`primed == false`）只推进游标、不弹窗：历史积压不是「刚刚发生的介入需求」，
/// 全量重放会把用户训练成无脑关掉弹窗；站内记录本来就在，不靠弹窗补。
fn catch_up(app: &AppHandle, gate: &mut Gate, cursor: &mut Cursor) {
    for _ in 0..MAX_CATCHUP_PAGES {
        let url = format!("{HUB_ORIGIN}/api/feed?since={}", cursor.last_since);
        let body = match curl_get(&url) {
            Some(body) => body,
            None => return, // hub 没起来：保持游标，下次重连再追
        };
        let Some(batch) = feed::parse_batch(&body) else {
            eprintln!("desktop bridge: unreadable feed page, cursor held");
            return;
        };
        let silent = !cursor.primed;
        for candidate in &batch.events {
            let Some(fact) = crate::bridge::reminder::classify_value(candidate) else {
                continue;
            };
            if !silent {
                dispatch(app, gate, &fact);
            }
        }
        let advanced = match batch.next_since {
            Some(next) if next > cursor.last_since => {
                cursor.last_since = next;
                true
            }
            _ => false,
        };
        // 没有可推进的位置说明服务端没给出下一步：停在这里，别空转。
        if !batch.has_more || !advanced {
            if !cursor.primed {
                cursor.primed = true;
                // 只在「首次对齐完成」时记一行：此后上游有没有新事件由后续日志体现。
                eprintln!(
                    "desktop bridge: subscribed, primed at feed cursor {}",
                    cursor.last_since
                );
            }
            return;
        }
    }
    // 追不平也不回退游标：下次重连继续从 last_since 追。
    cursor.primed = true;
}

fn dispatch(app: &AppHandle, gate: &mut Gate, fact: &Fact) {
    let permission = permission(app);
    let now_ms = now_ms();
    let decision = gate.decide(fact, permission, now_ms, DEDUP_WINDOW.as_millis() as u64);
    match decision {
        Decision::Notify => deliver(app, gate, fact),
        Decision::InApp(why) => log_event("in-app only", fact, &format!("{why:?}")),
        Decision::Skip(why) => log_event("skipped", fact, &format!("{why:?}")),
    }
}

/// 只问一次权限；`Prompt` 当场请求，被拒就按「只留站内」处理。
fn permission(app: &AppHandle) -> Permission {
    let state = match app.notification().permission_state() {
        Ok(PermissionState::Granted) => return Permission::Granted,
        Ok(PermissionState::Prompt) | Ok(PermissionState::PromptWithRationale) => {
            app.notification().request_permission()
        }
        _ => return Permission::Denied,
    };
    match state {
        Ok(PermissionState::Granted) => Permission::Granted,
        _ => Permission::Denied,
    }
}

fn deliver(app: &AppHandle, gate: &mut Gate, fact: &Fact) {
    let zh = crate::ui_lang_zh();
    let (title, kind_label) = labels(fact.kind, zh);
    let body = format!("{kind_label} · {}", fact.subject);
    match app.notification().builder().title(title).body(body).show() {
        Ok(()) => log_event("notified", fact, kind_label),
        Err(err) => {
            // 弹窗失败不改变权威状态：站内提醒仍在，只是这次没弹到桌面上。
            // 撤销记账，让下一次同类事实还能重试。
            gate.forget(fact);
            log_event("notify failed, in-app stands", fact, &err.to_string());
        }
    }
}

/// `curl -sf` 取一段文本；失败一律 `None`（联网失败不是事实，不能推进游标）。
fn curl_get(url: &str) -> Option<String> {
    let out = Command::new("curl")
        .args(["-sf", "--max-time", "10", url])
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .output()
        .ok()?;
    if !out.status.success() {
        return None;
    }
    String::from_utf8(out.stdout).ok()
}

/// 日志只写事件身份与结论，绝不写事件正文：正文里可能带任务输入。
fn log_event(outcome: &str, fact: &Fact, detail: &str) {
    eprintln!(
        "desktop bridge: {} {} {}",
        outcome,
        scrub(&fact.event_id),
        scrub(detail)
    );
}

fn now_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0)
}
