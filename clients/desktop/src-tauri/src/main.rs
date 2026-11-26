#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod bridge;
mod pump;

use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::thread;
use std::time::Duration;

use tauri::{
    include_image,
    menu::{Menu, MenuItem},
    plugin::Plugin,
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager, Runtime, Url, Webview, WindowEvent,
};

const HUB: &str = "http://127.0.0.1:7420";
const HUB_ADDR: &str = "127.0.0.1:7420";

struct HubChild(Mutex<Option<Child>>);

/// NavGuard 是 webview 的导航白名单（VN-902）。
///
/// 挂在应用级而不是某一处 `navigate` 调用上：页面里的链接、脚本跳转、面板内的
/// 表单提交都要经过它。判据在 `bridge::nav`，这里只负责把「不许」变成「取消导航」。
struct NavGuard;

impl<R: Runtime> Plugin<R> for NavGuard {
    fn name(&self) -> &'static str {
        "maw-nav-guard"
    }

    fn on_navigation(&mut self, _webview: &Webview<R>, url: &Url) -> bool {
        match bridge::nav::allows(url) {
            Ok(()) => true,
            Err(denied) => {
                // 只记原因，不记 URL 原文：URL 里可能带查询参数。
                eprintln!("desktop bridge: blocked navigation ({})", denied.reason());
                false
            }
        }
    }
}

pub(crate) fn ui_lang_zh() -> bool {
    for key in ["MAW_LANG", "LC_ALL", "LC_MESSAGES", "LANG"] {
        if let Ok(v) = std::env::var(key) {
            let t = v.trim().to_ascii_lowercase();
            if t.is_empty() || t == "c" || t == "posix" {
                continue;
            }
            return t.starts_with("zh");
        }
    }
    false
}

fn tray_labels() -> (&'static str, &'static str) {
    if ui_lang_zh() {
        ("显示看板", "退出")
    } else {
        ("Show board", "Quit")
    }
}

fn hub_ready() -> bool {
    ureq_get(&format!("{HUB}/api/board")).unwrap_or(false)
}

fn ureq_get(url: &str) -> Result<bool, ()> {
    // Avoid extra deps in MVP: use `curl` if present, else TCP connect heuristic via std.
    if let Ok(status) = Command::new("curl")
        .args(["-sf", "-o", "/dev/null", "-w", "%{http_code}", url])
        .output()
    {
        let code = String::from_utf8_lossy(&status.stdout);
        return Ok(code.trim() == "200");
    }
    Ok(false)
}

/// macOS GUI apps inherit a minimal PATH (`/usr/bin:/bin:/usr/sbin:/sbin`),
/// so `Command::new("maw")` fails even when `~/.local/bin/maw` exists.
fn resolve_maw() -> Option<PathBuf> {
    if let Ok(bin) = std::env::var("MAW_BIN") {
        let p = PathBuf::from(bin.trim());
        if p.is_file() {
            return Some(p);
        }
    }

    let mut candidates: Vec<PathBuf> = Vec::new();

    if let Ok(path) = std::env::var("PATH") {
        for dir in std::env::split_paths(&path) {
            candidates.push(dir.join("maw"));
        }
    }

    if let Some(home) = std::env::var_os("HOME") {
        let home = PathBuf::from(home);
        candidates.push(home.join(".local/bin/maw"));
        candidates.push(home.join("bin/maw"));
        candidates.push(home.join("go/bin/maw"));
    }

    candidates.push(PathBuf::from("/opt/homebrew/bin/maw"));
    candidates.push(PathBuf::from("/usr/local/bin/maw"));
    candidates.push(PathBuf::from("/usr/bin/maw"));

    // Prefer a maw next to the app bundle (optional sidecar layout).
    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            candidates.push(dir.join("maw"));
            // .../maw.app/Contents/MacOS → .../maw.app/Contents/Resources/maw
            if let Some(contents) = dir.parent() {
                candidates.push(contents.join("Resources/maw"));
            }
        }
    }

    for c in candidates {
        if is_executable(&c) {
            return Some(c);
        }
    }
    None
}

fn is_executable(path: &Path) -> bool {
    if !path.is_file() {
        return false;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if let Ok(meta) = std::fs::metadata(path) {
            return meta.permissions().mode() & 0o111 != 0;
        }
        false
    }
    #[cfg(not(unix))]
    {
        true
    }
}

