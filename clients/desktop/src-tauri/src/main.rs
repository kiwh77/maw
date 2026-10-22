#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::thread;
use std::time::Duration;

use tauri::{
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager, WindowEvent,
};

const HUB: &str = "http://127.0.0.1:7420";

struct HubChild(Mutex<Option<Child>>);

fn hub_ready() -> bool {
    let Ok(resp) = ureq_get(&format!("{HUB}/api/board")) else {
        return false;
    };
    resp
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

fn ensure_hub(state: &HubChild) {
    if hub_ready() {
        return;
    }
    let mut guard = state.0.lock().unwrap();
    if guard.is_some() {
        return;
    }
    match Command::new("maw")
        .args(["serve", "--addr", "127.0.0.1:7420"])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
    {
        Ok(child) => {
            *guard = Some(child);
            for _ in 0..40 {
                if hub_ready() {
                    break;
                }
                thread::sleep(Duration::from_millis(250));
            }
        }
        Err(err) => {
            eprintln!("failed to spawn `maw serve`: {err}");
        }
    }
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .manage(HubChild(Mutex::new(None)))
        .setup(|app| {
            ensure_hub(app.state::<HubChild>().inner());

            let show = MenuItem::with_id(app, "show", "显示看板", true, None::<&str>)?;
            let quit = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show, &quit])?;

            let _tray = TrayIconBuilder::new()
                .menu(&menu)
                .tooltip("maw")
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "quit" => {
                        if let Some(child) = app.state::<HubChild>().0.lock().unwrap().take() {
                            let _ = child; // drop; OS reaps; serve may keep running if shared
                        }
                        app.exit(0);
                    }
                    "show" => {
                        if let Some(w) = app.get_webview_window("main") {
                            let _ = w.show();
                            let _ = w.set_focus();
                        }
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
                        if let Some(w) = app.get_webview_window("main") {
                            let _ = w.show();
                            let _ = w.set_focus();
                        }
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
