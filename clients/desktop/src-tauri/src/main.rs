#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::thread;
use std::time::Duration;

use tauri::{
    include_image,
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager, Url, WindowEvent,
};

const HUB: &str = "http://127.0.0.1:7420";
const HUB_ADDR: &str = "127.0.0.1:7420";

struct HubChild(Mutex<Option<Child>>);

fn ui_lang_zh() -> bool {
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
        if let Ok(url) = Url::parse(&format!("{HUB}/")) {
            let _ = w.navigate(url);
        } else {
            let _ = w.reload();
        }
        let _ = w.show();
        let _ = w.set_focus();
    }
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .manage(HubChild(Mutex::new(None)))
        .setup(|app| {
            let ready = ensure_hub(app.state::<HubChild>().inner());
            if ready {
                open_hub_window(app.handle());
            } else {
                eprintln!("hub not ready; window shows bootstrap page until maw serve is available");
            }

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
                let _ = window.hide();
                api.prevent_close();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running maw desktop");
}