fn hub_path_env() -> Option<std::ffi::OsString> {
    let mut dirs: Vec<PathBuf> = Vec::new();
    if let Some(maw) = resolve_maw() {
        if let Some(dir) = maw.parent() {
            dirs.push(dir.to_path_buf());
        }
    }
    if let Some(home) = std::env::var_os("HOME") {
        let home = PathBuf::from(home);
        dirs.push(home.join(".local/bin"));
        dirs.push(home.join("bin"));
    }
    dirs.push(PathBuf::from("/opt/homebrew/bin"));
    dirs.push(PathBuf::from("/usr/local/bin"));
    if let Ok(existing) = std::env::var("PATH") {
        for dir in std::env::split_paths(&existing) {
            if !dirs.iter().any(|d| d == &dir) {
                dirs.push(dir);
            }
        }
    }
    std::env::join_paths(dirs).ok()
}

/// Ensure local hub is up. Returns true when `/api/board` responds 200.
fn ensure_hub(state: &HubChild) -> bool {
    if hub_ready() {
        return true;
    }
    {
        let guard = state.0.lock().unwrap();
        if guard.is_some() {
            drop(guard);
            for _ in 0..40 {
                if hub_ready() {
                    return true;
                }
                thread::sleep(Duration::from_millis(250));
            }
            return hub_ready();
        }
    }

    let Some(maw) = resolve_maw() else {
        eprintln!(
            "failed to locate `maw` binary (GUI PATH is minimal). \
             Install to ~/.local/bin/maw or set MAW_BIN, then reopen the app."
        );
        return false;
    };

    let mut cmd = Command::new(&maw);
    cmd.args(["serve", "--addr", HUB_ADDR])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    if let Some(path) = hub_path_env() {
        cmd.env("PATH", path);
    }

    match cmd.spawn() {
        Ok(child) => {
            *state.0.lock().unwrap() = Some(child);
            for _ in 0..40 {
                if hub_ready() {
                    return true;
                }
                thread::sleep(Duration::from_millis(250));
            }
            let ok = hub_ready();
            if !ok {
                eprintln!(
                    "spawned `{} serve` but hub at {HUB} did not become ready",
                    maw.display()
                );
            }
            ok
        }
        Err(err) => {
            eprintln!("failed to spawn `{} serve`: {err}", maw.display());
            false
        }
    }
}

fn open_hub_window(app: &tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        // 导航目标走白名单：只有本机 hub 根地址会被接受，其它一律留在引导页。
        match bridge::nav::hub_root() {
            Ok(url) => {
                let _ = w.navigate(url);
            }
            Err(denied) => {
                eprintln!(
                    "desktop bridge: refused hub navigation ({})",
                    denied.reason()
                );
                let _ = w.reload();
            }
        }
        let _ = w.show();
        let _ = w.set_focus();
    }
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_notification::init())
        .plugin(NavGuard)
        .manage(HubChild(Mutex::new(None)))
        .setup(|app| {
            let ready = ensure_hub(app.state::<HubChild>().inner());
            if ready {
                open_hub_window(app.handle());
            } else {
                eprintln!("hub not ready; window shows bootstrap page until maw serve is available");
            }

            // 提醒桥接与「hub 是否就绪」解耦：订阅线程自己退避重连，
            // hub 晚一点起来也能接上，不需要重启桌面壳。
            pump::spawn(app.handle().clone());

            let (show_label, quit_label) = tray_labels();
            let show = MenuItem::with_id(app, "show", show_label, true, None::<&str>)?;
            let quit = MenuItem::with_id(app, "quit", quit_label, true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show, &quit])?;

            // Color tray mark; macOS template variants live beside it as tray-template*.png.
            let _tray = TrayIconBuilder::new()
                .icon(include_image!("icons/tray-color.png"))
                .menu(&menu)
                .tooltip("maw")
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "quit" => {
                        if let Some(mut child) = app.state::<HubChild>().0.lock().unwrap().take() {
                            let _ = child.kill();
                            let _ = child.wait();
                        }
                        app.exit(0);
                    }
                    "show" => {
                        // Retry hub start if the first attempt failed (e.g. maw installed later).
                        let _ = ensure_hub(app.state::<HubChild>().inner());
                        open_hub_window(app);
                    }
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        let app = tray.app_handle();
                        let _ = ensure_hub(app.state::<HubChild>().inner());
                        open_hub_window(app);
                    }
                })
                .build(app)?;

            Ok(())
        })
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                // Hide to tray instead of quitting.
                //
                // 关窗只是收起界面：桌面壳不持有 Runner，也从不因界面动作去停
                // 受管运行；运行的推进与停止由 hub/CLI 负责，窗口关掉不影响它们。
                let _ = window.hide();
                api.prevent_close();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running maw desktop");
}
