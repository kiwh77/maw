# Brand assets

Source masters for the maw app mark (generated 2026-10-22).

| File | Use |
| --- | --- |
| `maw-icon-1024.png` | App icon master (slate background) |
| `maw-icon-transparent-for-dark.png` | Transparent mark on dark UI |
| `maw-icon-transparent-for-light.png` | Transparent mark on light UI |
| `maw-statusbar-Template.png` / `@2x` | macOS menu-bar template (monochrome) |
| `maw-statusbar-color.png` / `@2x` | Color tray / status mark |

Packaged Tauri icons are derived into `../src-tauri/icons/` (`icon.icns`, `icon.ico`, PNGs, tray copies).

To regenerate Tauri sizes from the 1024 master (macOS):

```bash
# from repo root
MASTER=clients/desktop/brand/maw-icon-1024.png
# then re-run sips/iconutil steps, or: cargo tauri icon "$MASTER"
```
